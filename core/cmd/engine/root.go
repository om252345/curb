package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var configPath string

var rootCmd = &cobra.Command{
	Use:   "curb",
	Short: "Curb is a security mesh and zero-trust proxy for AI agents.",
	Long:  "Protects local environments from AI agents across the Terminal, Filesystem, and MCP toolsets using deterministic, workspace-aware security policies.",
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", "curb.yaml", "Path to the configuration file")
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
