package cmd

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/om252345/curb/internal/audit"
	"github.com/om252345/curb/internal/config"
	"github.com/om252345/curb/internal/db"
	"github.com/om252345/curb/internal/evaluator"
	"github.com/om252345/curb/internal/ipc"
	"github.com/om252345/curb/internal/mcp"
	"github.com/om252345/curb/internal/state"
	"github.com/spf13/cobra"
)

type RPCCall struct {
	ID     int                    `json:"id"`
	Method string                 `json:"method"`
	Params map[string]interface{} `json:"params"`
}

type RPCResponse struct {
	ID     int         `json:"id"`
	Result interface{} `json:"result,omitempty"`
	Error  interface{} `json:"error,omitempty"`
}

// configWatcher is the module-level config watcher for hot-reload
var cfgWatcher *config.ConfigWatcher

var (
	pendingAskMutex sync.Mutex
	pendingAsks     = make(map[string]chan bool)
)

// CallExtensionAsk sends a JSON-RPC notification to the IDE and waits for its boolean reply
func CallExtensionAsk(toolName string, target string) bool {
	id := fmt.Sprintf("%d", time.Now().UnixNano())
	ch := make(chan bool, 1)

	pendingAskMutex.Lock()
	pendingAsks[id] = ch
	pendingAskMutex.Unlock()

	notif := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "vscode_hitl_request",
		"params": map[string]interface{}{
			"id":       id,
			"toolName": toolName,
			"target":   target,
		},
	}
	b, _ := json.Marshal(notif)
	fmt.Fprintln(os.Stdout, string(b))

	// Block indefinitely until IDE clicks "Approve" (true) or "Block" (false)
	// (or IDE crashes, in which case timeout is missing, but user preferred indefinite)
	res := <-ch
	return res
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Starts the Curb Hub daemon for VS Code integration and real-time audit logs",
	Run: func(cmd *cobra.Command, args []string) {
		log.SetOutput(os.Stderr)
		log.Println("[Stdio] Starting curb stdio RPC loop...")

		// ── 1. Ensure default config exists ──
		config.EnsureDefaultConfig()

		// ── 2. Load YAML config (rules source) ──
		cfgPaths := config.ConfigPaths()
		cfg, err := config.LoadConfig(cfgPaths...)
		if err != nil {
			log.Printf("[Stdio] Config warning: %v (using defaults)", err)
			cfg = &config.Config{Version: 1, MCP: config.MCPConfig{Servers: map[string]config.MCPServer{}}}
		}

		// ── 3. Init policy cache from YAML config (for IPC CLI evaluation) ──
		cache := state.NewPolicyCache()
		loadCacheFromConfig(cache, cfg)

		// ── 4. Start config watcher for hot-reload ──
		cfgWatcher = config.NewConfigWatcher(cfgPaths, func(newCfg *config.Config) {
			log.Println("[Stdio] Config reloaded — rules updated from config.yml")
			loadCacheFromConfig(cache, newCfg)
		})
		cfgWatcher.Start()
		defer cfgWatcher.Stop()

		// ── 5. Init SQLite for audit logs ONLY ──
		dbPath := config.DefaultDBPath()

		database, err := db.InitDB(dbPath)
		if err != nil {
			log.Fatalf("Failed to init DB for stdio: %v", err)
		}

		celEnv, err := evaluator.CreateCELEnv(database)
		if err != nil {
			log.Fatalf("Failed to init CEL Env: %v", err)
		}

		audit.Global = audit.NewAuditLogger(database, 100)
		defer audit.Global.Close()

		// Start the Unix socket IPC server for the CLI interceptor
		ipc.StartServer(cache, celEnv, CallExtensionAsk)

		// ── 6. Run stdio RPC loop ──
		RunStdioLoop(cfg, cache, database)
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
}

// loadCacheFromConfig populates the policy cache from YAML config
func loadCacheFromConfig(cache *state.PolicyCache, cfg *config.Config) {
	// We still load from DB for backward compatibility, but YAML is primary
	// The cache is used by the IPC server for CLI command evaluation
	cache.ClearAndReload(cfg.CLI.Rules)
}

func RunStdioLoop(cfg *config.Config, cache *state.PolicyCache, database *sql.DB) {
	scanner := bufio.NewScanner(os.Stdin)

	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()

		if len(line) == 0 {
			continue
		}

		var call RPCCall
		if err := json.Unmarshal(line, &call); err != nil {
			log.Printf("[Stdio] Invalid JSON received: %v", err)
			continue
		}

		// Always get the latest config from the watcher (hot-reload)
		currentCfg := cfg
		if cfgWatcher != nil {
			currentCfg = cfgWatcher.Current()
		}

		switch call.Method {

		case "vscode_hitl_response":
			id, _ := call.Params["id"].(string)
			allowed, _ := call.Params["allowed"].(bool)

			pendingAskMutex.Lock()
			if ch, exists := pendingAsks[id]; exists {
				ch <- allowed
				delete(pendingAsks, id)
			}
			pendingAskMutex.Unlock()
			continue

		// ═══════════════════════════════════════════
		//  FILE PROTECTION — reads/writes .curb.yml
		// ═══════════════════════════════════════════

		case "get_file_rules":
			resp := RPCResponse{
				ID: call.ID,
				Result: map[string]interface{}{
					"blocked_patterns": currentCfg.Files.Protect,
				},
			}
			sendResponse(resp)

		case "get_protected_resources":
			var resources []map[string]interface{}
			for i, pattern := range currentCfg.Files.Protect {
				resources = append(resources, map[string]interface{}{
					"id":      i,
					"type":    "file",
					"pattern": pattern,
				})
			}
			if resources == nil {
				resources = make([]map[string]interface{}, 0)
			}
			sendResponse(RPCResponse{ID: call.ID, Result: resources})

		case "add_protected_resource":
			pattern, _ := call.Params["pattern"].(string)
			if pattern != "" {
				currentCfg.Files.Protect = append(currentCfg.Files.Protect, pattern)
				saveCurrentConfig(currentCfg)
			}
			sendResponse(RPCResponse{ID: call.ID, Result: "ok"})

		case "remove_protected_resource":
			idVal := int(call.Params["id"].(float64))
			if idVal >= 0 && idVal < len(currentCfg.Files.Protect) {
				currentCfg.Files.Protect = append(
					currentCfg.Files.Protect[:idVal],
					currentCfg.Files.Protect[idVal+1:]...,
				)
				saveCurrentConfig(currentCfg)
			}
			sendResponse(RPCResponse{ID: call.ID, Result: "ok"})

		// ═══════════════════════════════════════════
		//  CLI GUARDS — reads/writes .curb.yml
		// ═══════════════════════════════════════════

		case "get_guards":
			var guards []map[string]interface{}
			for i, rule := range currentCfg.CLI.Rules {
				guards = append(guards, map[string]interface{}{
					"id":              i,
					"name":            rule.Name,
					"trigger_targets": rule.Command,
					"condition":       rule.Condition,
					"action":          rule.Action,
					"error_message":   fmt.Sprintf("Blocked by Curb: %s", rule.Name),
					"is_active":       true,
				})
			}
			if guards == nil {
				guards = make([]map[string]interface{}, 0)
			}
			sendResponse(RPCResponse{ID: call.ID, Result: guards})

		case "add_cli_guard":
			name, _ := call.Params["name"].(string)
			targets, _ := call.Params["trigger_targets"].(string)
			cond, _ := call.Params["condition"].(string)
			action, _ := call.Params["action"].(string)

			currentCfg.CLI.Rules = append(currentCfg.CLI.Rules, config.CLIRule{
				Name:      name,
				Command:   targets,
				Condition: cond,
				Action:    action,
			})
			saveCurrentConfig(currentCfg)
			loadCacheFromConfig(cache, currentCfg)
			sendResponse(RPCResponse{ID: call.ID, Result: "ok"})

		case "remove_guard":
			idVal := int(call.Params["id"].(float64))
			if idVal >= 0 && idVal < len(currentCfg.CLI.Rules) {
				currentCfg.CLI.Rules = append(
					currentCfg.CLI.Rules[:idVal],
					currentCfg.CLI.Rules[idVal+1:]...,
				)
				saveCurrentConfig(currentCfg)
				loadCacheFromConfig(cache, currentCfg)
			}
			sendResponse(RPCResponse{ID: call.ID, Result: "ok"})

		// ═══════════════════════════════════════════
		//  MCP SERVERS — reads/writes .curb.yml
		// ═══════════════════════════════════════════

		case "get_mcp_servers":
			var servers []map[string]interface{}
			for name, srv := range currentCfg.MCP.Servers {
				envBytes, _ := json.Marshal(srv.Env)
				headersBytes, _ := json.Marshal(srv.Headers)
				servers = append(servers, map[string]interface{}{
					"name":         name,
					"upstream_cmd": srv.Upstream,
					"env_vars":     string(envBytes),
					"headers":      string(headersBytes),
					"is_active":    true,
				})
			}
			if servers == nil {
				servers = make([]map[string]interface{}, 0)
			}
			sendResponse(RPCResponse{ID: call.ID, Result: servers})

		case "sync_mcp_server":
			name, _ := call.Params["name"].(string)
			upstream, _ := call.Params["upstream_cmd"].(string)
			envVarsStr, _ := call.Params["env_vars"].(string)
			headersStr, _ := call.Params["headers_json"].(string)

			var envMap map[string]string
			if envVarsStr != "" {
				json.Unmarshal([]byte(envVarsStr), &envMap)
			}

			var headersMap map[string]string
			if headersStr != "" {
				json.Unmarshal([]byte(headersStr), &headersMap)
			}

			if currentCfg.MCP.Servers == nil {
				currentCfg.MCP.Servers = make(map[string]config.MCPServer)
			}

			// Preserve existing policies when updating server
			existing, hasExisting := currentCfg.MCP.Servers[name]
			var policies []config.MCPPolicy
			if hasExisting {
				policies = existing.Policies
			}

			currentCfg.MCP.Servers[name] = config.MCPServer{
				Upstream: upstream,
				Env:      envMap,
				Headers:  headersMap,
				Policies: policies,
			}
			saveCurrentConfig(currentCfg)
			sendResponse(RPCResponse{ID: call.ID, Result: "ok"})

		case "sync_mcp_json":
			jsonPayloadStr, _ := call.Params["mcp_json"].(string)
			log.Printf("[Stdio] Ingesting MCP config payload (length: %d)", len(jsonPayloadStr))

			var parsedMap map[string]interface{}
			if err := json.Unmarshal([]byte(jsonPayloadStr), &parsedMap); err == nil {
				var mcpMap map[string]interface{}
				if val, ok := parsedMap["mcpServers"]; ok {
					mcpMap, _ = val.(map[string]interface{})
				} else if val, ok := parsedMap["servers"]; ok {
					mcpMap, _ = val.(map[string]interface{})
				}

				if mcpMap != nil {
					if currentCfg.MCP.Servers == nil {
						currentCfg.MCP.Servers = make(map[string]config.MCPServer)
					}

					for name, srvInf := range mcpMap {
						srv, ok := srvInf.(map[string]interface{})
						if !ok {
							continue
						}

						cmdIf, hasCmd := srv["command"]
						if !hasCmd {
							continue
						}

						cmdStr, _ := cmdIf.(string)
						if strings.Contains(cmdStr, "curb") || strings.Contains(cmdStr, "curb") {
							continue
						}

						argsIf, _ := srv["args"].([]interface{})
						cmdArgs := cmdStr
						for _, argI := range argsIf {
							if argStr, ok := argI.(string); ok {
								cmdArgs += " " + argStr
							}
						}

						var envMap map[string]string
						if envIf, ok := srv["env"]; ok {
							envBytes, _ := json.Marshal(envIf)
							json.Unmarshal(envBytes, &envMap)
						}

						var headersMap map[string]string
						if headersIf, ok := srv["headers"]; ok {
							headersBytes, _ := json.Marshal(headersIf)
							json.Unmarshal(headersBytes, &headersMap)
						}

						log.Printf("[Stdio] Intercepting MCP server: %s | Command: %s", name, cmdArgs)

						// Preserve existing policies
						existing, hasExisting := currentCfg.MCP.Servers[name]
						var policies []config.MCPPolicy
						if hasExisting {
							policies = existing.Policies
						}

						currentCfg.MCP.Servers[name] = config.MCPServer{
							Upstream: cmdArgs,
							Env:      envMap,
							Headers:  headersMap,
							Policies: policies,
						}
					}
					saveCurrentConfig(currentCfg)
				}
			}
			sendResponse(RPCResponse{ID: call.ID, Result: "ok"})

		case "toggle_mcp_server":
			// In YAML, all servers are active. Toggle = remove/add.
			// For MVP, we just acknowledge the request.
			sendResponse(RPCResponse{ID: call.ID, Result: "ok"})

		case "fetch_mcp_tools":
			name, _ := call.Params["server_name"].(string)

			srv, ok := currentCfg.MCP.Servers[name]
			if !ok {
				sendResponse(RPCResponse{
					ID: call.ID,
					Error: map[string]interface{}{
						"message": "Server " + name + " not found in config.yml. Proxy the server through Curb first.",
					},
				})
				break
			}

			tools, fetchErr := mcp.FetchToolSchemaFromConfig(name, srv.Upstream, srv.Env)

			if fetchErr != nil {
				sendResponse(RPCResponse{
					ID: call.ID,
					Error: map[string]interface{}{
						"message": fetchErr.Error(),
					},
				})
			} else {
				sendResponse(RPCResponse{ID: call.ID, Result: tools})
			}

		// ═══════════════════════════════════════════
		//  MCP POLICIES — reads/writes .curb.yml
		// ═══════════════════════════════════════════

		case "get_mcp_policies":
			var policies []map[string]interface{}
			globalID := 0
			for srvName, srv := range currentCfg.MCP.Servers {
				for _, policy := range srv.Policies {
					policies = append(policies, map[string]interface{}{
						"id":          globalID,
						"server_name": srvName,
						"tool_name":   policy.Tool,
						"condition":   policy.Condition,
						"action":      policy.Action,
						"error_msg":   policy.Message,
					})
					globalID++
				}
			}
			if policies == nil {
				policies = make([]map[string]interface{}, 0)
			}
			sendResponse(RPCResponse{ID: call.ID, Result: policies})

		case "add_mcp_policy":
			srvName, _ := call.Params["server_name"].(string)
			tool, _ := call.Params["tool_name"].(string)
			cond, _ := call.Params["condition"].(string)
			action, _ := call.Params["action"].(string)
			errMsg, _ := call.Params["error_msg"].(string)

			if srv, ok := currentCfg.MCP.Servers[srvName]; ok {
				srv.Policies = append(srv.Policies, config.MCPPolicy{
					Tool:      tool,
					Condition: cond,
					Action:    action,
					Message:   errMsg,
				})
				currentCfg.MCP.Servers[srvName] = srv
				saveCurrentConfig(currentCfg)
			}
			sendResponse(RPCResponse{ID: call.ID, Result: "ok"})

		case "remove_mcp_policy":
			// The global ID is computed by iterating servers in map order.
			// We need to find which server+policy index this maps to.
			targetID := int(call.Params["id"].(float64))
			globalID := 0
			removed := false
			for srvName, srv := range currentCfg.MCP.Servers {
				for i := range srv.Policies {
					if globalID == targetID {
						srv.Policies = append(srv.Policies[:i], srv.Policies[i+1:]...)
						currentCfg.MCP.Servers[srvName] = srv
						saveCurrentConfig(currentCfg)
						removed = true
						break
					}
					globalID++
				}
				if removed {
					break
				}
			}
			sendResponse(RPCResponse{ID: call.ID, Result: "ok"})

		// ═══════════════════════════════════════════
		//  AUDIT LOGS — stays in SQLite
		// ═══════════════════════════════════════════

		case "get_audit_logs":
			var logs []map[string]interface{}
			rows, err := database.Query("SELECT id, timestamp, source, payload, action_taken FROM audit_logs ORDER BY id DESC LIMIT 50")
			if err == nil {
				defer rows.Close()
				for rows.Next() {
					var id int
					var timestamp, source, payload, action string
					rows.Scan(&id, &timestamp, &source, &payload, &action)
					logs = append(logs, map[string]interface{}{
						"id":           id,
						"timestamp":    timestamp,
						"source":       source,
						"payload":      payload,
						"action_taken": action,
					})
				}
			}
			if logs == nil {
				logs = make([]map[string]interface{}, 0)
			}
			sendResponse(RPCResponse{ID: call.ID, Result: logs})

		case "log_audit_event":
			source, _ := call.Params["source"].(string)
			payload, _ := call.Params["payload"].(string)
			action, _ := call.Params["action"].(string)
			if audit.Global != nil {
				audit.Global.LogEvent(source, payload, action)
			}
			sendResponse(RPCResponse{ID: call.ID, Result: "ok"})

		default:
			log.Printf("[Stdio] Unknown method: %s", call.Method)
			sendResponse(RPCResponse{
				ID: call.ID,
				Error: map[string]interface{}{
					"code":    -32601,
					"message": "Method not found",
				},
			})
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("[Stdio] Scanner error: %v", err)
	}

	log.Println("[Stdio] Stdio loop exited.")
}

// ── Helpers ──

func sendResponse(resp RPCResponse) {
	outBytes, _ := json.Marshal(resp)
	fmt.Fprintln(os.Stdout, string(outBytes))
}

func saveCurrentConfig(cfg *config.Config) {
	if err := config.SaveConfig(cfg, config.DefaultConfigPath()); err != nil {
		log.Printf("[Stdio] Failed to save config: %v", err)
		return
	}
	// Immediately update the watcher's internal state so subsequent RPC reads
	// return the freshly saved config without waiting for the 2s poll cycle.
	if cfgWatcher != nil {
		cfgWatcher.SetCurrent(cfg)
	}
}
