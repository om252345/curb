package ipc

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/google/cel-go/cel"
	"github.com/om252345/curb/internal/audit"
	"github.com/om252345/curb/internal/evaluator"
	"github.com/om252345/curb/internal/state"
)

func generateToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func GetUnixSocketPath() string {
	return "/tmp/curb.sock"
}

func GetWindowsTokenPath() string {
	return filepath.Join(os.TempDir(), "curb.ipc_token")
}

func StartServer(cache *state.PolicyCache, celEnv *cel.Env, askUserCallback func(string, string) bool) {
	if runtime.GOOS == "windows" {
		startWindowsServer(cache, celEnv, askUserCallback)
	} else {
		startUnixServer(cache, celEnv, askUserCallback)
	}
}

func startUnixServer(cache *state.PolicyCache, celEnv *cel.Env, askUserCallback func(string, string) bool) {
	sockPath := GetUnixSocketPath()
	os.Remove(sockPath)

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		log.Printf("[IPC] Failed to start Unix socket: %v", err)
		return
	}

	os.Chmod(sockPath, 0600)

	log.Printf("[IPC] Listening for CLI commands on Unix socket %s", sockPath)
	go acceptLoop(listener, "", cache, celEnv, askUserCallback)
}

func startWindowsServer(cache *state.PolicyCache, celEnv *cel.Env, askUserCallback func(string, string) bool) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Printf("[IPC] Failed to start TCP fallback: %v", err)
		return
	}

	port := listener.Addr().(*net.TCPAddr).Port
	token := generateToken()

	tokenPath := GetWindowsTokenPath()
	data := fmt.Sprintf("%d\n%s", port, token)
	if err := os.WriteFile(tokenPath, []byte(data), 0600); err != nil {
		log.Printf("[IPC] Failed to write token file: %v", err)
		return
	}

	log.Printf("[IPC] Listening for CLI commands on 127.0.0.1:%d", port)
	go acceptLoop(listener, token, cache, celEnv, askUserCallback)
}

func acceptLoop(listener net.Listener, requiredToken string, cache *state.PolicyCache, celEnv *cel.Env, askUserCallback func(string, string) bool) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("[IPC] Accept error: %v", err)
			continue
		}
		go handleConn(conn, requiredToken, cache, celEnv, askUserCallback)
	}
}

type IPCRequest struct {
	Method string   `json:"method"`
	Params []string `json:"params"`
}

type IPCResponse struct {
	Result IPCResult `json:"result"`
}

type IPCResult struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
}

func EvaluateCLI(args []string, cache *state.PolicyCache, celEnv *cel.Env, askUserCallback func(string, string) bool) (bool, string) {
	if len(args) == 0 {
		return true, ""
	}

	cmdCtx := evaluator.CommandContext{
		Args:          args,
		ScriptPayload: "", // PTY input wrapper buffer stream payload parsing for future completeness
		Depth:         0,
	}

	baseCmd := filepath.Base(args[0])

	for _, rule := range cache.GetEndpointRules() {
		// Verify if the trigger target applies
		triggers := strings.Split(rule.TriggerTargets, ",")
		triggered := false
		for _, t := range triggers {
			trimmed := strings.TrimSpace(t)
			if trimmed == baseCmd || trimmed == "*" {
				triggered = true
				break
			}
		}

		if !triggered {
			continue
		}

		match, err := evaluator.EvaluateRule(celEnv, rule.Condition, cmdCtx, "", "")
		if err != nil {
			log.Printf("[Cel Eval Error] Rule %s failed: %v", rule.Name, err)
			continue
		}

		if match {
			if rule.Action == "block" {
				return false, rule.ErrorMsg
			} else if rule.Action == "ask" || rule.Action == "hitl" {
				// Trigger the VS Code extension HITL modal if available
				allowed := false
				if askUserCallback != nil {
					allowed = askUserCallback(rule.Name, baseCmd)
				}
				if !allowed {
					reason := rule.ErrorMsg
					if reason == "" {
						reason = "Blocked by User via CLI Guard HITL"
					}
					return false, reason
				}
			}
		}
	}

	return true, ""
}

func handleConn(conn net.Conn, requiredToken string, cache *state.PolicyCache, celEnv *cel.Env, askUserCallback func(string, string) bool) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 1024*1024)

	if requiredToken != "" {
		if !scanner.Scan() {
			return
		}
		if scanner.Text() != requiredToken {
			fmt.Fprintf(conn, "ERROR: Invalid Auth Token\n")
			return
		}
	}

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "{") {
			var req IPCRequest
			if err := json.Unmarshal([]byte(trimmed), &req); err == nil {
				if req.Method == "evaluate_cli" {
					allowed, reason := EvaluateCLI(req.Params, cache, celEnv, askUserCallback)

					if audit.Global != nil {
						payloadBytes, _ := json.Marshal(req.Params)
						action := "allow"
						if !allowed {
							action = "block"
						}
						audit.Global.LogEvent("cli", string(payloadBytes), action)
					}

					resp := IPCResponse{
						Result: IPCResult{
							Allowed: allowed,
							Reason:  reason,
						},
					}
					outBytes, _ := json.Marshal(resp)
					fmt.Fprintf(conn, "%s\n", string(outBytes))
				} else if req.Method == "vscode_hitl_request" {
					// Expect Params: [toolName, mcpTarget]
					allowed := false
					if askUserCallback != nil && len(req.Params) == 2 {
						allowed = askUserCallback(req.Params[0], req.Params[1])
					}

					reason := ""
					if !allowed {
						reason = "Blocked by User via MCP Guard HITL"
					}

					resp := IPCResponse{
						Result: IPCResult{
							Allowed: allowed,
							Reason:  reason,
						},
					}
					outBytes, _ := json.Marshal(resp)
					fmt.Fprintf(conn, "%s\n", string(outBytes))
				}
				continue
			}
		}

		switch trimmed {
		case "PAUSE":
			log.Println("[IPC] [WARN] curb globally PAUSED via CLI")
			fmt.Fprintf(conn, "SUCCESS: curb Paused\n")
		case "RESUME":
			log.Println("[IPC] [INFO] curb globally RESUMED via CLI")
			fmt.Fprintf(conn, "SUCCESS: curb Resumed\n")
		default:
			fmt.Fprintf(conn, "ERROR: Unknown Command\n")
		}
	}
}

func DialCmd(command string) error {
	var conn net.Conn
	var err error

	if runtime.GOOS == "windows" {
		tokenPath := GetWindowsTokenPath()
		data, readErr := os.ReadFile(tokenPath)
		if readErr != nil {
			return fmt.Errorf("curb does not appear to be running (cannot find token): %v", readErr)
		}
		parts := strings.SplitN(string(data), "\n", 2)
		if len(parts) != 2 {
			return fmt.Errorf("corrupt IPC token file")
		}
		port, token := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])

		conn, err = net.Dial("tcp", "127.0.0.1:"+port)
		if err != nil {
			return fmt.Errorf("curb does not appear to be running: %v", err)
		}
		defer conn.Close()
		fmt.Fprintf(conn, "%s\n", token)
	} else {
		conn, err = net.Dial("unix", GetUnixSocketPath())
		if err != nil {
			return fmt.Errorf("curb does not appear to be running on unix socket: %v", err)
		}
		defer conn.Close()
	}

	fmt.Fprintf(conn, "%s\n", command)

	scanner := bufio.NewScanner(conn)
	if scanner.Scan() {
		response := scanner.Text()
		if strings.HasPrefix(response, "SUCCESS") {
			return nil
		}
		if strings.HasPrefix(response, "{") {
			return nil
		}
		return fmt.Errorf("server error: %s", response)
	}
	return fmt.Errorf("no response from server")
}

func DialCmdWithResponse(command string) (string, error) {
	var conn net.Conn
	var err error

	if runtime.GOOS == "windows" {
		tokenPath := GetWindowsTokenPath()
		data, readErr := os.ReadFile(tokenPath)
		if readErr != nil {
			return "", fmt.Errorf("curb does not appear to be running (cannot find token): %v", readErr)
		}
		parts := strings.SplitN(string(data), "\n", 2)
		if len(parts) != 2 {
			return "", fmt.Errorf("corrupt IPC token file")
		}
		port, token := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])

		conn, err = net.Dial("tcp", "127.0.0.1:"+port)
		if err != nil {
			return "", fmt.Errorf("curb does not appear to be running: %v", err)
		}
		defer conn.Close()
		fmt.Fprintf(conn, "%s\n", token)
	} else {
		conn, err = net.Dial("unix", GetUnixSocketPath())
		if err != nil {
			return "", fmt.Errorf("curb does not appear to be running on unix socket: %v", err)
		}
		defer conn.Close()
	}

	fmt.Fprintf(conn, "%s\n", command)

	scanner := bufio.NewScanner(conn)
	if scanner.Scan() {
		return scanner.Text(), nil
	}
	return "", fmt.Errorf("no response from server")
}
