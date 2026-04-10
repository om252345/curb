package mcp

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Tool represents an MCP tool
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// JSONRPCRequest represents a standard MCP JSON-RPC request
type JSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// JSONRPCResponse represents a standard MCP JSON-RPC response
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

// JSONRPCError represents a standard MCP JSON-RPC error
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ListToolsResult represents the specific result type for tools/list
type ListToolsResult struct {
	Tools []Tool `json:"tools"`
}

// FetchToolSchema queries the DB for the given MCP server, spawns it, sends tools/list, and returns the response.
func FetchToolSchema(serverName string, db *sql.DB) ([]Tool, error) {
	var upstreamCmd string
	var envVars sql.NullString
	var isActive bool

	err := db.QueryRow("SELECT upstream_cmd, env_vars, is_active FROM mcp_servers WHERE name = ?", serverName).Scan(&upstreamCmd, &envVars, &isActive)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("MCP server %s not found in registry", serverName)
		}
		return nil, fmt.Errorf("failed to query MCP server: %w", err)
	}

	if !isActive {
		return nil, fmt.Errorf("MCP server %s is not active", serverName)
	}

	// Make sure we have a command to run
	parts := strings.Fields(upstreamCmd)
	if len(parts) == 0 {
		return nil, fmt.Errorf("upstream command is empty for %s", serverName)
	}

	// Create command execution context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // Ensure cleanup

	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)

	// Setup environment variables if any
	cmd.Env = []string{}
	// Inherit path and basic OS envs
	// Provide user defined env vars
	if envVars.Valid && envVars.String != "" {
		var envMap map[string]string
		if err := json.Unmarshal([]byte(envVars.String), &envMap); err == nil {
			for k, v := range envMap {
				cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
			}
		}
	}

	// Configure stdin & stdout
	stdinWriter, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to get stdin pipe: %w", err)
	}

	stdoutReader, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	// Start process
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start MCP server process: %w", err)
	}

	// We don't wait for it to exit, we will just kill it by defer cancel() later

	// 1. Send Initialize Request
	initReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2024-11-05", // Example MCP protocol version
			"clientInfo": map[string]string{
				"name":    "AgentGate",
				"version": "1.0.0",
			},
			"capabilities": map[string]interface{}{},
		},
	}
	initReqBytes, _ := json.Marshal(initReq)
	_, err = stdinWriter.Write(append(initReqBytes, '\n'))
	if err != nil {
		return nil, fmt.Errorf("failed to send initialize request: %w", err)
	}

	// Create a scanner to read stdout
	scanner := bufio.NewScanner(stdoutReader)

	// Wait for initialize response
	initialized := false
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue // skip empty lines
		}
		
		var resp JSONRPCResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			// might just be server logging to stdout instead of stderr, keep reading
			continue
		}

		if resp.ID == 1 {
			if resp.Error != nil {
				return nil, fmt.Errorf("MCP initialize error: %s", resp.Error.Message)
			}
			initialized = true
			break
		}
	}

	if !initialized {
		return nil, fmt.Errorf("failed to initialize MCP server %s", serverName)
	}

	// Send initialized notification
	initializedNotif := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	}
	initNotifBytes, _ := json.Marshal(initializedNotif)
	stdinWriter.Write(append(initNotifBytes, '\n'))

	// 2. Send tools/list Request
	toolsReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/list",
	}
	toolsReqBytes, _ := json.Marshal(toolsReq)
	_, err = stdinWriter.Write(append(toolsReqBytes, '\n'))
	if err != nil {
		return nil, fmt.Errorf("failed to send tools/list request: %w", err)
	}

	// Wait for tools/list response
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var resp JSONRPCResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			continue
		}

		if resp.ID == 2 {
			currentCancel := cancel
			cancel = func() {} // Prevent double close 
			
			// Try gracefully closing stdin to let it die, but forcefully kill as well
			stdinWriter.Close()
			currentCancel() // Kills the process

			if resp.Error != nil {
				return nil, fmt.Errorf("tools/list error: %s", resp.Error.Message)
			}

			var toolsResult ListToolsResult
			if err := json.Unmarshal(resp.Result, &toolsResult); err != nil {
				return nil, fmt.Errorf("failed to parse tools/list result: %w", err)
			}

			return toolsResult.Tools, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading MCP server stdout: %w", err)
	}

	return nil, fmt.Errorf("MCP server %s closed connection before returning tools", serverName)
}

// FetchToolSchemaFromConfig takes the upstream command and env directly from the YAML config.
// It supports both HTTP (`http://` or `https://`) and standard command-line Stdio upstreams.
// It utilizes a robust discovery client with fallbacks for non-compliant servers.
func FetchToolSchemaFromConfig(serverName string, upstream string, env map[string]string) ([]Tool, error) {
	if upstream == "" {
		return nil, fmt.Errorf("upstream is empty for %s", serverName)
	}

	var transport ClientTransport

	if strings.HasPrefix(upstream, "http://") || strings.HasPrefix(upstream, "https://") {
		transport = &HTTPTransport{
			URL: upstream,
		}
	} else if strings.HasPrefix(upstream, "exec:") {
		// Support "exec: npx ..." prefix
		parts := strings.Fields(strings.TrimPrefix(upstream, "exec:"))
		if len(parts) == 0 {
			return nil, fmt.Errorf("upstream command is empty for %s", serverName)
		}
		transport = &StdioTransport{
			Command: parts[0],
			Args:    parts[1:],
			Env:     env,
		}
	} else {
		// Standard stdio upstream
		parts := strings.Fields(upstream)
		if len(parts) == 0 {
			return nil, fmt.Errorf("upstream command is empty for %s", serverName)
		}
		transport = &StdioTransport{
			Command: parts[0],
			Args:    parts[1:],
			Env:     env,
		}
	}

	client := NewMCPClient(transport)
	
	// Create context with timeout for overall discovery
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	mcpTools, err := client.Discover(ctx)
	if err != nil {
		return nil, fmt.Errorf("discovery failed for %s: %w", serverName, err)
	}

	// Map analytics.MCPTool to Curb's mcp.Tool struct to maintain backward compatibility for the UI
	var finalTools []Tool
	for _, mt := range mcpTools {
		schemaBytes, _ := json.Marshal(mt.InputSchema)
		
		finalTools = append(finalTools, Tool{
			Name:        mt.Name,
			Description: mt.Description,
			InputSchema: schemaBytes,
		})
	}

	return finalTools, nil
}

