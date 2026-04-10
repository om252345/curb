import * as vscode from 'vscode';
import * as fs from 'fs';
import * as path from 'path';

export async function setupGitWrapper(context: vscode.ExtensionContext) {
    // Only set up if there is an active workspace folder
    if (!vscode.workspace.workspaceFolders || vscode.workspace.workspaceFolders.length === 0) {
        return;
    }

    const workspaceFolder = vscode.workspace.workspaceFolders[0].uri.fsPath;
    const binDir = path.join(workspaceFolder, '.vscode', '.curb', 'bin');
    const gitWrapperPath = path.join(binDir, 'git');

    // Absolute path to the bundled Go binary
    const binaryPath = path.join(context.extensionPath, 'bin', 'agentgate');

    try {
        // Ensure the directory exists
        await vscode.workspace.fs.createDirectory(vscode.Uri.file(binDir));

        // Create the wrapper script content
        const scriptContent = `#!/bin/bash\nexec "${binaryPath}" git "$@"\n`;

        // Write the script with executable permissions (0o755)
        fs.writeFileSync(gitWrapperPath, scriptContent, { mode: 0o755 });

        // Prepend the bin directory to the terminal PATH using environmentVariableCollection
        context.environmentVariableCollection.prepend(
            'PATH',
            binDir + path.delimiter
        );

        console.log(`curb: Safe Git Mode wrapper installed at ${gitWrapperPath}`);
    } catch (err) {
        console.error(`curb: Failed to setup Git wrapper: ${err}`);
        vscode.window.showErrorMessage(`curb Guardrail: Failed to setup Safe Git Mode: ${err}`);
    }
}
