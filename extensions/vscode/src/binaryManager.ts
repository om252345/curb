import * as vscode from 'vscode';
import * as cp from 'child_process';
import * as path from 'path';
import * as readline from 'readline';
import * as fs from 'fs';
import * as os from 'os';

export let cachedBlockedPatterns: string[] = [];
export let curbConfigPath: string = '';
export let curbDir: string = '';

let binaryProcess: cp.ChildProcess | null = null;
let outputChannel: vscode.OutputChannel;

let nextRequestId = 2;
const pendingRequests = new Map<number, (res: any) => void>();

/**
 * Get the cross-platform Curb home directory.
 *   macOS/Linux: ~/.curb/
 *   Windows:     %USERPROFILE%\.curb\
 */
function getCurbHomeDir(): string {
    return path.join(os.homedir(), '.curb');
}

/**
 * Sync binaries from extension bundle to ~/.curb/bin for global stability.
 */
async function syncBinaries(context: vscode.ExtensionContext): Promise<string> {
    const curbBinDir = path.join(curbDir, 'bin');
    await fs.promises.mkdir(curbBinDir, { recursive: true });

    const binaries = ['curb', 'curb-interceptor', 'curb-mcp'];
    for (const bin of binaries) {
        const sourceName = process.platform === 'win32' ? `${bin}.exe` : bin;
        const sourcePath = path.join(context.extensionPath, 'bin', sourceName);
        const destPath = path.join(curbBinDir, sourceName);

        if (fs.existsSync(sourcePath)) {
            try {
                // Remove old binary first (avoids "text file busy" on some Unixes)
                if (fs.existsSync(destPath)) {
                    await fs.promises.unlink(destPath);
                }
                await fs.promises.copyFile(sourcePath, destPath);
                if (process.platform !== 'win32') {
                    await fs.promises.chmod(destPath, 0o755);
                }
                outputChannel.appendLine(`[Curb] Synced bin: ${sourceName}`);
            } catch (e) {
                console.error(`[Curb] Failed to sync binary ${sourceName}:`, e);
            }
        }
    }
    return curbBinDir;
}


export function sendRpcCommand(method: string, params: any): Promise<any> {
    return new Promise((resolve, reject) => {
        if (!binaryProcess || !binaryProcess.stdin) {
            reject(new Error("No background process active."));
            return;
        }
        const id = nextRequestId++;
        pendingRequests.set(id, resolve);
        const req = JSON.stringify({ id, method, params });
        binaryProcess.stdin.write(req + "\n");
        setTimeout(() => {
            if (pendingRequests.has(id)) {
                pendingRequests.delete(id);
                reject(new Error("RPC Timeout"));
            }
        }, 30000);
    });
}

export async function startCurb(context: vscode.ExtensionContext) {
    console.log('[Curb] Starting Curb Sidecar Binary Manager...');
    outputChannel = vscode.window.createOutputChannel('Curb Sidecar');
    outputChannel.appendLine('Starting Curb Sidecar Binary Manager in serve mode...');

    // ── Cross-platform paths ──
    curbDir = getCurbHomeDir();
    await fs.promises.mkdir(curbDir, { recursive: true });
    const curbDbPath = path.join(curbDir, 'curb.db');
    curbConfigPath = path.join(curbDir, 'config.yml');
    outputChannel.appendLine(`[Curb] Home:   ${curbDir}`);
    outputChannel.appendLine(`[Curb] DB:     ${curbDbPath}`);
    outputChannel.appendLine(`[Curb] Config: ${curbConfigPath}`);

    // ── Sync and Resolve binary path ──
    const curbBinDir = await syncBinaries(context);
    const binaryName = process.platform === 'win32' ? 'curb.exe' : 'curb';
    const binaryPath = path.join(curbBinDir, binaryName);
    outputChannel.appendLine(`[Curb] Binary: ${binaryPath}`);

    // ── Register Curb MCP server ──
    try {
        const mcpBinaryName = process.platform === 'win32' ? 'curb-mcp.exe' : 'curb-mcp';
        const mcpBinaryPath = path.join(curbBinDir, mcpBinaryName);

        // @ts-ignore - VS Code LM API is experimental
        const disposable = vscode.lm.registerMcpServerDefinitionProvider('curb-security-generator', {
            provideMcpServerDefinitions: () => ([{
                label: 'Curb Security Generator',
                command: mcpBinaryPath,
                args: [],
                env: process.env as any
            }])
        });
        context.subscriptions.push(disposable);
        outputChannel.appendLine(`[Curb] MCP server registered: ${mcpBinaryPath}`);
    } catch (err) {
        outputChannel.appendLine(`[Curb] MCP server registration skipped: ${err}`);
    }

    // ── Spawn Curb process ──
    try {
        binaryProcess = cp.spawn(binaryPath, ['serve'], {
            cwd: context.extensionPath,
            env: { ...process.env }
        });

        if (binaryProcess.stdout) {
            const rl = readline.createInterface({
                input: binaryProcess.stdout,
                terminal: false
            });

            rl.on('line', (line) => {
                if (!line.trim()) { return; }

                try {
                    const response = JSON.parse(line);
                    if (response.id && pendingRequests.has(response.id)) {
                        if (response.error) {
                            pendingRequests.get(response.id)!({ error: response.error });
                        } else {
                            pendingRequests.get(response.id)!(response.result);
                        }
                        pendingRequests.delete(response.id);
                    } else if (response.id === 1 && response.result && response.result.blocked_patterns) {
                        cachedBlockedPatterns = response.result.blocked_patterns;
                        outputChannel.appendLine(`[Curb Sync] Cached ${cachedBlockedPatterns.length} blocked patterns.`);
                    } else if (response.method === "vscode_hitl_request") {
                        console.log("[Curb] HITL request:", response);
                        const hitlId = response.params.id;
                        const toolName = response.params.toolName;
                        const target = response.params.target;

                        vscode.window.showWarningMessage(
                            `🚨 Curb: Agent is attempting to execute tool '${toolName}' on server '${target}'. Allow this operation?`,
                            { modal: true },
                            "Approve",
                            "Block"
                        ).then(choice => {
                            const allowed = choice === "Approve";
                            if (binaryProcess && binaryProcess.stdin) {
                                binaryProcess.stdin.write(JSON.stringify({
                                    method: "vscode_hitl_response",
                                    params: { id: hitlId, allowed: allowed }
                                }) + "\n");
                            }
                        });
                    } else {
                        outputChannel.appendLine(`[RPC] ${line}`);
                    }
                } catch (e) {
                    outputChannel.appendLine(`[RAW] ${line}`);
                }
            });
        }

        if (binaryProcess.stderr) {
            const rlErr = readline.createInterface({
                input: binaryProcess.stderr,
                terminal: false
            });
            rlErr.on('line', (line) => {
                outputChannel.appendLine(`[Curb Service] ${line}`);
            });
        }

        binaryProcess.on('exit', (code, signal) => {
            outputChannel.appendLine(`Curb exited (code: ${code}, signal: ${signal})`);
            if (code !== 0 && code !== null) {
                vscode.window.showErrorMessage(
                    `Curb: Process exited unexpectedly (code: ${code}). Protections may be offline.`
                );
            }
        });

        binaryProcess.on('error', (err) => {
            outputChannel.appendLine(`Curb spawn error: ${err.message}`);
            vscode.window.showErrorMessage(`Curb: Failed to start: ${err.message}`);
        });

        // Boot sync: request file rules immediately
        if (binaryProcess.stdin) {
            const request = JSON.stringify({
                id: 1,
                method: "get_file_rules",
                params: {}
            });
            binaryProcess.stdin.write(request + "\n");
        }

        // Graceful teardown on deactivate
        context.subscriptions.push({
            dispose: () => {
                if (binaryProcess) {
                    outputChannel.appendLine('Shutting down Curb process...');
                    binaryProcess.kill();
                }
            }
        });

    } catch (error) {
        outputChannel.appendLine(`Exception spawning Curb: ${error}`);
    }
}
