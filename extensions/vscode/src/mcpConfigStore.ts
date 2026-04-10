import * as vscode from 'vscode';
import * as path from 'path';
import * as fs from 'fs';
import * as os from 'os';

// ─── Types ───────────────────────────────────────────────────

export interface McpServerEntry {
    command?: string;
    args?: string[];
    env?: Record<string, string>;
    headers?: Record<string, string>;
    url?: string;
}

export interface McpConfigData {
    servers: Record<string, McpServerEntry>;
    /** The raw key used in the JSON file: "mcpServers" or "servers" */
    serverKey: string;
    /** The raw parsed JSON (for rewriting) */
    raw: any;
}

export interface McpServerStatus {
    name: string;
    /** Original upstream command (from config.yml if proxied, else from mcp.json) */
    upstream: string;
    /** Environment variables from mcp.json */
    env: Record<string, string>;
    /** Headers from mcp.json */
    headers: Record<string, string>;
    /**
     * True ONLY when BOTH conditions are met:
     * 1. Server exists in config.yml (upstream saved)
     * 2. Server's command in mcp.json has been replaced with curb
     */
    isProxied: boolean;
}

export interface McpLocationOption {
    label: string;
    path: string;
    exists: boolean;
    isWorkspace: boolean;
}

// ─── GlobalState Keys ────────────────────────────────────────

const STATE_KEY_MCP_PATH = 'curb.mcpConfigPath';

// ─── GlobalState Helpers ─────────────────────────────────────

export function getMcpConfigPath(context: vscode.ExtensionContext): string | undefined {
    return context.globalState.get<string>(STATE_KEY_MCP_PATH);
}

export async function setMcpConfigPath(context: vscode.ExtensionContext, configPath: string): Promise<void> {
    await context.globalState.update(STATE_KEY_MCP_PATH, configPath);
}

export async function clearMcpConfigPath(context: vscode.ExtensionContext): Promise<void> {
    await context.globalState.update(STATE_KEY_MCP_PATH, undefined);
}

// ─── Known Locations ─────────────────────────────────────────

export function getKnownMcpLocations(): McpLocationOption[] {
    const home = os.homedir();
    const locations: McpLocationOption[] = [];

    // Workspace-level locations
    const wsFolders = vscode.workspace.workspaceFolders;
    if (wsFolders) {
        for (const folder of wsFolders) {
            const vscodeMcp = path.join(folder.uri.fsPath, '.vscode', 'mcp.json');
            locations.push({
                label: `${path.basename(folder.uri.fsPath)}/.vscode/mcp.json`,
                path: vscodeMcp,
                exists: fs.existsSync(vscodeMcp),
                isWorkspace: true,
            });

            const cursorMcp = path.join(folder.uri.fsPath, '.cursor', 'mcp.json');
            locations.push({
                label: `${path.basename(folder.uri.fsPath)}/.cursor/mcp.json`,
                path: cursorMcp,
                exists: fs.existsSync(cursorMcp),
                isWorkspace: true,
            });
        }
    }

    // Global locations
    const globalLocations: { label: string; filePath: string }[] = [
        { label: '~/.cursor/mcp.json', filePath: path.join(home, '.cursor', 'mcp.json') },
        { label: '~/.codeium/windsurf/mcp_config.json', filePath: path.join(home, '.codeium', 'windsurf', 'mcp_config.json') },
        { label: '~/.gemini/antigravity/mcp_config.json', filePath: path.join(home, '.gemini', 'antigravity', 'mcp_config.json') },
    ];

    for (const loc of globalLocations) {
        locations.push({
            label: loc.label,
            path: loc.filePath,
            exists: fs.existsSync(loc.filePath),
            isWorkspace: false,
        });
    }

    return locations;
}

// ─── Config Parsing ──────────────────────────────────────────

/**
 * Read and parse the user-selected mcp.json file.
 * Handles both `mcpServers` (Cursor, Claude) and `servers` (VS Code) keys.
 */
export function parseMcpConfig(configPath: string): McpConfigData | null {
    try {
        if (!fs.existsSync(configPath)) {
            return null;
        }
        const content = fs.readFileSync(configPath, 'utf8');
        const data = JSON.parse(content);

        let serverKey = 'servers';
        let servers: Record<string, McpServerEntry> = {};

        if (data.mcpServers && typeof data.mcpServers === 'object') {
            serverKey = 'mcpServers';
            servers = data.mcpServers;
        } else if (data.servers && typeof data.servers === 'object') {
            serverKey = 'servers';
            servers = data.servers;
        }

        return { servers, serverKey, raw: data };
    } catch (e) {
        console.error('[Curb] Failed to parse MCP config:', e);
        return null;
    }
}

// ─── Proxy Status ────────────────────────────────────────────

/**
 * Parse the config.yml to extract proxied MCP server names.
 * Returns a map of { serverName: upstreamCommand }.
 */
function getProxiedServersFromConfigYml(): Record<string, string> {
    const configPath = path.join(os.homedir(), '.curb', 'config.yml');
    const result: Record<string, string> = {};

    try {
        if (!fs.existsSync(configPath)) {
            return result;
        }
        const content = fs.readFileSync(configPath, 'utf8');
        const lines = content.split('\n');

        let inMcp = false;
        let inServers = false;
        let currentServer = '';

        for (const line of lines) {
            const trimmed = line.trimEnd();
            if (trimmed.startsWith('#')) { continue; }

            // Detect 'mcp:' section
            if (/^mcp:/.test(trimmed)) {
                inMcp = true;
                continue;
            }

            if (inMcp && /^\s{4}servers:/.test(trimmed)) {
                inServers = true;
                continue;
            }

            if (inServers) {
                // Server name (8 spaces indent)
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

                // Exit section on non-indented line
                if (trimmed.length > 0 && !trimmed.startsWith(' ') && !trimmed.startsWith('#')) {
                    inMcp = false;
                    inServers = false;
                }
            }
        }
    } catch (e) {
        console.error('[Curb] Failed to parse config.yml for proxy status:', e);
    }

    return result;
}

/**
 * Get the full MCP status: list of servers from mcp.json with proxy status.
 */
export function getMcpStatus(configPath: string): { servers: McpServerStatus[]; unproxiedCount: number } {
    const config = parseMcpConfig(configPath);
    if (!config) {
        return { servers: [], unproxiedCount: 0 };
    }

    const configYmlServers = getProxiedServersFromConfigYml();
    const servers: McpServerStatus[] = [];
    let unproxiedCount = 0;

    for (const [name, entry] of Object.entries(config.servers)) {
        // Check if mcp.json command has been replaced with Curb
        const isRewrittenInMcpJson = entry.command ? entry.command.includes('curb') : false;

        // Check if server exists in config.yml with an upstream saved
        const existsInConfigYml = name in configYmlServers;

        // Proxied = BOTH conditions true:
        // 1. Server is in config.yml (original upstream saved)
        // 2. Server command in mcp.json points to Curb (rewritten)
        const isProxied = existsInConfigYml && isRewrittenInMcpJson;

        // Determine the upstream to display:
        // - If rewritten, show the original from config.yml
        // - Otherwise, show the current command from mcp.json
        let upstream = '';
        if (isRewrittenInMcpJson && existsInConfigYml) {
            upstream = configYmlServers[name];
        } else {
            if (entry.command) {
                upstream = entry.command;
                if (entry.args && entry.args.length > 0) {
                    upstream += ' ' + entry.args.join(' ');
                }
            }
        }

        if (!isProxied) {
            unproxiedCount++;
        }

        servers.push({
            name,
            upstream,
            env: entry.env || {},
            headers: entry.headers || {},
            isProxied,
        });
    }

    return { servers, unproxiedCount };
}

// ─── Proxy/Unproxy Operations ────────────────────────────────

/**
 * Proxy a single MCP server:
 * 1. Save the original upstream to config.yml via RPC
 * 2. Rewrite the mcp.json entry to route through curb
 */
export function rewriteMcpJsonEntry(
    configPath: string,
    serverName: string,
    curbBinaryPath: string
): { originalUpstream: string, originalEnv: Record<string, string>, originalHeaders: Record<string, string> } | null {
    try {
        const content = fs.readFileSync(configPath, 'utf8');
        const parsed = JSON.parse(content);
        const serverKey = parsed.mcpServers ? 'mcpServers' : 'servers';
        const mcpMap = parsed[serverKey];

        if (!mcpMap || !mcpMap[serverName]) {
            return null;
        }

        const entry = mcpMap[serverName];

        // Build original upstream string before rewriting
        let originalUpstream = entry.command || '';
        if (entry.args && entry.args.length > 0) {
            originalUpstream += ' ' + entry.args.join(' ');
        }

        // Don't double-proxy
        if (entry.command && entry.command.includes('curb')) {
            return { originalUpstream, originalEnv: entry.env || {}, originalHeaders: entry.headers || {} };
        }

        // Rewrite the entry
        const curbBinPath = path.join(os.homedir(), '.curb', 'bin', process.platform === 'win32' ? 'curb.exe' : 'curb');

        mcpMap[serverName] = {
            ...entry,
            command: curbBinPath,
            args: ['mcp-proxy', serverName],
            env: { ...(entry.env || {}) },
        };

        fs.writeFileSync(configPath, JSON.stringify(parsed, null, 2));
        return { originalUpstream, originalEnv: entry.env || {}, originalHeaders: entry.headers || {} };
    } catch (e) {
        console.error(`[Curb] Failed to rewrite mcp.json for ${serverName}:`, e);
        return null;
    }
}

/**
 * Restore a single MCP server's original command from config.yml upstream.
 */
export function restoreMcpJsonEntry(
    configPath: string,
    serverName: string,
    originalUpstream: string
): boolean {
    try {
        const content = fs.readFileSync(configPath, 'utf8');
        const parsed = JSON.parse(content);
        const serverKey = parsed.mcpServers ? 'mcpServers' : 'servers';
        const mcpMap = parsed[serverKey];

        if (!mcpMap || !mcpMap[serverName]) {
            return false;
        }

        const parts = originalUpstream.split(' ');
        mcpMap[serverName].command = parts[0];
        mcpMap[serverName].args = parts.slice(1);



        fs.writeFileSync(configPath, JSON.stringify(parsed, null, 2));
        return true;
    } catch (e) {
        console.error(`[Curb] Failed to restore mcp.json for ${serverName}:`, e);
        return false;
    }
}
