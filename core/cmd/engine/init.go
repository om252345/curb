package cmd

import (
	"fmt"
	"log"
	"os"

	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a default Curb configuration file (.curb.yml)",
	Run: func(cmd *cobra.Command, args []string) {
		configPath := ".curb.yml"
		if _, err := os.Stat(configPath); err == nil {
			fmt.Printf("⚠️ Configuration file already exists at %s\n", configPath)
			return
		}

		defaultConfig := `version: 1
files:
  protect: ["*.env", "**/auth_token.json", "**/.curb.yml"]
cli:
  rules:
    - name: "Block Force Push"
      command: "git"
      condition: "args.contains('push') && args.contains('--force')"
      action: "block"
    - name: "HITL on Hard Reset"
      command: "git"
      condition: "args.contains('reset') && args.contains('--hard')"
      action: "hitl"
mcp:
  servers:
    github:
      upstream: "npx @modelcontextprotocol/server-github"
      rules:
        - tool: "create_pull_request"
          condition: "args.base == 'main'"
          action: "hitl"
`
		err := os.WriteFile(configPath, []byte(defaultConfig), 0644)
		if err != nil {
			log.Fatalf("Failed to create config: %v", err)
		}

		fmt.Printf("✅ Successfully created default configuration at %s\n", configPath)
		fmt.Println("🚀 You can now run 'curb run <your-agent-command>'")
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}
