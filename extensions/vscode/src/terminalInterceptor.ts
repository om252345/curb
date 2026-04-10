import * as vscode from 'vscode';
import * as path from 'path';
import * as fs from 'fs';
import * as os from 'os';

/**
 * Get the path to the curb binary.
 * Uses the extension's bundled binary.
 */
function getCurbBinaryPath(context: vscode.ExtensionContext): string {
    const binaryName = process.platform === 'win32' ? 'curb.exe' : 'curb';
    return path.join(os.homedir(), '.curb', 'bin', binaryName);
}

/**
 * Get the guard folder path inside the workspace's .vscode directory.
 */
function getGuardFolder(): string | null {
    const wsFolders = vscode.workspace.workspaceFolders;
    if (!wsFolders || wsFolders.length === 0) { return null; }
    return path.join(wsFolders[0].uri.fsPath, '.vscode', '.curb', 'tmpbin');
}

/**
 * Generate a wrapper script that calls curb evaluate.
 * Cross-platform: bash for macOS/Linux, .cmd for Windows.
 */
function generateWrapper(command: string, realBinaryPath: string, curbBinaryPath: string): string {
    if (process.platform === 'win32') {
        return `@echo off\r\n` +
            `if not exist "${curbBinaryPath}" goto :runreal\r\n` +
            `"${curbBinaryPath}" evaluate ${command} %* >nul 2>&1\r\n` +
            `if errorlevel 1 (\r\n` +
            `    "${curbBinaryPath}" evaluate ${command} %* >nul\r\n` +
            `    exit /b 1\r\n` +
            `)\r\n` +
            `:runreal\r\n` +
            `"${realBinaryPath}" %*\r\n`;
    }

    return `#!/bin/bash\n` +
        `# Curb CLI wrapper for '${command}'\n` +
        `if [ ! -f "${curbBinaryPath}" ]; then exec "${realBinaryPath}" "$@"; fi\n` +
        `"${curbBinaryPath}" evaluate ${command} "$@" > /dev/null 2>&1\n` +
        `CURB_EXIT=$?\n` +
        `if [ $CURB_EXIT -ne 0 ]; then\n` +
        `    "${curbBinaryPath}" evaluate ${command} "$@" > /dev/null\n` +
        `    exit 1\n` +
        `fi\n` +
        `exec "${realBinaryPath}" "$@"\n`;
}

/**
 * Find the real binary path for a command, skipping the trap folder.
 */
function findRealBinary(command: string, excludeDir: string): string | null {
    const pathEnv = process.env.PATH || '';
    const dirs = pathEnv.split(path.delimiter);

    for (const dir of dirs) {
        if (dir === excludeDir) { continue; }
        const candidate = path.join(dir, command);
        try {
            const stats = fs.statSync(candidate);
            if (stats.isFile()) { return candidate; }
        } catch { /* skip */ }
    }
    return null;
}

/**
 * Set up terminal guard: generate wrapper scripts in .vscode/.curb/bin/
 * and inject them at the front of the integrated terminal's PATH.
 * 
 * Wrapper scripts call `curb evaluate` to check rules from .curb.yml.
 */
export async function setupTerminalGuard(context: vscode.ExtensionContext) {
    const guardFolder = getGuardFolder();
    if (!guardFolder) { return; }

    const curbBinaryPath = getCurbBinaryPath(context);
    const outputChannel = vscode.window.createOutputChannel('Curb Terminal Protection');

    // Commands to protect — derived from common dangerous operations
    // These will be dynamically evaluated against .curb.yml rules
    const commandsToProtect = ['git', 'rm', 'mv', 'cp', 'chmod', 'chown',
        'python', 'python3', 'node', 'npm', 'npx',
        'curl', 'wget', 'ssh', 'scp'];

    try {
        await fs.promises.mkdir(guardFolder, { recursive: true });

        for (const cmd of commandsToProtect) {
            const realPath = findRealBinary(cmd, guardFolder);
            if (!realPath) {
                outputChannel.appendLine(`[Curb] Skipping '${cmd}' — not found in PATH`);
                continue;
            }

            const ext = process.platform === 'win32' ? '.cmd' : '';
            const wrapperPath = path.join(guardFolder, `${cmd}${ext}`);
            const content = generateWrapper(cmd, realPath, curbBinaryPath);

            await fs.promises.writeFile(wrapperPath, content, { mode: 0o755 });
            if (process.platform !== 'win32') {
                await fs.promises.chmod(wrapperPath, 0o755);
            }
        }

        // Inject guard folder at front of integrated terminal's PATH
        context.environmentVariableCollection.clear();
        context.environmentVariableCollection.prepend(
            'PATH',
            `${guardFolder}${path.delimiter}`,
            { applyAtShellIntegration: true }
        );

        outputChannel.appendLine(`[Curb] Enabled terminal protection for ${commandsToProtect.length} commands at ${guardFolder}`);

        // Register cleanup on deactivate
        context.subscriptions.push({
            dispose: () => {
                cleanupGuard(guardFolder, context);
            }
        });

    } catch (err) {
        console.error(`[Curb] Failed to setup terminal guard: ${err}`);
        vscode.window.showErrorMessage(`Curb: Failed to setup shell protection: ${err}`);
    }
}

/**
 * Clean up all Curb artifacts:
 * 1. Remove wrapper scripts from .vscode/.curb/bin/
 * 2. Clear PATH environment variable injection
 * 3. Remove settings.json shell override if we set it
 */
export function cleanupGuard(guardFolder: string | null, context: vscode.ExtensionContext) {
    // 1. Clear PATH injection
    context.environmentVariableCollection.clear();

    // 2. Remove wrapper scripts
    if (guardFolder) {
        try {
            const entries = fs.readdirSync(guardFolder);
            for (const entry of entries) {
                fs.unlinkSync(path.join(guardFolder, entry));
            }
            // Remove the bin/ dir and .curb/ dir if empty
            fs.rmdirSync(guardFolder);
            const curbDir = path.dirname(guardFolder);
            const remaining = fs.readdirSync(curbDir);
            if (remaining.length === 0) {
                fs.rmdirSync(curbDir);
            }
        } catch { /* already cleaned */ }
    }

    // 3. Clean up settings.json shell overrides if we set them
    removeShellOverrides();
}

/**
 * Remove any terminal.integrated.defaultProfile or shell overrides
 * from workspace settings.json that Curb may have injected.
 */
export function removeShellOverrides() {
    const wsFolders = vscode.workspace.workspaceFolders;
    if (!wsFolders || wsFolders.length === 0) { return; }

    const settingsPath = path.join(wsFolders[0].uri.fsPath, '.vscode', 'settings.json');
    if (!fs.existsSync(settingsPath)) { return; }

    try {
        const raw = fs.readFileSync(settingsPath, 'utf-8');
        const settings = JSON.parse(raw);

        // Remove shell-related keys that Curb may have set
        const keysToRemove = [
            'terminal.integrated.defaultProfile.osx',
            'terminal.integrated.defaultProfile.linux',
            'terminal.integrated.defaultProfile.windows',
            'terminal.integrated.shell.osx',
            'terminal.integrated.shell.linux',
            'terminal.integrated.shell.windows',
        ];

        let changed = false;
        for (const key of keysToRemove) {
            if (key in settings) {
                delete settings[key];
                changed = true;
            }
        }

        if (changed) {
            fs.writeFileSync(settingsPath, JSON.stringify(settings, null, 4), 'utf-8');
        }
    } catch { /* settings file may not be valid JSON */ }
}
