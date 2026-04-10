import * as vscode from 'vscode';
import * as os from 'os';
import * as path from 'path';
import * as fs from 'fs';
import { getMcpConfigPath, getMcpStatus } from './mcpConfigStore';

// Prevent spamming the UI
let lastWarningTime = 0;

export function setupMcpWatcher(context: vscode.ExtensionContext) {
    // Watch the user-selected mcp.json for changes
    const checkForUnproxied = async () => {
        const mcpPath = getMcpConfigPath(context);
        if (!mcpPath) { return; }
        if (!fs.existsSync(mcpPath)) { return; }

        const status = getMcpStatus(mcpPath);
        if (status.unproxiedCount === 0) { return; }

        const now = Date.now();
        if (now - lastWarningTime < 30000) {
            return; // Debounce warnings to once every 30 seconds
        }
        lastWarningTime = now;

        const selection = await vscode.window.showWarningMessage(
            `Curb: ${status.unproxiedCount} unprotected MCP server(s) detected. Open the Dashboard to protect them.`,
            "Open Dashboard",
            "Dismiss"
        );

        if (selection === "Open Dashboard") {
            vscode.commands.executeCommand('curb.openDashboard');
        }
    };

    // Watch all known patterns for mcp config file changes
    const watchers = [
        vscode.workspace.createFileSystemWatcher('**/.vscode/mcp.json'),
        vscode.workspace.createFileSystemWatcher('**/.cursor/mcp.json'),
    ];

    for (const watcher of watchers) {
        context.subscriptions.push(
            watcher.onDidChange(checkForUnproxied),
            watcher.onDidCreate(checkForUnproxied),
            watcher
        );
    }

    // Also watch global paths via polling (FileSystemWatcher doesn't work outside workspace)
    const pollInterval = setInterval(async () => {
        const mcpPath = getMcpConfigPath(context);
        if (!mcpPath) { return; }

        // Only poll for global (non-workspace) configs
        const wsFolders = vscode.workspace.workspaceFolders;
        const isWorkspace = wsFolders?.some(f => mcpPath.startsWith(f.uri.fsPath));
        if (isWorkspace) { return; } // Workspace paths are covered by FileSystemWatcher

        await checkForUnproxied();
    }, 15000);

    context.subscriptions.push({
        dispose: () => clearInterval(pollInterval),
    });

    // Initial check after Curb boots
    setTimeout(checkForUnproxied, 5000);
}
