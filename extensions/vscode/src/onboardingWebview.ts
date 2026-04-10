import * as vscode from 'vscode';
import * as path from 'path';
import * as os from 'os';
import * as fs from 'fs';
import { setMcpConfigPath, getKnownMcpLocations } from './mcpConfigStore';

export function registerOnboardingCommand(context: vscode.ExtensionContext) {
    const command = vscode.commands.registerCommand('curb.openOnboarding', () => {
        const panel = vscode.window.createWebviewPanel(
            'curbOnboarding',
            'Welcome to Curb',
            vscode.ViewColumn.One,
            { enableScripts: true, retainContextWhenHidden: true }
        );

        panel.webview.html = getOnboardingWebviewContent();

        panel.webview.onDidReceiveMessage(async (msg) => {
            try {
                if (msg.type === 'get_mcp_locations') {
                    const locations = getKnownMcpLocations();
                    panel.webview.postMessage({ type: 'mcp_locations_data', data: locations });
                } else if (msg.type === 'set_terminal_profile') {
                    const profileType = msg.profileType; // 'pty' or 'hijack'
                    if (profileType === 'pty') {
                        const config = vscode.workspace.getConfiguration();
                        const platform = process.platform === 'win32' ? 'windows' : (process.platform === 'darwin' ? 'osx' : 'linux');
                        const profilesKey = `terminal.integrated.profiles.${platform}`;
                        const defaultKey = `terminal.integrated.defaultProfile.${platform}`;

                        const binaryExt = process.platform === 'win32' ? 'curb.exe' : 'curb';
                        const curbPath = path.join(os.homedir(), '.curb', 'bin', binaryExt);

                        const inspectProfiles = config.inspect<any>(profilesKey);
                        const profiles: any = inspectProfiles?.workspaceValue || {};
                        profiles['tcurb'] = {
                            "path": curbPath,
                            "args": ["pty"]
                        };

                        await config.update(profilesKey, profiles, vscode.ConfigurationTarget.Global);
                        await config.update(defaultKey, 'tcurb', vscode.ConfigurationTarget.Global);
                        await config.update(profilesKey, profiles, vscode.ConfigurationTarget.Workspace);
                        await config.update(defaultKey, 'tcurb', vscode.ConfigurationTarget.Workspace);

                        // Automatically launch and focus the secure terminal
                        const tcurbTerm = vscode.window.createTerminal({
                            name: 'tcurb',
                            shellPath: curbPath,
                            shellArgs: ['pty']
                        });
                        tcurbTerm.show();

                        vscode.window.showInformationMessage('🛡️ Curb is now your default terminal.');
                    }
                    // if hijack, we don't do config changes here. trap command is used elsewhere.
                } else if (msg.type === 'set_mcp_config') {
                    const selectedPath = msg.path;
                    if (!fs.existsSync(selectedPath)) {
                        const dir = path.dirname(selectedPath);
                        try {
                            fs.mkdirSync(dir, { recursive: true });
                            fs.writeFileSync(selectedPath, JSON.stringify({ servers: {} }, null, 2));
                        } catch (e) {
                            console.error('Failed to create mcp config', e);
                        }
                    }
                    await setMcpConfigPath(context, selectedPath);
                } else if (msg.type === 'onboarding_complete') {
                    // write .firstrun
                    const firstRunPath = path.join(os.homedir(), '.curb', '.firstrun');
                    fs.writeFileSync(firstRunPath, new Date().toISOString(), 'utf8');

                    panel.dispose();
                    vscode.commands.executeCommand('curb.openDashboard');
                }
            } catch (e) {
                console.error('[Curb Onboarding]', e);
            }
        });
    });

    context.subscriptions.push(command);
}

function getOnboardingWebviewContent() {
    return `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Welcome to Curb</title>
    <script src="https://unpkg.com/react@18/umd/react.production.min.js" crossorigin></script>
    <script src="https://unpkg.com/react-dom@18/umd/react-dom.production.min.js" crossorigin></script>
    <script src="https://unpkg.com/babel-standalone@6/babel.min.js"></script>
    <script src="https://cdn.tailwindcss.com"></script>
    <style>
        :root {
            --bg-color: var(--vscode-editor-background, #0d1117);
            --fg-color: var(--vscode-editor-foreground, #c9d1d9);
            --surface: var(--vscode-editorWidget-background, #21262d);
            --border-color: var(--vscode-widget-border, #30363d);
            --primary: var(--vscode-button-background, #238636);
            --primary-hover: var(--vscode-button-hoverBackground, #2ea043);
        }
        body { 
            background-color: var(--bg-color); 
            color: var(--fg-color); 
            font-family: var(--vscode-font-family, system-ui, sans-serif); 
            margin: 0; 
            display: flex;
            align-items: center;
            justify-content: center;
            height: 100vh;
        }
        .vs-btn {
            background: var(--primary);
            color: white;
            border: none;
            padding: 10px 20px;
            border-radius: 6px;
            cursor: pointer;
            font-weight: 600;
            font-size: 14px;
            box-shadow: 0 2px 8px rgba(35,134,54,0.3);
            transition: all 0.2s;
        }
        .vs-btn:hover:not(:disabled) { background: var(--primary-hover); transform: translateY(-1px); }
        .vs-btn:disabled { opacity: 0.5; cursor: not-allowed; }
        
        .card-option {
            background: rgba(255,255,255,0.03);
            border: 2px solid var(--border-color);
            border-radius: 8px;
            padding: 20px;
            cursor: pointer;
            transition: all 0.2s;
        }
        .card-option:hover {
            background: rgba(255,255,255,0.06);
            border-color: rgba(255,255,255,0.2);
        }
        .card-option.selected {
            background: rgba(34,197,94,0.1);
            border-color: #2ea043;
        }
    </style>
</head>
<body>
    <div id="root" class="w-full h-full flex items-center justify-center p-8"></div>

    <script type="text/babel">
        const vscode = acquireVsCodeApi();
        const { useState, useEffect } = React;

        function App() {
            const [step, setStep] = useState(1);
            const [termChoice, setTermChoice] = useState('pty');
            const [locations, setLocations] = useState([]);
            const [selectedMcp, setSelectedMcp] = useState('');

            useEffect(() => {
                const handler = (event) => {
                    const msg = event.data;
                    if (msg.type === 'mcp_locations_data') {
                        setLocations(msg.data);
                        if (msg.data.length > 0) {
                            setSelectedMcp(msg.data[0].path);
                        }
                    }
                };
                window.addEventListener('message', handler);
                vscode.postMessage({ type: 'get_mcp_locations' });
                return () => window.removeEventListener('message', handler);
            }, []);

            const nextStep = () => setStep(step + 1);
            
            const submitTerminal = () => {
                vscode.postMessage({ type: 'set_terminal_profile', profileType: termChoice });
                nextStep();
            };

            const submitMcp = () => {
                if (selectedMcp) {
                    vscode.postMessage({ type: 'set_mcp_config', path: selectedMcp });
                }
                nextStep();
            };
            
            const finish = () => {
                vscode.postMessage({ type: 'onboarding_complete' });
            };

            if (step === 1) {
                return (
                    <div className="max-w-2xl w-full">
                        <div className="text-center mb-10">
                            <h1 className="text-4xl font-bold mb-4">Welcome to <span className="text-green-500">Curb</span></h1>
                            <p className="text-lg text-gray-400">The first semantic firewall for AI coding agents.</p>
                        </div>
                        
                        <div className="space-y-6 mb-10">
                            <div className="flex gap-4 items-start">
                                <div className="text-3xl">🛑</div>
                                <div>
                                    <h3 className="text-xl font-bold text-gray-200">Stop Dangerous Commands</h3>
                                    <p className="text-gray-400">Intercepts commands like 'git push --force' and intercepts package manager rogue installs locally in your terminal.</p>
                                </div>
                            </div>
                            <div className="flex gap-4 items-start">
                                <div className="text-3xl">🛡️</div>
                                <div>
                                    <h3 className="text-xl font-bold text-gray-200">Protect Sensitive Files</h3>
                                    <p className="text-gray-400">Enforces an OS-level read-only lock on dotfiles and secrets, stopping agents from overwriting your keys.</p>
                                </div>
                            </div>
                            <div className="flex gap-4 items-start">
                                <div className="text-3xl">🔌</div>
                                <div>
                                    <h3 className="text-xl font-bold text-gray-200">Secure MCP Servers</h3>
                                    <p className="text-gray-400">Proxies MCP tools and evaluates arguments over rules to prevent Agents from abusing filesystem tools.</p>
                                </div>
                            </div>
                        </div>

                        <div className="flex justify-end">
                            <button className="vs-btn" onClick={nextStep}>Get Started →</button>
                        </div>
                    </div>
                );
            }

            if (step === 2) {
                return (
                    <div className="max-w-2xl w-full">
                        <h1 className="text-3xl font-bold mb-2">Terminal Defense</h1>
                        <p className="text-gray-400 mb-8">How should Curb intercept the Agent's shell commands?</p>

                        <div className="grid grid-cols-1 gap-6 mb-10">
                            <div className={"card-option " + (termChoice === 'pty' ? 'selected' : '')} onClick={() => setTermChoice('pty')}>
                                <div className="flex justify-between items-center mb-2">
                                    <h3 className="text-lg font-bold">Curb Shell Profile (Recommended)</h3>
                                    {termChoice === 'pty' && <span className="bg-green-600 text-white text-xs px-2 py-1 rounded">Selected</span>}
                                </div>
                                <p className="text-sm text-gray-400">
                                    Installs a permanent pseudo-terminal wrapper interceptor as your default profile (\`terminal.integrated.defaultProfile\`). Reliable, consistent, and invisible.
                                </p>
                            </div>
                            
                            <div className={"card-option " + (termChoice === 'hijack' ? 'selected' : '')} onClick={() => setTermChoice('hijack')}>
                                <div className="flex justify-between items-center mb-2">
                                    <h3 className="text-lg font-bold">PATH Hijack Wrappers</h3>
                                    {termChoice === 'hijack' && <span className="bg-green-600 text-white text-xs px-2 py-1 rounded">Selected</span>}
                                </div>
                                <p className="text-sm text-gray-400">
                                    Just injects a folder with shadow wrappers running \`curb evaluate\` at the front of your PATH. (Must be started manually each session).
                                </p>
                            </div>
                        </div>

                        <div className="flex justify-between">
                            <button className="text-gray-500 hover:text-white" onClick={() => setStep(step - 1)}>← Back</button>
                            <button className="vs-btn" onClick={submitTerminal}>Continue</button>
                        </div>
                    </div>
                );
            }

            if (step === 3) {
                return (
                    <div className="max-w-2xl w-full">
                        <h1 className="text-3xl font-bold mb-2">MCP Guard Configuration</h1>
                        <p className="text-gray-400 mb-8">Select the MCP configuration file utilized by your IDE (like Cursor, Windsurf, or Antigravity).</p>

                        <div className="space-y-3 mb-10 h-64 overflow-y-auto pr-4">
                            {locations.length === 0 && <p className="text-gray-500">Scanning for MCP files...</p>}
                            {locations.map((loc, idx) => (
                                <div key={idx} className={"card-option p-4 " + (selectedMcp === loc.path ? 'selected' : '')} onClick={() => setSelectedMcp(loc.path)}>
                                    <div className="flex gap-3 items-center">
                                        <div className={"w-4 h-4 rounded-full border border-gray-500 flex items-center justify-center"}>
                                            {selectedMcp === loc.path && <div className="w-2.5 h-2.5 bg-green-500 rounded-full"></div>}
                                        </div>
                                        <div>
                                            <div className="font-bold flex items-center gap-2">
                                                {loc.label} 
                                                {!loc.exists && <span className="bg-yellow-900 text-yellow-300 text-[10px] px-1.5 py-0.5 rounded">Not Found (Will Form)</span>}
                                            </div>
                                            <div className="text-xs text-gray-500 font-mono mt-1">{loc.path}</div>
                                        </div>
                                    </div>
                                </div>
                            ))}
                        </div>

                        <div className="flex justify-between">
                            <button className="text-gray-500 hover:text-white" onClick={() => setStep(step - 1)}>← Back</button>
                            <button className="vs-btn" onClick={submitMcp} disabled={!selectedMcp}>Configure MCP →</button>
                        </div>
                    </div>
                );
            }

            if (step === 4) {
                return (
                    <div className="max-w-2xl w-full text-center">
                        <div className="text-6xl mb-6">🛡️</div>
                        <h1 className="text-4xl font-bold mb-4">You're Protected!</h1>
                        <p className="text-lg text-gray-400 mb-8">
                            Curb is now actively monitoring your local environment. AI Agents are restricted to your rules.
                        </p>
                        <button className="vs-btn text-lg px-8 py-3" onClick={finish}>Go to Curb Dashboard</button>
                    </div>
                );
            }

            return null;
        }

        const root = ReactDOM.createRoot(document.getElementById('root'));
        root.render(<App />);
    </script>
</body>
</html>`;
}
