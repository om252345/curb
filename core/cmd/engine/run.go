package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/om252345/curb/internal/config"
	"github.com/om252345/curb/internal/proc"
	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run [--] <command> [args...]",
	Short: "Execute a CLI agent (like Claude Code) inside the Curb security sandbox",
	Long: `Run any AI coding agent wrapped in Curb's defense layers:

  Layer 1: CLI commands intercepted via PATH wrapper scripts
  Layer 2: MCP tool calls intercepted via proxy
  Layer 3: Protected files locked via chmod 444
  Layer 4: Workspace sandbox via macOS sandbox-exec (macOS only)

Examples:
  curb run -- claude
  curb run -- aider --model gpt-4
  curb run -- bash`,
	DisableFlagParsing: true,
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) > 0 && args[0] == "--" {
			args = args[1:]
		}
		if len(args) == 0 {
			fmt.Fprintln(os.Stderr, "Usage: curb run [--] <command> [args...]")
			fmt.Fprintln(os.Stderr, "Example: curb run -- claude")
			os.Exit(1)
		}

		log.SetOutput(os.Stderr)

		// ── 1. Ensure default config exists ──
		config.EnsureDefaultConfig()

		// ── 2. Load config ──
		cfgPaths := config.ConfigPaths()
		cfg, err := config.LoadConfig(cfgPaths...)
		if err != nil {
			log.Printf("[Curb] Config warning: %v (using defaults)", err)
			cfg = &config.Config{Version: 1}
		}

		// ── 3. Start config watcher for hot-reload ──
		watcher := config.NewConfigWatcher(cfgPaths, func(newCfg *config.Config) {
			log.Println("[Curb] Rules updated — regenerating wrapper scripts")
			generateWrapperScripts(newCfg)
		})
		watcher.Start()
		defer watcher.Stop()

		// ── 4. Lock protected files (chmod 444) ──
		lockedFiles := lockProtectedFiles(cfg)
		defer unlockFiles(lockedFiles)

		// ── 5. Generate CLI wrapper scripts ──
		generateWrapperScripts(cfg)
		defer cleanupWrapperScripts()

		// ── 6. Rewrite MCP config (session-scoped) ──
		cleanupMCP := rewriteAgentMCPConfig(args[0], cfg)
		defer cleanupMCP()

		// ── 7. Generate sandbox profile (macOS only) ──
		sandboxProfile := ""
		if runtime.GOOS == "darwin" && cfg.Workspace.Sandbox {
			sandboxProfile = generateSandboxProfile(cfg)
			defer os.Remove(sandboxProfile)
		}

		// ── 8. Print banner ──
		printBanner(args[0], cfg, sandboxProfile != "")

		// ── 9. Spawn the agent ──
		spawnAgent(cfg, sandboxProfile, args[0], args[1:]...)
	},
}

func init() {
	rootCmd.AddCommand(runCmd)
}

// ── Agent Spawner ──

func spawnAgent(cfg *config.Config, sandboxProfile string, command string, args ...string) {
	curbTmpBinDir := filepath.Join(config.DefaultConfigDir(), "tmpbin")
	origPath := os.Getenv("PATH")
	modifiedPath := curbTmpBinDir + string(os.PathListSeparator) + origPath

	realCmd, err := exec.LookPath(command)
	if err != nil {
		realCmd = command
	}

	var c *exec.Cmd

	if sandboxProfile != "" {
		// macOS sandbox-exec: wraps the agent in a kernel-level sandbox
		// sandbox-exec -f <profile> <command> <args...>
		sandboxArgs := []string{"-f", sandboxProfile, realCmd}
		sandboxArgs = append(sandboxArgs, args...)
		c = exec.Command("/usr/bin/sandbox-exec", sandboxArgs...)
		log.Println("[Curb] 🏗️  Agent running inside macOS kernel sandbox")
	} else {
		c = exec.Command(realCmd, args...)
	}

	env := os.Environ()
	// Inject CURB_RUN_IPC to let evaluate.go communicate pauses to us
	runIpcPath := filepath.Join(os.TempDir(), fmt.Sprintf("curb-run-%d.sock", os.Getpid()))
	env = append(env, "CURB_RUN_IPC="+runIpcPath)

	os.Remove(runIpcPath)
	l, err := net.Listen("unix", runIpcPath)
	if err == nil {
		defer os.Remove(runIpcPath)
		defer l.Close()

		go func() {
			for {
				conn, err := l.Accept()
				if err != nil {
					return
				}
				go func(conn net.Conn) {
					defer conn.Close()
					scanner := bufio.NewScanner(conn)
					for scanner.Scan() {
						cmd := scanner.Text()
						if cmd == "PAUSE" {
							proc.Suspend(c.Process)
							fmt.Fprintf(conn, "OK\n")
						} else if cmd == "RESUME" {
							proc.Resume(c.Process)
							fmt.Fprintf(conn, "OK\n")
						} else if strings.HasPrefix(cmd, "ASK ") {
							proc.Suspend(c.Process)

							req := strings.TrimPrefix(cmd, "ASK ")
							parts := strings.SplitN(req, "|", 2)
							ruleName := "Unknown Rule"
							commandStr := req
							if len(parts) == 2 {
								ruleName = parts[0]
								commandStr = parts[1]
							}

							response := "n"
							prompted := false

							if runtime.GOOS == "darwin" {
								// macOS: Use native AppleScript popup
								script := fmt.Sprintf(`display dialog "Rule: %s\n\nAgent is attempting to execute:\n%s\n\nAllow this command?" with title "🚨 Curb HITL Intervention" buttons {"Deny", "Allow"} default button "Deny" with icon caution`, ruleName, commandStr)
								cmd := exec.Command("osascript", "-e", script)
								if out, err := cmd.Output(); err == nil {
									prompted = true
									if strings.Contains(string(out), "Allow") {
										response = "y"
									}
								}
							} else if runtime.GOOS == "windows" {
								// Windows: Use PowerShell PresentationFramework
								script := fmt.Sprintf(`Add-Type -AssemblyName PresentationFramework; $res = [System.Windows.MessageBox]::Show('Agent is attempting to execute:%s%s%sRule: %s', '🚨 Curb HITL Intervention', 'YesNo', 'Warning'); Write-Output $res`, "\n", commandStr, "\n\n", ruleName)
								cmd := exec.Command("powershell", "-Command", script)
								if out, err := cmd.Output(); err == nil {
									prompted = true
									if strings.Contains(string(out), "Yes") {
										response = "y"
									}
								}
							} else if runtime.GOOS == "linux" {
								// Linux: Try Zenity first (standard on GNOME/Ubuntu)
								text := fmt.Sprintf("Rule: %s\n\nAgent is attempting to execute:\n%s\n\nAllow this command?", ruleName, commandStr)
								cmd := exec.Command("zenity", "--question", "--title=🚨 Curb HITL Intervention", "--text="+text, "--icon-name=dialog-warning")
								err := cmd.Run()
								if err == nil {
									prompted = true
									response = "y" // Zenity exits 0 on Yes
								} else if err != nil && cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 1 {
									prompted = true
									response = "n" // Zenity exits 1 on No (it successfully showed the prompt but user declined)
								}
							}

							if !prompted {
								// Headless / Missing GUI Fallback: Direct Terminal
								tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
								if err != nil {
									tty = os.Stdin
								}
								fmt.Fprintf(os.Stderr, "\r\n===================================================================\r\n")
								fmt.Fprintf(os.Stderr, "🚨 \033[33m[CURB HITL INTERVENTION]\033[0m \r\n")
								fmt.Fprintf(os.Stderr, "Rule: %s\r\n", ruleName)
								fmt.Fprintf(os.Stderr, "Agent is attempting to execute: `\033[36m%s\033[0m`\r\n\r\n", commandStr)
								fmt.Fprintf(os.Stderr, "Allow this command? [y/N]: ")

								var responseBuilder strings.Builder
								b := make([]byte, 1)
								for {
									n, rErr := tty.Read(b)
									if rErr != nil || n == 0 || b[0] == '\r' || b[0] == '\n' {
										break
									}
									responseBuilder.WriteByte(b[0])
								}
								fmt.Fprintf(os.Stderr, "===================================================================\r\n\r\n")
								if tty != os.Stdin {
									tty.Close()
								}
								response = strings.TrimSpace(strings.ToLower(responseBuilder.String()))
							}

							proc.Resume(c.Process)

							if response == "y" || response == "yes" {
								fmt.Fprintf(conn, "ALLOW\n")
							} else {
								fmt.Fprintf(conn, "DENY\n")
							}
						}
					}
				}(conn)
			}
		}()
	}

	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	for i, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			env[i] = "PATH=" + modifiedPath
			break
		}
	}
	c.Env = env

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for sig := range sigCh {
			if c.Process != nil {
				c.Process.Signal(sig)
			}
		}
	}()

	if err := c.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		log.Printf("[Curb] Agent exited with error: %v", err)
	}
}

// ── macOS Sandbox Profile Generator ──

func generateSandboxProfile(cfg *config.Config) string {
	// Resolve workspace root
	workspaceRoot, _ := os.Getwd()
	if cfg.Workspace.Root != "" && cfg.Workspace.Root != "." {
		if filepath.IsAbs(cfg.Workspace.Root) {
			workspaceRoot = cfg.Workspace.Root
		} else {
			workspaceRoot = filepath.Join(workspaceRoot, cfg.Workspace.Root)
		}
	}
	workspaceRoot, _ = filepath.Abs(workspaceRoot)

	homeDir, _ := os.UserHomeDir()
	curbDir := config.DefaultConfigDir()

	// Generate the .sb sandbox profile
	profile := fmt.Sprintf(`(version 1)

;; ── Curb Workspace Sandbox ──
;; Auto-generated by curb. Do not edit.
;; Restricts file writes to ONLY the workspace directory.
;; The kernel itself enforces this — no process can bypass it.

;; Allow everything by default (read, network, exec, etc.)
(allow default)

;; ── DENY all file writes globally ──
(deny file-write* (subpath "/"))

;; ── ALLOW writes ONLY inside the workspace ──
(allow file-write* (subpath "%s"))

;; ── ALLOW writes to temp directories (needed for normal operation) ──
(allow file-write* (subpath "/tmp"))
(allow file-write* (subpath "/private/tmp"))
(allow file-write* (subpath "/private/var/folders"))
(allow file-write* (subpath "/var/folders"))

;; ── ALLOW writes to ~/.curb/ (audit logs, config) ──
(allow file-write* (subpath "%s"))

;; ── ALLOW writes to home dotfiles agents may need ──
(allow file-write* (subpath "%s/.claude"))
(allow file-write* (subpath "%s/.claude.json"))
(allow file-write* (subpath "%s/.cursor"))
(allow file-write* (subpath "%s/.config"))
(allow file-write* (subpath "%s/.npm"))
(allow file-write* (subpath "%s/.cache"))
(allow file-write* (subpath "%s/.mcp"))
(allow file-write* (subpath "%s/.vscode"))
(allow file-write* (subpath "%s/.gemini"))	
(allow file-write* (subpath "%s/.ollama"))
`, workspaceRoot, curbDir, homeDir, homeDir, homeDir, homeDir, homeDir, homeDir, homeDir, homeDir, homeDir, homeDir)

	// Write to a temp file
	profilePath := filepath.Join(os.TempDir(), "curb-sandbox.sb")
	if err := os.WriteFile(profilePath, []byte(profile), 0644); err != nil {
		log.Printf("[Curb] Warning: failed to write sandbox profile: %v", err)
		return ""
	}

	log.Printf("[Curb] 🔒 Sandbox: writes restricted to %s", workspaceRoot)
	return profilePath
}

// ── Wrapper Script Generator ──

func generateWrapperScripts(cfg *config.Config) {
	curbTmpBinDir := filepath.Join(config.DefaultConfigDir(), "tmpbin")
	os.MkdirAll(curbTmpBinDir, 0755)

	curbBinary, err := os.Executable()
	if err != nil {
		curbBinary = "curb"
	}

	commands := make(map[string]bool)
	for _, rule := range cfg.CLI.Rules {
		commands[rule.Command] = true
	}

	for command := range commands {
		wrapperPath := filepath.Join(curbTmpBinDir, command)
		realPath := findRealBinary(command, curbTmpBinDir)
		if realPath == "" {
			log.Printf("[Curb] Warning: cannot find real binary for '%s', skipping", command)
			continue
		}

		script := generateWrapperScript(command, realPath, curbBinary)
		if err := os.WriteFile(wrapperPath, []byte(script), 0755); err != nil {
			log.Printf("[Curb] Failed to write wrapper for '%s': %v", command, err)
			continue
		}

		log.Printf("[Curb] 🛡️  Wrapper: %s → intercepts '%s'", wrapperPath, command)
	}
}

func generateWrapperScript(command, realPath, curbBinary string) string {
	return fmt.Sprintf(`#!/bin/bash
# Curb CLI wrapper for '%s'
# Auto-generated — do not edit.

# Evaluate command against .curb.yml rules
# evaluate prints 'allow'/'block' to stdout and errors to stderr
%s evaluate %s "$@" > /dev/null
CURB_EXIT=$?

if [ $CURB_EXIT -ne 0 ]; then
    exit 1
fi

# Allowed — forward to real binary
exec %s "$@"
`, command, curbBinary, command, realPath)
}

func findRealBinary(command, excludeDir string) string {
	pathEnv := os.Getenv("PATH")
	dirs := strings.Split(pathEnv, string(os.PathListSeparator))

	for _, dir := range dirs {
		if dir == excludeDir {
			continue
		}
		candidate := filepath.Join(dir, command)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func cleanupWrapperScripts() {
	curbTmpBinDir := filepath.Join(config.DefaultConfigDir(), "tmpbin")
	entries, err := os.ReadDir(curbTmpBinDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		os.Remove(filepath.Join(curbTmpBinDir, entry.Name()))
	}
	// Also remove the directory itself
	os.Remove(curbTmpBinDir)
	log.Println("[Curb] Cleaned up runtime wrapper scripts")
}

// ── File Protection ──

func lockProtectedFiles(cfg *config.Config) map[string]os.FileMode {
	locked := make(map[string]os.FileMode)
	for _, pattern := range cfg.Files.Protect {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		for _, match := range matches {
			info, err := os.Stat(match)
			if err != nil || info.IsDir() {
				continue
			}
			locked[match] = info.Mode()
			if err := os.Chmod(match, 0444); err == nil {
				log.Printf("[Curb] 🔒 Locked: %s", match)
			}
		}
	}
	return locked
}

func unlockFiles(locked map[string]os.FileMode) {
	for path, mode := range locked {
		if err := os.Chmod(path, mode); err == nil {
			log.Printf("[Curb] 🔓 Unlocked: %s", path)
		}
	}
}

// ── MCP Config Rewrite ──

func rewriteAgentMCPConfig(agentCmd string, cfg *config.Config) func() {
	if len(cfg.MCP.Servers) == 0 {
		return func() {}
	}
	agentName := filepath.Base(agentCmd)
	switch agentName {
	case "claude":
		return rewriteClaudeMCPConfig(cfg)
	default:
		return func() {}
	}
}

func rewriteClaudeMCPConfig(cfg *config.Config) func() {
	mcpServers := make(map[string]interface{})
	curbBinary, err := os.Executable()
	if err != nil {
		curbBinary = "curb"
	}
	for name := range cfg.MCP.Servers {
		mcpServers[name] = map[string]interface{}{
			"command": curbBinary,
			"args":    []string{"mcp-proxy", name},
		}
	}

	claudeConfig := map[string]interface{}{"mcpServers": mcpServers}
	cwdMCPPath := ".mcp.json"
	data, _ := json.MarshalIndent(claudeConfig, "", "  ")

	var existingData []byte
	var hadExisting bool
	if existing, err := os.ReadFile(cwdMCPPath); err == nil {
		existingData = existing
		hadExisting = true
	}

	if err := os.WriteFile(cwdMCPPath, data, 0644); err != nil {
		log.Printf("[Curb] Warning: failed to write MCP config: %v", err)
		return func() {}
	}
	log.Printf("[Curb] 🔄 Rewrote .mcp.json — %d MCP servers proxied", len(mcpServers))

	return func() {
		if hadExisting {
			os.WriteFile(cwdMCPPath, existingData, 0644)
			log.Println("[Curb] Restored original .mcp.json")
		} else {
			os.Remove(cwdMCPPath)
			log.Println("[Curb] Removed session .mcp.json")
		}
	}
}

// ── Banner ──

func printBanner(agent string, cfg *config.Config, sandboxed bool) {
	commands := make(map[string]bool)
	for _, rule := range cfg.CLI.Rules {
		commands[rule.Command] = true
	}

	sandboxStatus := "off (enable in config.yml)"
	if sandboxed {
		sandboxStatus = "✅ active (macOS kernel-level)"
	} else if runtime.GOOS != "darwin" {
		sandboxStatus = "n/a (macOS only)"
	}

	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "\033[36m  ╭──────────────────────────────────────╮\033[0m\n")
	fmt.Fprintf(os.Stderr, "\033[36m  │       CURB WORKSPACE GUARDRAILS      │\033[0m\n")
	fmt.Fprintf(os.Stderr, "\033[36m  ╰──────────────────────────────────────╯\033[0m\n")
	fmt.Fprintf(os.Stderr, "\033[36m  Agent:\033[0m      %s\n", agent)
	fmt.Fprintf(os.Stderr, "\033[36m  Files:\033[0m      %d protected patterns\n", len(cfg.Files.Protect))
	fmt.Fprintf(os.Stderr, "\033[36m  CLI Rules:\033[0m  %d rules → %d commands wrapped\n", len(cfg.CLI.Rules), len(commands))
	fmt.Fprintf(os.Stderr, "\033[36m  MCP:\033[0m        %d servers proxied\n", len(cfg.MCP.Servers))
	fmt.Fprintf(os.Stderr, "\033[36m  Sandbox:\033[0m    %s\n", sandboxStatus)
	fmt.Fprintf(os.Stderr, "\033[36m  Config:\033[0m     ~/.curb/config.yml (hot-reload active)\n")
	fmt.Fprintf(os.Stderr, "\n")
}
