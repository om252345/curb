package main

import (
	"context"
	"database/sql"
	"log"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/om252345/curb/internal/config"
	"github.com/om252345/curb/internal/db"
)

func main() {
	log.SetOutput(os.Stderr)
	log.Println("[Curb MCP Server] Starting standalone rule generator server...")

	dbPath := config.DefaultDBPath()

	database, err := db.InitDB(dbPath)
	if err != nil {
		log.Fatalf("Failed to init DB for mcp: %v", err)
	}

	RunMCPServer(database)
}

func RunMCPServer(database *sql.DB) {
	s := server.NewMCPServer("curb-security-generator", "1.0.0")

	tool := mcp.NewTool("curb_propose_rules",
		mcp.WithDescription("Propose custom workspace-specific security rules to the Curb Firewall. Rules are stored as drafts for user review."),
	)

	tool.InputSchema = mcp.ToolInputSchema{
		Type: "object",
		Properties: map[string]interface{}{
			"protected_files": map[string]interface{}{
				"type":        "array",
				"description": "File patterns (globs) to add to the protected resources list",
				"items": map[string]interface{}{
					"type": "string",
				},
			},
			"custom_rules": map[string]interface{}{
				"type":        "array",
				"description": "Endpoint rules (CEL-based) to guard specific tools or commands",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"name":            map[string]interface{}{"type": "string"},
						"guard_type":      map[string]interface{}{"type": "string", "enum": []string{"file", "cmd", "git", "mcp"}},
						"trigger_targets": map[string]interface{}{"type": "string", "description": "Comma-separated list of tools/commands"},
						"condition":       map[string]interface{}{"type": "string", "description": "CEL expression"},
						"action":          map[string]interface{}{"type": "string", "enum": []string{"block", "hitl"}},
						"error_message":   map[string]interface{}{"type": "string"},
					},
					"required": []string{"name", "guard_type", "trigger_targets", "condition", "action"},
				},
			},
		},
	}

	s.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, ok := request.Params.Arguments.(map[string]interface{})
		if !ok {
			return mcp.NewToolResultError("Invalid arguments payload"), nil
		}

		if files, ok := args["protected_files"].([]interface{}); ok {
			for _, f := range files {
				pattern, _ := f.(string)
				if pattern == "" {
					continue
				}
				_, err := database.Exec(`INSERT INTO protected_resources (type, pattern, metadata) VALUES ('file', ?, '{}')`, pattern)
				if err != nil {
					log.Printf("[MCP] Failed to insert resource: %v", err)
				}
			}
		}

		if rules, ok := args["custom_rules"].([]interface{}); ok {
			for _, r := range rules {
				rule, ok := r.(map[string]interface{})
				if !ok {
					continue
				}
				name, _ := rule["name"].(string)
				gType, _ := rule["guard_type"].(string)
				triggers, _ := rule["trigger_targets"].(string)
				condition, _ := rule["condition"].(string)
				action, _ := rule["action"].(string)
				errMsg, _ := rule["error_message"].(string)

				_, err := database.Exec(`INSERT INTO endpoint_rules (name, guard_type, trigger_targets, condition, action, error_message, is_active, source) VALUES (?, ?, ?, ?, ?, ?, 0, 'ai_draft')`,
					name, gType, triggers, condition, action, errMsg)
				if err != nil {
					log.Printf("[MCP] Failed to insert rule: %v", err)
				}
			}
		}

		return mcp.NewToolResultText("Successfully persisted security rules as drafts into Curb Database. The user will review them in the dashboard."), nil
	})

	log.Println("[Curb MCP Server] Listening on standard I/O...")
	if err := server.ServeStdio(s); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
