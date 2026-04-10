package cmd

import (
	"github.com/om252345/curb/internal/interceptor"
	"github.com/spf13/cobra"
)

var ptyCmd = &cobra.Command{
	Use:   "pty",
	Short: "Starts the internal PTY interceptor for shell command evaluation",
	Run: func(cmd *cobra.Command, args []string) {
		interceptor.RunPTYSpoke()
	},
}

func init() {
	rootCmd.AddCommand(ptyCmd)
}
