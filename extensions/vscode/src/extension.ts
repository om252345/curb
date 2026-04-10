import * as vscode from 'vscode';
import * as path from 'path';
import * as fs from 'fs';
import * as os from 'os';
import { startCurb, cachedBlockedPatterns, curbDir } from './binaryManager';
import { setupTerminalGuard, cleanupGuard, removeShellOverrides } from './terminalInterceptor';
import { setupMcpWatcher } from './mcpWatcher';
import { registerDashboardCommand } from './dashboardWebview';
import { getMcpConfigPath, setMcpConfigPath, getKnownMcpLocations } from './mcpConfigStore';
import { registerOnboardingCommand } from './onboardingWebview';

// Module-scope: tracks files we've locked so deactivate() can restore permissions
const lockedFiles = new Map<string, number>(); // filePath → original mode (e.g., 0o644)

export async function activate(context: vscode.ExtensionContext) {
    console.log('Congratulations, your extension "curb" is now active!');

    // Initialize Curb process
    startCurb(context);

    // ─── First-Run Onboarding ─────────────────────────────────────────
    const firstRunPath = path.join(os.homedir(), '.curb', '.firstrun');
    if (!fs.existsSync(firstRunPath)) {
        setTimeout(() => {
            vscode.commands.executeCommand('curb.openOnboarding');
        }, 1000);
    }

    // ─── File Guard: Defense-in-Depth ─────────────────────────────────
    //
    // Layer 1: chmod 444 (OS-level) — blocks ALL file I/O writes (agents, tools, scripts)
    // Layer 2: VS Code session read-only — blocks editor-level edits (typing, paste)
    // Layer 3: FileSystemWatcher — detects if a protected file is modified despite locks
    //
    // Agent tools like Copilot's replace_string_in_file use direct fs.writeFile(),
    // bypassing every VS Code API. chmod is the ONLY reliable prevention.
    // ──────────────────────────────────────────────────────────────────

    const isFileProtected = (fsPath: string): boolean => {
        for (const pattern of cachedBlockedPatterns) {
            const cleanPattern = pattern.replace(/[\*\/]/g, '');
            if (fsPath.includes(cleanPattern)) {
                return true;
            }
        }
        return false;
    };

    // Lock a file: chmod 444 + VS Code session read-only
    const lockFile = async (filePath: string, setEditorReadonly: boolean) => {
        if (lockedFiles.has(filePath)) { return; }

        try {
            const stats = fs.statSync(filePath);
            if (!stats.isFile()) { return; }

            // Store original permissions for cleanup on deactivate
            lockedFiles.set(filePath, stats.mode);

            // Layer 1: OS-level lock — makes fs.writeFile() fail with EACCES
            fs.chmodSync(filePath, 0o444);

            // Layer 2: VS Code session read-only — prevents editor UI edits
            if (setEditorReadonly) {
                await vscode.commands.executeCommand('workbench.action.files.setActiveEditorReadonlyInSession');
            }

            vscode.window.showInformationMessage(
                `Curb: ${path.basename(filePath)} is protected — locked (OS + Editor).`
            );
        } catch (e) {
            console.error('[Curb] Failed to lock file:', filePath, e);
        }
    };

    // Unlock a file: restore original permissions + VS Code session writable
    const unlockFile = async (filePath: string) => {
        const originalMode = lockedFiles.get(filePath);
        if (originalMode === undefined) { return; }

        try {
            fs.chmodSync(filePath, originalMode);
            lockedFiles.delete(filePath);
            await vscode.commands.executeCommand('workbench.action.files.setActiveEditorWriteableInSession');
            vscode.window.showInformationMessage(
                `Curb: ${path.basename(filePath)} unlocked for editing.`
            );
        } catch (e) {
            console.error('[Curb] Failed to unlock file:', filePath, e);
        }
    };

    // Lock the active editor if it's a protected file
    const lockActiveIfProtected = async () => {
        const editor = vscode.window.activeTextEditor;
        if (!editor) { return; }
        const filePath = editor.document.uri.fsPath;
        if (!isFileProtected(filePath)) { return; }
        await lockFile(filePath, true);
    };

    // Watch for active editor changes — lock the moment a protected file is focused
    const editorWatcher = vscode.window.onDidChangeActiveTextEditor(lockActiveIfProtected);

    // Delayed initial check: wait for Curb to boot and populate cachedBlockedPatterns
    setTimeout(lockActiveIfProtected, 3000);

    // Layer 3: FileSystemWatcher — detect if a protected file is modified despite chmod
    // (e.g., root-level access, or file was unlocked then modified by agent)
    const fsWatcher = vscode.workspace.createFileSystemWatcher('**/*');
    fsWatcher.onDidChange(async (uri) => {
        const filePath = uri.fsPath;
        if (!isFileProtected(filePath)) { return; }
        if (!lockedFiles.has(filePath)) {
            // Protected file was modified and wasn't locked — lock it now
            vscode.window.showWarningMessage(
                `Curb: Protected file ${path.basename(filePath)} was modified — locking it now.`
            );
            await lockFile(filePath, false);
        }
    });

    // Proactive workspace scan: after patterns load, find and lock all matching files
    setTimeout(async () => {
        if (cachedBlockedPatterns.length === 0) { return; }
        const wsFolders = vscode.workspace.workspaceFolders;
        if (!wsFolders) { return; }

        for (const pattern of cachedBlockedPatterns) {
            const cleanPattern = pattern.replace(/[\*\/]/g, '');
            if (!cleanPattern) { continue; }

            try {
                // Find files matching the pattern in the workspace
                const files = await vscode.workspace.findFiles(`**/*${cleanPattern}*`, '**/node_modules/**', 50);
                for (const file of files) {
                    if (!lockedFiles.has(file.fsPath)) {
                        await lockFile(file.fsPath, false);
                    }
                }
            } catch (e) {
                console.error('[Curb] Workspace scan error:', e);
            }
        }
    }, 5000);

    // Unlock command for legitimate user edits (HITL escape hatch)
    const unlockCmd = vscode.commands.registerCommand('curb.unlockFile', async () => {
        const editor = vscode.window.activeTextEditor;
        if (!editor) {
            vscode.window.showWarningMessage('Curb: No active editor to unlock.');
            return;
        }
        await unlockFile(editor.document.uri.fsPath);
    });

    context.subscriptions.push(editorWatcher);
    context.subscriptions.push(unlockCmd);
    context.subscriptions.push(fsWatcher);

    let disposable = vscode.commands.registerCommand('curb.setupTerminalGuard', async () => {
        try {
            await setupTerminalGuard(context);

            // --- NEW BRUTE-FORCE FALLBACK ---
            let terminal = vscode.window.activeTerminal;
            if (!terminal) {
                terminal = vscode.window.createTerminal("Curb Guardrail");
            }
            terminal.show();

            const workspaceFolder = vscode.workspace.workspaceFolders![0].uri.fsPath;
            const guardFolder = path.join(workspaceFolder, '.vscode', '.curb', 'bin');

            if (process.platform === 'win32') {
                terminal.sendText(`set PATH=${guardFolder};%PATH%`);
            } else {
                terminal.sendText(`export PATH="${guardFolder}:$PATH"`);
                terminal.sendText(`clear`);
            }
            // ---------------------------------

            vscode.window.showInformationMessage('🛡️ Curb: Terminal protection enabled and activated!');
        } catch (error) {
            vscode.window.showErrorMessage(`Curb: Failed to setup terminal protection: ${error}`);
        }
    });

    context.subscriptions.push(disposable);

    // ─── MCP Config Selection Command ────────────────────────────────
    const selectMcpCmd = vscode.commands.registerCommand('curb.selectMcpConfig', async () => {
        vscode.commands.executeCommand('curb.openOnboarding');
    });
    context.subscriptions.push(selectMcpCmd);

    // Thin Client Step 2: Implement "Synchronous MCP Interceptor"
    setupMcpWatcher(context);

    // Terminal Interceptor
    setupTerminalGuard(context);

    // Thin Client Step 3: Implement Dashboard Webview
    registerDashboardCommand(context);

    // Onboarding webview
    registerOnboardingCommand(context);
}

export function deactivate() {
    // 1. Restore file permissions
    for (const [filePath, originalMode] of lockedFiles) {
        try {
            fs.chmodSync(filePath, originalMode);
        } catch (e) {
            // File may have been deleted or moved
        }
    }
    lockedFiles.clear();

    // 2. Restore MCP configs from config.yml (config-based restore)
    try {
        const configPath = path.join(os.homedir(), '.curb', 'config.yml');
        if (fs.existsSync(configPath)) {
            const configStr = fs.readFileSync(configPath, 'utf8');
            // Simple YAML parse for mcp.servers to get original upstream commands
            // We look for servers that have been proxied and restore them
            const serverUpstreams = parseServerUpstreamsFromYaml(configStr);

            if (Object.keys(serverUpstreams).length > 0) {
                // Scan all known MCP config files and restore proxied servers
                const allPaths = getAllMcpConfigPaths();
                for (const mcpPath of allPaths) {
                    try {
                        if (!fs.existsSync(mcpPath)) continue;
                        const content = fs.readFileSync(mcpPath, 'utf8');
                        const parsed = JSON.parse(content);
                        const serverKey = parsed.mcpServers ? 'mcpServers' : 'servers';
                        const mcpMap = parsed[serverKey];
                        if (!mcpMap) continue;

                        let changed = false;
                        for (const name of Object.keys(mcpMap)) {
                            const srv = mcpMap[name];
                            // Check if this server is curb-proxied
                            if (srv.command && srv.command.includes('curb')) {
                                const upstream = serverUpstreams[name];
                                if (upstream) {
                                    // Restore original command from config.yml
                                    const parts = upstream.split(' ');
                                    mcpMap[name].command = parts[0];
                                    mcpMap[name].args = parts.slice(1);

                                    changed = true;
                                }
                            }
                        }

                        if (changed) {
                            fs.writeFileSync(mcpPath, JSON.stringify(parsed, null, 2));
                        }
                    } catch { /* skip individual file errors */ }
                }
            }
        }
    } catch (e) {
        console.error('[Curb] MCP restore error:', e);
    }

    // 3. Clean up terminal trap scripts and settings.json overrides
    const wsFolders = vscode.workspace.workspaceFolders;
    if (wsFolders && wsFolders.length > 0) {
        const guardFolder = path.join(wsFolders[0].uri.fsPath, '.vscode', '.curb', 'tmpbin');
        try {
            const entries = fs.readdirSync(guardFolder);
            for (const entry of entries) {
                fs.unlinkSync(path.join(guardFolder, entry));
            }
            fs.rmdirSync(guardFolder);
            const curbDirPath = path.dirname(guardFolder);
            const remaining = fs.readdirSync(curbDirPath);
            if (remaining.length === 0) {
                fs.rmdirSync(curbDirPath);
            }
        } catch { /* already cleaned */ }
    }
    
    // Ensure shell overrides are cleaned from settings.json
    removeShellOverrides();
}



// ─── Helpers for config-based MCP restore ──

/**
 * Parse server upstream commands from config.yml content.
 * Returns { serverName: upstreamCommand } map.
 * Uses simple regex parsing to avoid requiring js-yaml dependency.
 */
function parseServerUpstreamsFromYaml(yamlStr: string): Record<string, string> {
    const result: Record<string, string> = {};
    const lines = yamlStr.split('\n');
    let inServers = false;
    let currentServer = '';

    for (const line of lines) {
        const trimmed = line.trimEnd();
        if (trimmed.startsWith('#')) continue;

        // Detect 'servers:' section under 'mcp:'
        if (/^\s{4}servers:/.test(trimmed)) {
            inServers = true;
            continue;
        }

        if (inServers) {
            // Server name line (8 spaces indent)
            const serverMatch = trimmed.match(/^\s{8}(\w[\w-]*):/);
            if (serverMatch) {
                currentServer = serverMatch[1];
                continue;
            }

            // Upstream line (12 spaces indent)
            const upstreamMatch = trimmed.match(/^\s{12}upstream:\s*["']?(.+?)["']?\s*$/);
            if (upstreamMatch && currentServer) {
                result[currentServer] = upstreamMatch[1];
                continue;
            }

            // If we hit a non-indented line, we've left the servers section
            if (trimmed.length > 0 && !trimmed.startsWith(' ') && !trimmed.startsWith('#')) {
                inServers = false;
            }
        }
    }

    return result;
}

/**
 * Get all known MCP config file paths (workspace + global).
 */
function getAllMcpConfigPaths(): string[] {
    return getKnownMcpLocations().map(loc => loc.path);
}
