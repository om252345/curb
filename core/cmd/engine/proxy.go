package cmd

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/google/cel-go/cel"
	"github.com/om252345/curb/internal/audit"
	"github.com/om252345/curb/internal/config"
	"github.com/om252345/curb/internal/db"
	"github.com/om252345/curb/internal/evaluator"
	"github.com/om252345/curb/internal/ipc"
	"github.com/spf13/cobra"
)

var mcpProxyCmd = &cobra.Command{
	Use:   "mcp-proxy [server_name]",
	Short: "Acts as a secure MCP gateway (Deep Payload Inspection)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		serverName := args[0]

		// STDIO proxies should log exclusively to stderr to avoid polluting stdout
		log.SetOutput(os.Stderr)

		// Try YAML config first (curb run flow), fall back to SQLite (VS Code extension flow)
		cfg, yamlErr := config.LoadConfig(config.ConfigPaths()...)
		if yamlErr == nil {
			if server, ok := cfg.MCP.Servers[serverName]; ok {
				log.Printf("[Curb Proxy] Using YAML config for server '%s'", serverName)
				runProxyFromYAML(serverName, server, cfg)
				return
			}
		}

		// Fallback: SQLite-based config (backward compat with VS Code extension)
		log.Printf("[Curb Proxy] Falling back to SQLite config for server '%s'", serverName)

		dbPath := config.DefaultDBPath()

		database, err := db.InitDB(dbPath)
		if err != nil {
			log.Fatalf("Failed to init DB for proxy: %v", err)
		}

		celEnv, err := evaluator.CreateCELEnv(database)
		if err != nil {
			log.Fatalf("Failed to init CEL Env: %v", err)
		}

		audit.Global = audit.NewAuditLogger(database, 100)
		defer audit.Global.Close()

		var upstreamCmd string
		var envVars sql.NullString
		err = database.QueryRow("SELECT upstream_cmd, env_vars FROM mcp_servers WHERE name = ? AND is_active = 1", serverName).Scan(&upstreamCmd, &envVars)
		if err != nil {
			log.Fatalf("Failed to retrieve active MCP server '%s': %v", serverName, err)
		}

		runProxy(serverName, upstreamCmd, envVars, database, celEnv)
	},
}

func init() {
	rootCmd.AddCommand(mcpProxyCmd)
}

// ── YAML-based proxy (used by curb run) ──

func runProxyFromYAML(serverName string, server config.MCPServer, cfg *config.Config) {
	// Init audit DB
	dbPath := config.DefaultDBPath()
	database, err := db.InitDB(dbPath)
	if err != nil {
		log.Printf("[Curb Proxy] Audit DB warning: %v (continuing without audit)", err)
	} else {
		audit.Global = audit.NewAuditLogger(database, 100)
		defer audit.Global.Close()
	}

	// Create CEL environment for policy evaluation
	celEnv, err := cel.NewEnv(
		cel.Variable("args", cel.ListType(cel.StringType)),
		cel.Variable("payload", cel.StringType),
		cel.Variable("depth", cel.IntType),
		cel.Variable("server", cel.StringType),
		cel.Variable("tool", cel.StringType),
		cel.Variable("mcp_args", cel.DynType),
	)
	if err != nil {
		log.Fatalf("[Curb Proxy] Failed to create CEL env: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Parse upstream command
	parts := strings.Fields(server.Upstream)
	if len(parts) == 0 {
		log.Fatalf("[Curb Proxy] Upstream command is empty for server '%s'", serverName)
	}

	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
	cmd.Env = os.Environ()

	// Inject server-specific env vars from .curb.yml
	for k, v := range server.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	stdinWriter, err := cmd.StdinPipe()
	if err != nil {
		log.Fatalf("Failed to get stdin pipe: %v", err)
	}

	stdoutReader, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatalf("Failed to get stdout pipe: %v", err)
	}

	stderrReader, err := cmd.StderrPipe()
	if err != nil {
		log.Fatalf("Failed to get stderr pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		log.Fatalf("Failed to start upstream MCP server: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// Intercept stdin — evaluate tools/call against YAML policies
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(os.Stdin)
		buf := make([]byte, 1024*1024)
		scanner.Buffer(buf, 1024*1024)

		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}

			var rpcReq mcpJSONRPC
			if err := json.Unmarshal(line, &rpcReq); err == nil && rpcReq.Method == "tools/call" {
				var callParams struct {
					Name      string                 `json:"name"`
					Arguments map[string]interface{} `json:"arguments"`
				}
				if err := json.Unmarshal(rpcReq.Params, &callParams); err == nil {
					toolName := callParams.Name
					payloadBytes, _ := json.Marshal(callParams.Arguments)
					payloadStr := string(payloadBytes)

					// Evaluate YAML policies for this tool
					allowed := true
					reason := ""

					for _, policy := range server.Policies {
						if policy.Tool != toolName {
							continue
						}

						evalCtx := map[string]interface{}{
							"args":     []string{},
							"payload":  payloadStr,
							"depth":    0,
							"server":   serverName,
							"tool":     toolName,
							"mcp_args": callParams.Arguments,
						}

						ast, issues := celEnv.Compile(policy.Condition)
						if issues != nil && issues.Err() != nil {
							log.Printf("[Curb Proxy] CEL compile error: %v", issues.Err())
							continue
						}
						prg, _ := celEnv.Program(ast)
						out, _, err := prg.Eval(evalCtx)
						if err != nil {
							log.Printf("[Curb Proxy] CEL Evaluation ERROR: %v", err)
							allowed = false
							reason = fmt.Sprintf("Policy eval error: %v", err)
							break
						}

						if outVal, ok := out.Value().(bool); ok && outVal {
							if policy.Action == "block" {
								allowed = false
								reason = policy.Message
								if reason == "" {
									reason = "Blocked by Curb policy"
								}
								break
							} else if policy.Action == "ask" || policy.Action == "hitl" {
								// Dial Hub to trigger a VSCode Popup
								req := ipc.IPCRequest{
									Method: "vscode_hitl_request",
									Params: []string{toolName, serverName},
								}
								reqBytes, _ := json.Marshal(req)
								respStr, err := ipc.DialCmdWithResponse(string(reqBytes))
								if err == nil {
									var resp ipc.IPCResponse
									if json.Unmarshal([]byte(respStr), &resp) == nil {
										if !resp.Result.Allowed {
											allowed = false
											reason = resp.Result.Reason
											if reason == "" {
												reason = "Blocked by User via MCP Guard HITL"
											}
											break
										}
									} else {
										log.Printf("[Curb Proxy] HITL IPC payload invalid.")
									}
								} else {
									// Fail closed if IPC is down
									allowed = false
									reason = "Curb Hub Unreachable for HITL approval"
									break
								}
							}
						}
					}

					// Audit log
					actionStr := "allow"
					if !allowed {
						actionStr = "block"
					}
					if audit.Global != nil {
						audit.Global.LogEvent("mcp", fmt.Sprintf("Tool Call: %s(%s)", toolName, payloadStr), actionStr)
					}

					if !allowed {
						resp := map[string]interface{}{
							"jsonrpc": "2.0",
							"id":      rpcReq.ID,
							"error": map[string]interface{}{
								"code":    -32000,
								"message": fmt.Sprintf("Curb Gateway Blocked: %s", reason),
							},
						}
						respBytes, _ := json.Marshal(resp)
						fmt.Fprintln(os.Stdout, string(respBytes))
						continue
					}
				}
			}

			// Forward to upstream
			stdinWriter.Write(line)
			stdinWriter.Write([]byte("\n"))
		}
		stdinWriter.Close()
	}()

	// Flush stdout transparently
	go func() {
		defer wg.Done()
		io.Copy(os.Stdout, stdoutReader)
	}()

	// Stream stderr
	go func() {
		io.Copy(os.Stderr, stderrReader)
	}()

	cmd.Wait()
	wg.Wait()
}

// ── SQLite-based proxy (used by VS Code extension) ──

type mcpJSONRPC struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func runProxy(serverName string, upstreamCmd string, envVars sql.NullString, database *sql.DB, celEnv *cel.Env) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	parts := strings.Fields(upstreamCmd)
	if len(parts) == 0 {
		log.Fatalf("Upstream command is empty")
	}

	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
	cmd.Env = os.Environ()

	if envVars.Valid && envVars.String != "" {
		var envMap map[string]string
		if err := json.Unmarshal([]byte(envVars.String), &envMap); err == nil {
			for k, v := range envMap {
				cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
			}
		}
	}

	stdinWriter, err := cmd.StdinPipe()
	if err != nil {
		log.Fatalf("Failed to get stdin pipe: %v", err)
	}

	stdoutReader, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatalf("Failed to get stdout pipe: %v", err)
	}

	stderrReader, err := cmd.StderrPipe()
	if err != nil {
		log.Fatalf("Failed to get stderr pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		log.Fatalf("Failed to start upstream MCP server process: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// Intercept Stdin from IDE
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(os.Stdin)
		buf := make([]byte, 1024*1024)
		scanner.Buffer(buf, 1024*1024)

		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}

			var rpcReq mcpJSONRPC
			if err := json.Unmarshal(line, &rpcReq); err == nil && rpcReq.Method == "tools/call" {
				var callParams struct {
					Name      string                 `json:"name"`
					Arguments map[string]interface{} `json:"arguments"`
				}
				if err := json.Unmarshal(rpcReq.Params, &callParams); err == nil {
					toolName := callParams.Name
					payloadBytes, _ := json.Marshal(callParams.Arguments)
					payloadStr := string(payloadBytes)

					// Fetch policies for this tool
					rows, err := database.Query("SELECT condition, action, error_msg FROM mcp_tool_policies WHERE server_name = ? AND tool_name = ?", serverName, toolName)
					allowed := true
					reason := ""

					if err == nil {
						defer rows.Close()
						for rows.Next() {
							var cond, action, errMsg string
							rows.Scan(&cond, &action, &errMsg)

							// Construct Evaluation Context
							evalCtx := map[string]interface{}{
								"args":     []string{}, // empty for MCP context
								"payload":  payloadStr,
								"depth":    0,
								"server":   serverName,
								"tool":     toolName,
								"mcp_args": callParams.Arguments,
							}

							ast, issues := celEnv.Compile(cond)
							if issues != nil && issues.Err() != nil {
								log.Printf("CEL Compile Error: %v", issues.Err())
								continue
							}
							prg, _ := celEnv.Program(ast)
							out, _, err := prg.Eval(evalCtx)
							if err == nil {
								if outVal, ok := out.Value().(bool); ok && outVal {
									// Condition met
									if action == "block" {
										allowed = false
										reason = errMsg
										break
									}
								}
							}
						}
					}

					audit.Global.LogEvent("mcp", fmt.Sprintf("Tool Call: %s(%s)", toolName, payloadStr), map[bool]string{true: "allow", false: "block"}[allowed])

					if !allowed {
						// Blocked: return a synthetically crafted error
						resp := map[string]interface{}{
							"jsonrpc": "2.0",
							"id":      rpcReq.ID,
							"error": map[string]interface{}{
								"code":    -32000,
								"message": fmt.Sprintf("Curb Gateway Blocked Execution: %s", reason),
							},
						}
						respBytes, _ := json.Marshal(resp)
						fmt.Fprintln(os.Stdout, string(respBytes))
						continue
					}
				}
			}

			// Forward
			stdinWriter.Write(line)
			stdinWriter.Write([]byte("\n"))
		}
		stdinWriter.Close()
	}()

	// Flush Stdout transparently
	go func() {
		defer wg.Done()
		io.Copy(os.Stdout, stdoutReader)
	}()

	// Stream Stderr unconditionally to Stderr
	go func() {
		io.Copy(os.Stderr, stderrReader)
	}()

	cmd.Wait()
	wg.Wait()
}
