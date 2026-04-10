import * as vscode from 'vscode';
import * as path from 'path';
import * as os from 'os';
import { sendRpcCommand } from './binaryManager';
import {
    getMcpConfigPath,
    getMcpStatus,
    rewriteMcpJsonEntry,
    restoreMcpJsonEntry,
    McpServerStatus,
} from './mcpConfigStore';

export function registerDashboardCommand(context: vscode.ExtensionContext) {
    const command = vscode.commands.registerCommand('curb.openDashboard', () => {
        const panel = vscode.window.createWebviewPanel(
            'curbDashboard',
            'Curb Control Center',
            vscode.ViewColumn.One,
            { enableScripts: true, retainContextWhenHidden: true }
        );

        panel.webview.html = getWebviewContent();

        panel.webview.onDidReceiveMessage(async (msg) => {
            try {
                if (['get_file_rules', 'get_protected_resources', 'get_guards', 'get_mcp_policies', 'get_audit_logs'].includes(msg.type)) {
                    const res = await sendRpcCommand(msg.type, {});
                    panel.webview.postMessage({ type: msg.type + '_data', data: res });
                } else if (msg.type === 'add_protected_resource') {
                    await sendRpcCommand(msg.type, { pattern: msg.pattern });
                    const res = await sendRpcCommand('get_protected_resources', {});
                    panel.webview.postMessage({ type: 'get_protected_resources_data', data: res });
                } else if (msg.type === 'remove_protected_resource') {
                    await sendRpcCommand(msg.type, { id: msg.id });
                    const res = await sendRpcCommand('get_protected_resources', {});
                    panel.webview.postMessage({ type: 'get_protected_resources_data', data: res });
                } else if (msg.type === 'add_cli_guard') {
                    await sendRpcCommand(msg.type, { name: msg.name, trigger_targets: msg.target, condition: msg.condition, action: msg.action, error_msg: msg.error_msg });
                    const res = await sendRpcCommand('get_guards', {});
                    panel.webview.postMessage({ type: 'get_guards_data', data: res });
                } else if (msg.type === 'remove_guard') {
                    await sendRpcCommand(msg.type, { id: msg.id });
                    const res = await sendRpcCommand('get_guards', {});
                    panel.webview.postMessage({ type: 'get_guards_data', data: res });
                } else if (msg.type === 'add_mcp_policy') {
                    await sendRpcCommand(msg.type, { server_name: msg.server, tool_name: msg.tool, condition: msg.condition, action: msg.action, error_msg: msg.error_msg });
                    const res = await sendRpcCommand('get_mcp_policies', {});
                    panel.webview.postMessage({ type: 'get_mcp_policies_data', data: res });
                } else if (msg.type === 'remove_mcp_policy') {
                    await sendRpcCommand(msg.type, { id: msg.id });
                    const res = await sendRpcCommand('get_mcp_policies', {});
                    panel.webview.postMessage({ type: 'get_mcp_policies_data', data: res });

                    // ═══════════════════════════════════════════
                    //  NEW: MCP Status & Proxy Management
                    // ═══════════════════════════════════════════

                } else if (msg.type === 'get_mcp_status') {
                    const mcpPath = getMcpConfigPath(context);
                    if (!mcpPath) {
                        panel.webview.postMessage({
                            type: 'get_mcp_status_data',
                            data: { servers: [], unproxiedCount: 0, configPath: null }
                        });
                        return;
                    }
                    const status = getMcpStatus(mcpPath);
                    panel.webview.postMessage({
                        type: 'get_mcp_status_data',
                        data: { ...status, configPath: mcpPath.replace(os.homedir(), '~') }
                    });

                } else if (msg.type === 'proxy_mcp_server') {
                    const mcpPath = getMcpConfigPath(context);
                    if (!mcpPath) { return; }

                    const serverName = msg.name as string;

                    // 1. Rewrite mcp.json (saves original upstream)
                    const result = rewriteMcpJsonEntry(mcpPath, serverName, 'curb');
                    if (result) {
                        await sendRpcCommand('sync_mcp_server', {
                            name: serverName,
                            upstream_cmd: result.originalUpstream,
                            env_vars: JSON.stringify(result.originalEnv),
                            headers_json: JSON.stringify(result.originalHeaders),
                        });

                        vscode.window.showInformationMessage(
                            `Curb: ${serverName} is now proxied through Curb's firewall.`
                        );
                    }

                    // 3. Refresh status
                    const status = getMcpStatus(mcpPath);
                    panel.webview.postMessage({
                        type: 'get_mcp_status_data',
                        data: { ...status, configPath: mcpPath.replace(os.homedir(), '~') }
                    });

                } else if (msg.type === 'change_mcp_config') {
                    vscode.commands.executeCommand('curb.selectMcpConfig');

                } else if (msg.type === 'fetch_mcp_tools') {
                    const res = await sendRpcCommand(msg.type, { server_name: msg.server_name });
                    panel.webview.postMessage({ type: 'fetch_mcp_tools_data', server: msg.server_name, data: res });

                }
            } catch (e) {
                console.error('[Curb Dashboard] RPC error:', e);
                vscode.window.showErrorMessage(`Curb UI Error: ${e}`);
            }
        });
    });

    context.subscriptions.push(command);
}

function getWebviewContent() {
    return `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Curb Control Center</title>
    <script src="https://unpkg.com/react@18/umd/react.production.min.js" crossorigin></script>
    <script src="https://unpkg.com/react-dom@18/umd/react-dom.production.min.js" crossorigin></script>
    <script src="https://unpkg.com/babel-standalone@6/babel.min.js"></script>
    <script src="https://cdn.tailwindcss.com"></script>
    <style>
        :root {
            --bg-color: var(--vscode-editor-background, #0d1117);
            --fg-color: var(--vscode-editor-foreground, #c9d1d9);
            --sidebar-bg: var(--vscode-sideBar-background, #161b22);
            --border-color: var(--vscode-widget-border, #30363d);
            --surface: var(--vscode-editorWidget-background, #21262d);
            --primary: var(--vscode-button-background, #238636);
            --primary-hover: var(--vscode-button-hoverBackground, #2ea043);
            --danger: var(--vscode-errorForeground, #f85149);
            --danger-bg: rgba(248,81,73,0.1);
        }
        body { 
            background-color: var(--bg-color); 
            color: var(--fg-color); 
            font-family: var(--vscode-font-family, system-ui, sans-serif); 
            margin: 0; 
            padding: 0;
        }
        .vs-input {
            background: var(--vscode-input-background, #0d1117);
            border: 1px solid var(--vscode-input-border, #30363d);
            color: var(--vscode-input-foreground, #c9d1d9);
            padding: 4px 8px;
            border-radius: 4px;
            outline: none;
        }
        .vs-input:focus { border-color: var(--primary); }
        .vs-btn {
            background: var(--primary);
            color: white;
            border: none;
            padding: 6px 12px;
            border-radius: 4px;
            cursor: pointer;
            font-weight: 500;
        }
        .vs-btn:disabled { opacity: 0.5; cursor: not-allowed; }
        .vs-btn:hover:not(:disabled) { background: var(--primary-hover); }
        
        .layout-app { display: flex; height: 100vh; overflow: hidden; }
        .sidebar { width: 260px; background: var(--sidebar-bg); border-right: 1px solid var(--border-color); display: flex; flex-direction: column; }
        .main-content { flex: 1; overflow-y: auto; background: var(--bg-color); }
        
        .nav-item { padding: 10px 16px; cursor: pointer; color: var(--vscode-descriptionForeground, #8b949e); border-left: 3px solid transparent; font-size: 14px; }
        .nav-item:hover { color: var(--fg-color); background: rgba(255,255,255,0.05); }
        .nav-item.active { color: var(--fg-color); border-left-color: var(--primary); background: rgba(255,255,255,0.05); font-weight: 500; }
        
        .card { background: var(--surface); border: 1px solid var(--border-color); border-radius: 6px; padding: 16px; margin-bottom: 16px; }
        .rule-card { border: 1px solid var(--border-color); border-radius: 6px; margin-bottom: 24px; overflow: hidden; }
        .rule-card-header { background: rgba(255,255,255,0.02); padding: 12px 16px; border-bottom: 1px solid var(--border-color); display: flex; justify-content: space-between; align-items: center; }
        
        .policy-builder { padding: 16px; border-top: 1px solid rgba(255,255,255,0.05); }
        .builder-row { display: flex; gap: 8px; align-items: center; margin-bottom: 12px; }
        .builder-select { background: #1a1e23; border: 1px solid #30363d; color: #c9d1d9; border-radius: 4px; padding: 6px 10px; font-size: 13px; outline: none; }
        .builder-chip { background: rgba(255,255,255,0.1); font-family: monospace; font-size: 12px; padding: 2px 6px; border-radius: 4px; margin-right: 4px; }

        .text-btn { background: none; border: 1px solid var(--border-color); color: var(--fg-color); padding: 4px 10px; border-radius: 4px; cursor: pointer; font-size: 12px; font-weight: 500; }
        .text-btn:hover { background: rgba(255,255,255,0.08); border-color: var(--fg-color); }
        .text-btn:disabled { opacity: 0.4; cursor: not-allowed; }

        .warning-banner {
            background: linear-gradient(135deg, rgba(234,179,8,0.12), rgba(234,179,8,0.06));
            border: 1px solid rgba(234,179,8,0.3);
            border-radius: 8px;
            padding: 14px 18px;
            margin-bottom: 20px;
            display: flex;
            align-items: center;
            justify-content: space-between;
            gap: 12px;
        }

        .badge-proxied {
            background: rgba(34,197,94,0.15);
            color: #4ade80;
            border: 1px solid rgba(34,197,94,0.3);
            font-size: 11px;
            font-weight: 700;
            padding: 3px 10px;
            border-radius: 20px;
            display: inline-flex;
            align-items: center;
            gap: 5px;
        }

        .badge-unproxied {
            background: rgba(161,161,170,0.1);
            color: #a1a1aa;
            border: 1px solid rgba(161,161,170,0.3);
            font-size: 11px;
            font-weight: 700;
            padding: 3px 10px;
            border-radius: 20px;
        }

        .protect-btn {
            background: linear-gradient(135deg, #238636, #2ea043);
            color: white;
            border: none;
            padding: 8px 20px;
            border-radius: 6px;
            cursor: pointer;
            font-weight: 600;
            font-size: 13px;
            transition: all 0.2s;
            box-shadow: 0 2px 8px rgba(35,134,54,0.3);
        }
        .protect-btn:hover { transform: translateY(-1px); box-shadow: 0 4px 12px rgba(35,134,54,0.4); }

        .server-card {
            border: 1px solid var(--border-color);
            border-radius: 8px;
            overflow: hidden;
            margin-bottom: 12px;
            transition: border-color 0.2s;
        }
        .server-card:hover { border-color: rgba(255,255,255,0.15); }
        .server-card-header {
            padding: 14px 18px;
            display: flex;
            justify-content: space-between;
            align-items: center;
            background: rgba(255,255,255,0.02);
        }
    </style>
</head>
<body>
    <div id="root"></div>

    <script type="text/babel">
        const vscode = acquireVsCodeApi();
        const { useState, useEffect } = React;

        function App() {
            const [view, setView] = useState('dashboard');
            
            const [resources, setResources] = useState([]);
            const [guards, setGuards] = useState([]);
            const [mcpStatus, setMcpStatus] = useState({ servers: [], unproxiedCount: 0, configPath: null });
            const [mcpPolicies, setMcpPolicies] = useState([]);
            const [toolSchemas, setToolSchemas] = useState({});
            const [auditLogs, setAuditLogs] = useState([]);

            useEffect(() => {
                const handler = (event) => {
                    const msg = event.data;
                    const getArr = (d) => Array.isArray(d) ? d : (d && Array.isArray(d.result) ? d.result : []);
                    if (msg.type === 'get_protected_resources_data') setResources(getArr(msg.data));
                    if (msg.type === 'get_guards_data') setGuards(getArr(msg.data));
                    if (msg.type === 'get_mcp_status_data') setMcpStatus(msg.data || { servers: [], unproxiedCount: 0, configPath: null });
                    if (msg.type === 'get_mcp_policies_data') setMcpPolicies(getArr(msg.data));
                    if (msg.type === 'get_audit_logs_data') setAuditLogs(getArr(msg.data));
                    if (msg.type === 'fetch_mcp_tools_data') {
                        setToolSchemas(prev => ({ ...prev, [msg.server]: msg.data }));
                    }
                };
                window.addEventListener('message', handler);
                
                const loadData = () => {
                    vscode.postMessage({ type: 'get_protected_resources' });
                    vscode.postMessage({ type: 'get_guards' });
                    vscode.postMessage({ type: 'get_mcp_status' });
                    vscode.postMessage({ type: 'get_mcp_policies' });
                    vscode.postMessage({ type: 'get_audit_logs' });
                };
                
                loadData();
                const interval = setInterval(loadData, 3000);
                
                return () => {
                    window.removeEventListener('message', handler);
                    clearInterval(interval);
                };
            }, []);

            return (
                <div className="layout-app">
                    <div className="sidebar">
                        <div className="p-4 flex items-center gap-2 border-b border-[#30363d] mb-4">
                            <span className="font-bold tracking-wide">Curb</span>
                        </div>
                        
                        <div className="text-xs font-semibold uppercase text-gray-500 tracking-wider px-4 mb-2">Observability</div>
                        <div className={"nav-item " + (view === 'dashboard' ? 'active' : '')} onClick={() => setView('dashboard')}>
                            Live Dashboard
                        </div>
                        
                        <div className="text-xs font-semibold uppercase text-gray-500 tracking-wider px-4 mt-6 mb-2">Management</div>
                        <div className={"nav-item " + (view === 'rules' ? 'active' : '')} onClick={() => setView('rules')}>
                            Safety Rules
                        </div>
                        <div className={"nav-item " + (view === 'mcp' ? 'active' : '')} onClick={() => setView('mcp')}>
                            MCP Guard
                            {mcpStatus.unproxiedCount > 0 && (
                                <span className="ml-2 bg-yellow-500 text-black text-[10px] font-bold px-1.5 py-0.5 rounded-full">{mcpStatus.unproxiedCount}</span>
                            )}
                        </div>
                    </div>
                    
                    <div className="main-content p-8">
                        {view === 'dashboard' && <DashboardView resources={resources} guards={guards} policies={mcpPolicies} logs={auditLogs} mcpStatus={mcpStatus} setView={setView} />}
                        {view === 'rules' && <RulesView resources={resources} guards={guards} />}
                        {view === 'mcp' && <McpGuardView mcpStatus={mcpStatus} policies={mcpPolicies} toolSchemas={toolSchemas} />}
                    </div>
                </div>
            );
        }

        // --- DASHBOARD VIEW ---
        function DashboardView({ resources, guards, policies, logs, mcpStatus, setView }) {
            const [page, setPage] = useState(0);
            const [bannerDismissed, setBannerDismissed] = useState(false);
            const rowsPerPage = 5;
            const maxPage = Math.max(0, Math.ceil(logs.length / rowsPerPage) - 1);
            const visibleLogs = logs.slice(page * rowsPerPage, (page + 1) * rowsPerPage);

            return (
                <div className="max-w-4xl">

                    {/* ── Warning Banner for Unproxied MCP servers ── */}
                    {!bannerDismissed && mcpStatus.unproxiedCount > 0 && (
                        <div className="warning-banner">
                            <div className="flex items-center gap-3">
                                <span className="text-yellow-400 text-xl">⚠️</span>
                                <div>
                                    <div className="text-yellow-300 font-semibold text-sm">
                                        {mcpStatus.unproxiedCount} MCP server{mcpStatus.unproxiedCount > 1 ? 's' : ''} detected but not proxied
                                    </div>
                                    <div className="text-yellow-500/80 text-xs mt-0.5">
                                        For comprehensive protection, go to MCP Guard to create rules.
                                    </div>
                                </div>
                            </div>
                            <div className="flex gap-2">
                                <button className="vs-btn text-xs" onClick={() => setView('mcp')}>Go to MCP Guard</button>
                                <button className="text-btn text-xs text-gray-400" onClick={() => setBannerDismissed(true)}>Dismiss</button>
                            </div>
                        </div>
                    )}

                    <h2 className="text-lg font-semibold mb-4 text-gray-200">Security Pulse</h2>
                    <div className="grid grid-cols-4 gap-4 mb-8">
                        <div className="card text-center bg-[#1c2128] border-blue-900/30">
                            <div className="text-3xl font-bold text-blue-400 mb-1">{logs.length}</div>
                            <div className="text-[10px] text-gray-400 uppercase tracking-widest">Process Scans</div>
                        </div>
                        <div className="card text-center bg-[#1c2128]">
                            <div className="text-3xl font-bold text-orange-400 mb-1">{resources.length}</div>
                            <div className="text-[10px] text-gray-400 uppercase tracking-widest">File Protections</div>
                        </div>
                        <div className="card text-center bg-[#1c2128]">
                            <div className="text-3xl font-bold text-green-400 mb-1">{guards.length + policies.length}</div>
                            <div className="text-[10px] text-gray-400 uppercase tracking-widest">CLI Rules</div>
                        </div>
                        <div className="card text-center bg-[#1c2128] border-red-900/30">
                            <div className="text-3xl font-bold text-red-500 mb-1">{logs.filter(l => l.action_taken === 'block').length}</div>
                            <div className="text-[10px] text-gray-400 uppercase tracking-widest">Blocks Issued</div>
                        </div>
                    </div>


                    <h1 className="text-2xl font-semibold mb-6">Live Activity Stream</h1>
                    
                    <div className="card overflow-hidden !p-0 mb-8">
                        {(!logs || logs.length === 0) ? (
                            <div className="p-8 text-center text-gray-500">No recent security events.</div>
                        ) : (
                            <div>
                                <table className="w-full text-sm text-left">
                                    <thead className="text-xs text-gray-400 bg-[rgba(255,255,255,0.02)] border-b border-[#30363d]">
                                        <tr>
                                            <th className="px-4 py-3">Time</th>
                                            <th className="px-4 py-3">Source</th>
                                            <th className="px-4 py-3">Action</th>
                                            <th className="px-4 py-3">Payload Details</th>
                                        </tr>
                                    </thead>
                                    <tbody>
                                        {visibleLogs.map(log => (
                                            <tr key={log.id} className={"border-b border-[#30363d] hover:bg-[rgba(255,255,255,0.02)] " + (log.action_taken === 'allow' ? 'opacity-30 grayscale-[0.5]' : '')}>
                                                <td className="px-4 py-3 text-gray-400 whitespace-nowrap">{new Date(log.timestamp).toLocaleTimeString()}</td>
                                                <td className="px-4 py-3">
                                                    <span className={"px-2 py-1 rounded text-xs font-bold " + (log.source === 'mcp' ? 'bg-purple-900/50 text-purple-300' : 'bg-blue-900/50 text-blue-300')}>
                                                        {log.source.toUpperCase()}
                                                    </span>
                                                </td>
                                                <td className="px-4 py-3">
                                                    <span className={"px-2 py-1 rounded text-xs font-bold " + (log.action_taken === 'block' ? 'bg-red-600 text-white shadow-[0_0_10px_rgba(220,38,38,0.5)]' : 'bg-green-900/30 text-green-400')}>
                                                        {log.action_taken.toUpperCase()}
                                                    </span>
                                                </td>
                                                <td className="px-4 py-3 font-mono text-xs text-gray-400 truncate max-w-xs" title={log.payload}>
                                                    {log.payload}
                                                </td>
                                            </tr>
                                        ))}
                                    </tbody>
                                </table>
                                <div className="px-4 py-2 border-t border-[#30363d] flex justify-between items-center text-xs text-gray-400 bg-[rgba(0,0,0,0.2)]">
                                    <span>Showing {page * rowsPerPage + 1} - {Math.min((page + 1) * rowsPerPage, logs.length)} of {logs.length} events</span>
                                    <div className="flex gap-2">
                                        <button className="px-3 py-1 bg-[#21262d] border border-[#30363d] rounded hover:bg-[#30363d] disabled:opacity-30 disabled:cursor-not-allowed" 
                                            disabled={page === 0} 
                                            onClick={() => setPage(page-1)}>Prev</button>
                                        <button className="px-3 py-1 bg-[#21262d] border border-[#30363d] rounded hover:bg-[#30363d] disabled:opacity-30 disabled:cursor-not-allowed" 
                                            disabled={page >= maxPage} 
                                            onClick={() => setPage(page+1)}>Next</button>
                                    </div>
                                </div>
                            </div>
                        )}
                    </div>
                    
                    <div className="card mt-12 bg-[#0d1117] border-[#22272e]">
                        <h2 className="text-xs font-semibold mb-2 text-gray-400 uppercase tracking-wider">System Information</h2>
                        <ul className="list-disc pl-5 text-gray-500 space-y-1 text-xs">
                            <li>To add CLI or File protections, go to <strong>Policy Rules</strong>.</li>
                            <li>To discover tools and enforce zero-trust for Agents, go to <strong>MCP Guard</strong>.</li>
                            <li>Agents attempting to breach these rules will be blocked locally at the endpoint layer.</li>
                        </ul>
                    </div>
                </div>
            );
        }

        // --- RULES VIEW ---
        function RulesView({ resources, guards }) {
            const [rType, setRType] = useState('cmd');
            
            // CLI Guard form
            const [name, setName] = useState('');
            const [target, setTarget] = useState('');
            const [chainRows, setChainRows] = useState([{ logOp: '', op: '.contains', val: '' }]);
            
            // File form
            const [pattern, setPattern] = useState('');

            const updateChainRow = (index, field, val) => {
                const newRows = [...chainRows];
                newRows[index][field] = val;
                setChainRows(newRows);
            };

            const addChainRow = () => {
                setChainRows([...chainRows, { logOp: '&&', op: '.contains', val: '' }]);
            };

            const removeChainRow = (index) => {
                const newRows = [...chainRows];
                if (index === 0 && newRows.length > 1) {
                    newRows[1].logOp = '';
                }
                newRows.splice(index, 1);
                setChainRows(newRows);
            };

            const addGuard = () => {
                if (!name || !target || chainRows.length === 0) return;
                
                let condStr = '';
                let isValid = true;
                
                for (let i=0; i<chainRows.length; i++) {
                    const row = chainRows[i];
                    if (!row.val.trim()) { isValid = false; break; }
                    
                    if (row.logOp) condStr += \` \${row.logOp} \`;
                    
                    let rowRule = '';
                    if (row.op === '.contains') {
                        rowRule = \`args.contains("\${row.val}")\`;
                    } else if (row.op === '!.contains') {
                        rowRule = \`!args.contains("\${row.val}")\`;
                    }
                    
                    condStr += \`(\${rowRule})\`;
                }
                
                if (!isValid) return;

                vscode.postMessage({ type: 'add_cli_guard', name, target, condition: condStr, action: 'block', error_msg: \`Blocked \${name}\` });
                setName(''); setTarget(''); setChainRows([{ logOp: '', op: '.contains', val: '' }]);
            };

            const addFile = () => {
                vscode.postMessage({ type: 'add_protected_resource', pattern });
                setPattern('');
            };

            return (
                <div className="max-w-4xl">
                    <h1 className="text-2xl font-semibold mb-6">Policy Configuration</h1>
                    
                    <div className="card mb-8">
                        <h2 className="text-md font-semibold mb-4 text-gray-200">Create New Rule</h2>
                        <div className="flex gap-4 mb-4 border-b border-[#30363d] pb-2">
                            <button className={"pb-2 font-medium " + (rType==='cmd' ? "text-white border-b-2 border-green-500" : "text-gray-500")} onClick={()=>setRType('cmd')}>CLI Watcher</button>
                            <button className={"pb-2 font-medium " + (rType==='file' ? "text-white border-b-2 border-green-500" : "text-gray-500")} onClick={()=>setRType('file')}>File Watcher</button>
                        </div>
                        
                        {rType === 'cmd' ? (
                            <div className="card bg-[#161b22] border-[#30363d] !p-0 overflow-hidden mt-4">
                                <div className="p-4 border-b border-[#30363d] bg-[rgba(255,255,255,0.02)]">
                                    <div className="flex gap-4">
                                        <input className="vs-input flex-1" placeholder="Rule Name (e.g. Git Guard)" value={name} onChange={e=>setName(e.target.value)} />
                                        <input className="vs-input flex-1" placeholder="Target Command (e.g. git)" value={target} onChange={e=>setTarget(e.target.value)} />
                                    </div>
                                </div>
                                <div className="p-4 bg-[rgba(0,0,0,0.2)]">
                                    <div className="text-xs font-semibold text-purple-400 mb-4 tracking-widest uppercase flex justify-between items-center">
                                        <span>Visual Argument Builder</span>
                                    </div>
                                    
                                    {chainRows.map((row, i) => (
                                        <div key={i} className="flex gap-2 items-center mb-2">
                                            {i === 0 ? (
                                                <div className="w-16 text-center font-bold text-xs text-gray-500 font-mono tracking-widest">IF</div>
                                            ) : (
                                                <select className="builder-select w-16 text-center text-xs font-bold font-mono text-yellow-500" value={row.logOp} onChange={e => updateChainRow(i, 'logOp', e.target.value)}>
                                                    <option value="&&">AND</option>
                                                    <option value="||">OR</option>
                                                </select>
                                            )}
                                            
                                            <div className="builder-select text-gray-400 font-mono bg-transparent border-none w-16 text-right">args</div>
                                            <select className="builder-select w-36 font-mono text-center" value={row.op} onChange={e => updateChainRow(i, 'op', e.target.value)}>
                                                <option value=".contains">.contains</option>
                                                <option value="!.contains">NOT .contains</option>
                                            </select>
                                            <input className="vs-input flex-1 font-mono text-sm" placeholder="e.g. push" value={row.val} onChange={e => updateChainRow(i, 'val', e.target.value)} />
                                            
                                            <button className="text-gray-500 hover:text-white px-2" onClick={() => removeChainRow(i)} disabled={chainRows.length === 1}>✕</button>
                                        </div>
                                    ))}
                                    
                                    <div className="mt-4 flex justify-between">
                                        <button className="text-xs text-blue-400 hover:text-blue-300 font-medium" onClick={addChainRow}>+ Add Condition row</button>
                                        <button className="vs-btn px-6" onClick={addGuard} disabled={!name || !target}>Save Rule</button>
                                    </div>
                                </div>
                            </div>
                        ) : (
                            <div className="flex gap-2 mt-4">
                                <input className="vs-input flex-1" placeholder="Glob Pattern (e.g. .env* or secrets/)" value={pattern} onChange={e=>setPattern(e.target.value)} />
                                <button className="vs-btn whitespace-nowrap" onClick={addFile} disabled={!pattern}>Protect Resource</button>
                            </div>
                        )}
                    </div>

                    <div className="grid grid-cols-2 gap-6">
                        <div>
                            <h3 className="text-sm font-semibold uppercase text-gray-500 mb-3">CLI Watchers</h3>
                            {guards.length === 0 && <div className="text-gray-500 text-sm">No rules configured.</div>}
                            {guards.map(g => (
                                <div key={g.id} className="card p-3 mb-2 flex justify-between items-center bg-[#1c2128]">
                                    <div>
                                        <div className="font-semibold text-sm">{g.name} <span className="text-xs text-gray-500 ml-2">[{g.trigger_targets}]</span></div>
                                        <div className="text-xs text-blue-400 font-mono mt-1">{g.condition}</div>
                                    </div>
                                    <button className="text-red-400 hover:text-red-300" onClick={() => vscode.postMessage({type: 'remove_guard', id: g.id})}>✕</button>
                                </div>
                            ))}
                        </div>
                        <div>
                            <h3 className="text-sm font-semibold uppercase text-gray-500 mb-3">Protected Resources</h3>
                            {resources.length === 0 && <div className="text-gray-500 text-sm">No resources protected.</div>}
                            {resources.map(r => (
                                <div key={r.id} className="card p-3 mb-2 flex justify-between items-center bg-[#1c2128]">
                                    <div className="font-mono text-sm text-yellow-300">{r.pattern}</div>
                                    <button className="text-red-400 hover:text-red-300" onClick={() => vscode.postMessage({type: 'remove_protected_resource', id: r.id})}>✕</button>
                                </div>
                            ))}
                        </div>
                    </div>
                </div>
            );
        }

        // --- MCP Guard VIEW ---
        function McpGuardView({ mcpStatus, policies, toolSchemas }) {
            const [expanded, setExpanded] = useState({});
            const servers = mcpStatus.servers || [];
            const proxiedCount = servers.filter(s => s.isProxied).length;

            const toggleExpand = (name) => {
                const isExp = expanded[name];
                setExpanded(prev => ({ ...prev, [name]: !isExp }));
                if (!isExp && !toolSchemas[name]) {
                    vscode.postMessage({ type: 'fetch_mcp_tools', server_name: name });
                }
            };

            const handleProtect = (name) => {
                vscode.postMessage({ type: 'proxy_mcp_server', name });
            };

            return (
                <div className="max-w-5xl">
                    {/* ── Header ── */}
                    <div className="flex justify-between items-start mb-6">
                        <div>
                            <h1 className="text-2xl font-semibold mb-1">MCP Guard</h1>
                            {mcpStatus.configPath ? (
                                <div className="text-xs text-gray-500 flex items-center gap-2 mt-1">
                                    <span className="font-mono bg-[rgba(255,255,255,0.05)] px-2 py-0.5 rounded">{mcpStatus.configPath}</span>
                                    <button className="text-blue-400 hover:text-blue-300 underline" onClick={() => vscode.postMessage({ type: 'change_mcp_config' })}>Change</button>
                                </div>
                            ) : (
                                <div className="text-xs text-yellow-500 mt-1">
                                    No MCP config selected. <button className="text-blue-400 hover:text-blue-300 underline ml-1" onClick={() => vscode.postMessage({ type: 'change_mcp_config' })}>Select now</button>
                                </div>
                            )}
                        </div>
                        {servers.length > 0 && (
                            <div className="text-sm text-gray-400">
                                <span className="text-green-400 font-bold">{proxiedCount}</span> / {servers.length} servers proxied
                            </div>
                        )}
                    </div>

                    {/* ── Empty State ── */}
                    {servers.length === 0 && (
                        <div className="p-12 text-center border border-[#30363d] rounded-lg bg-[rgba(255,255,255,0.02)]">
                            <div className="text-4xl mb-4">🛡️</div>
                            <div className="text-gray-400 mb-2 font-bold">No MCP Servers Found</div>
                            <p className="text-sm text-gray-500 max-w-lg mx-auto mb-6">
                                {mcpStatus.configPath
                                    ? "No servers found in your selected MCP config file. Add MCP servers to your config and they'll appear here."
                                    : "Select your MCP config file to get started. Curb will discover your servers and let you protect them."
                                }
                            </p>
                            <button className="vs-btn" onClick={() => vscode.postMessage({ type: 'change_mcp_config' })}>
                                {mcpStatus.configPath ? 'Change Config' : 'Select MCP Config'}
                            </button>
                        </div>
                    )}

                    {/* ── Server List ── */}
                    <div className="space-y-3">
                        {servers.map(srv => {
                            const isProtected = srv.isProxied;
                            const isExp = expanded[srv.name];
                            const tools = toolSchemas[srv.name];
                            const serverPolicies = policies.filter(p => p.server_name === srv.name);

                            return (
                                <div key={srv.name} className="server-card" style={isProtected ? { borderColor: 'rgba(34,197,94,0.2)' } : {}}>
                                    <div className="server-card-header">
                                        <div className="flex items-center gap-3">
                                            <div className={"w-2.5 h-2.5 rounded-full " + (isProtected ? "bg-green-500 shadow-[0_0_6px_rgba(34,197,94,0.5)]" : "bg-gray-600")}></div>
                                            <div>
                                                <div className="font-bold text-base">{srv.name}</div>
                                                <div className="text-xs text-gray-500 font-mono mt-0.5 truncate max-w-md">{srv.upstream}</div>
                                            </div>
                                        </div>
                                        <div className="flex items-center gap-3">
                                            {isProtected ? (
                                                <React.Fragment>
                                                    <span className="badge-proxied">
                                                        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round">
                                                            <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/>
                                                        </svg>
                                                        PROXIED
                                                    </span>
                                                    {isExp ? (
                                                        <button className="text-btn text-xs" onClick={() => toggleExpand(srv.name)}>Collapse</button>
                                                    ) : (
                                                        <button className="text-btn text-xs" onClick={() => toggleExpand(srv.name)}>Manage Policies ▸</button>
                                                    )}
                                                </React.Fragment>
                                            ) : (
                                                <React.Fragment>
                                                    <span className="badge-unproxied">NOT PROXIED</span>
                                                    <button className="protect-btn" onClick={() => handleProtect(srv.name)}>
                                                        🛡️ Protect Now
                                                    </button>
                                                </React.Fragment>
                                            )}
                                        </div>
                                    </div>

                                    {/* ── Expanded: Tool Policies ── */}
                                    {isExp && isProtected && (
                                        <div className="p-4 bg-[var(--bg-color)] border-t border-[#30363d]">
                                            {!tools ? (
                                                <div className="animate-pulse flex gap-2 items-center text-blue-400 text-sm py-4">
                                                    <div className="w-4 h-4 border-2 border-blue-400 border-t-transparent rounded-full animate-spin"></div>
                                                    Live Discovery: Fetching tool schema from server...
                                                </div>
                                            ) : tools.error ? (
                                                <div className="text-red-400 p-3 bg-red-900/20 border border-red-900/50 rounded text-sm">
                                                    Discovery Failed: {tools.error.message || JSON.stringify(tools)}
                                                </div>
                                            ) : tools.length === 0 ? (
                                                <div className="text-gray-500 text-sm py-4">No tools advertised by this server.</div>
                                            ) : (
                                                <div className="space-y-6">
                                                    {tools.map(tool => (
                                                        <ToolPolicyBuilder 
                                                            key={tool.name} 
                                                            serverName={srv.name} 
                                                            tool={tool} 
                                                            policies={serverPolicies.filter(p => p.tool_name === tool.name)} 
                                                        />
                                                    ))}
                                                </div>
                                            )}
                                        </div>
                                    )}
                                </div>
                            );
                        })}
                    </div>
                </div>
            );
        }

        function ToolPolicyBuilder({ serverName, tool, policies }) {
            const [propName, setPropName] = useState('');
            const [op, setOp] = useState('==');
            const [val, setVal] = useState('');
            const [action, setAction] = useState('block');

            // Extract input properties keys from JSON schema
            let argProps = {};
            if (tool.inputSchema && tool.inputSchema.properties) {
                argProps = tool.inputSchema.properties;
            }
            const argKeys = Object.keys(argProps);

            // Determine active prop type
            let propType = 'string';
            if (propName && argProps[propName]) {
                propType = argProps[propName].type || 'string';
            }

            useEffect(() => {
                if (propType === 'boolean') {
                    setOp('=='); setVal('true');
                } else if (propType === 'number' || propType === 'integer') {
                    setOp('=='); setVal('0');
                } else {
                    setOp('.contains'); setVal('');
                }
            }, [propName, propType]);

            const addPolicy = () => {
                let formattedVal = val;
                if (propType === 'string') {
                    formattedVal = \`"\${val}"\`;
                }
                
                let condition = \`mcp_args.\${propName} \${op} \${formattedVal}\`;
                if (op === '.contains') {
                    condition = \`mcp_args.\${propName}.contains(\${formattedVal})\`;
                }
                
                let errorMsg = 'Blocked by Active Policy';
                if (action === 'hitl') errorMsg = 'Needs Human Approval';
                vscode.postMessage({ type: 'add_mcp_policy', server: serverName, tool: tool.name, condition, action, error_msg: errorMsg });
                setPropName(''); 
                if (propType === 'string') setVal('');
            };

            return (
                <div className="border border-[#444c56] rounded-lg overflow-hidden bg-[#161b22]">
                    <div className="px-4 py-3 border-b border-[#444c56] flex justify-between items-center bg-[#21262d]">
                        <div>
                            <span className="font-mono text-blue-300 font-bold">{tool.name}</span>
                            <span className="ml-3 text-xs text-gray-400">{tool.description ? tool.description.substring(0, 90) : ''}</span>
                        </div>
                        <div className="flex gap-1">
                            {argKeys.map(a => <span key={a} className="builder-chip">{a}</span>)}
                        </div>
                    </div>
                    
                    <div className="p-4">
                        {policies.length > 0 && (
                            <div className="mb-6 border-b border-[#30363d] pb-4">
                                <div className="text-xs uppercase text-gray-500 font-bold tracking-wider mb-2">Active Enforcement</div>
                                {policies.map(p => (
                                    <div key={p.id} className="flex justify-between items-center bg-red-900/10 border border-red-900/30 text-red-400 px-3 py-2 rounded mb-2 font-mono text-sm">
                                        <span>IF {p.condition} &rarr; {p.action.toUpperCase()}</span>
                                        <button className="text-gray-500 hover:text-red-300" onClick={() => vscode.postMessage({type: 'remove_mcp_policy', id: p.id})}>✕</button>
                                    </div>
                                ))}
                            </div>
                        )}

                        <div className="policy-builder bg-[rgba(0,0,0,0.1)] rounded !border-none">
                            <div className="flex justify-between items-center mb-3">
                                <div className="text-xs font-semibold text-purple-400 flex items-center gap-2 tracking-widest uppercase">
                                    Visual Policy Builder
                                </div>
                                <div className="flex items-center gap-3">
                                    <span className="text-xs font-medium text-gray-400 uppercase tracking-wide">Action:</span>
                                    <select className="builder-select bg-[#0d1117] text-xs font-bold" value={action} onChange={e=>setAction(e.target.value)}>
                                        <option value="block" className="text-red-400">Block Execution</option>
                                        <option value="hitl" className="text-yellow-400">Notify Human (HITL)</option>
                                    </select>
                                </div>
                            </div>
                            
                            <div className="builder-row mt-4">
                                <span className="font-mono text-sm text-gray-400 font-bold w-12 text-center">IF</span>
                                <select className="builder-select flex-1" value={propName} onChange={e=>setPropName(e.target.value)}>
                                    <option value="">Select argument...</option>
                                    {argKeys.map(a => <option key={a} value={a}>{a} ({argProps[a].type||'string'})</option>)}
                                </select>
                                
                                <select className="builder-select w-32 text-center font-mono" value={op} onChange={e=>setOp(e.target.value)} disabled={!propName}>
                                    {(propType === 'string') && <option value="==">&eq;&eq;</option>}
                                    {(propType === 'string') && <option value="!=">!=</option>}
                                    {(propType === 'string') && <option value=".contains">.contains</option>}
                                    
                                    {(propType === 'number' || propType === 'integer') && <option value="==">&eq;&eq;</option>}
                                    {(propType === 'number' || propType === 'integer') && <option value="!=">!=</option>}
                                    {(propType === 'number' || propType === 'integer') && <option value=">">&gt;</option>}
                                    {(propType === 'number' || propType === 'integer') && <option value="<">&lt;</option>}
                                    {(propType === 'number' || propType === 'integer') && <option value=">=">&gt;=</option>}
                                    {(propType === 'number' || propType === 'integer') && <option value="<=">&lt;=</option>}
                                    
                                    {(propType === 'boolean') && <option value="==">&eq;&eq;</option>}
                                    {(propType === 'boolean') && <option value="!=">!=</option>}
                                </select>
                                
                                {propType === 'boolean' ? (
                                    <select className="builder-select flex-1 font-mono text-sm" value={val} onChange={e=>setVal(e.target.value)} disabled={!propName}>
                                        <option value="true">True</option>
                                        <option value="false">False</option>
                                    </select>
                                ) : propType === 'number' || propType === 'integer' ? (
                                    <input type="number" className="vs-input flex-1 font-mono text-sm" placeholder="e.g. 100" value={val} onChange={e=>setVal(e.target.value)} disabled={!propName} />
                                ) : (
                                    <input type="text" className="vs-input flex-1 font-mono text-sm" placeholder="Value..." value={val} onChange={e=>setVal(e.target.value)} disabled={!propName} />
                                )}
                                
                                <button className="vs-btn h-full px-6" onClick={addPolicy} disabled={!propName || val === ''}>Save</button>
                            </div>
                        </div>
                    </div>
                </div>
            );
        }

        const root = ReactDOM.createRoot(document.getElementById('root'));
        root.render(<App />);
    </script>
</body>
</html>`;
}
