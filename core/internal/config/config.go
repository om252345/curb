package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ── Top-level Config ──

type Config struct {
	Version   int             `yaml:"version"`
	Workspace WorkspaceConfig `yaml:"workspace"`
	Files     FileConfig      `yaml:"files"`
	CLI       CLIConfig       `yaml:"cli"`
	MCP       MCPConfig       `yaml:"mcp"`
}

// ── Workspace Section ──

type WorkspaceConfig struct {
	Root    string `yaml:"root"`
	Sandbox bool   `yaml:"sandbox"`
}

// ── Files Section ──

type FileConfig struct {
	Protect []string `yaml:"protect"`
}

// ── CLI Section ──

type CLIConfig struct {
	Rules []CLIRule `yaml:"rules"`
}

type CLIRule struct {
	Name      string `yaml:"name"`
	Command   string `yaml:"command"`
	Condition string `yaml:"condition"`
	Action    string `yaml:"action"`
}

// ── MCP Section ──

type MCPConfig struct {
	Servers map[string]MCPServer `yaml:"servers"`
}

type MCPServer struct {
	Upstream string            `yaml:"upstream"`
	Env      map[string]string `yaml:"env,omitempty"`
	Headers  map[string]string `yaml:"headers,omitempty"`
	Policies []MCPPolicy       `yaml:"policies,omitempty"`
}

type MCPPolicy struct {
	Tool      string `yaml:"tool"`
	Condition string `yaml:"condition"`
	Action    string `yaml:"action"`
	Message   string `yaml:"message,omitempty"`
}

// ── Paths ──

// DefaultConfigDir returns ~/.curb/
func DefaultConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".curb")
}

// DefaultConfigPath returns ~/.curb/config.yml
func DefaultConfigPath() string {
	return filepath.Join(DefaultConfigDir(), "config.yml")
}

// DefaultDBPath returns ~/.curb/curb.db (audit logs only)
func DefaultDBPath() string {
	return filepath.Join(DefaultConfigDir(), "curb.db")
}

// ── Load / Save / Merge ──

// LoadConfig loads and merges YAML configs in order.
// First path is the base; subsequent paths overlay on top.
func LoadConfig(paths ...string) (*Config, error) {
	cfg := &Config{
		Version: 1,
		MCP:     MCPConfig{Servers: map[string]MCPServer{}},
	}

	loaded := 0
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("failed to read config %s: %w", p, err)
		}

		var overlay Config
		if err := yaml.Unmarshal(data, &overlay); err != nil {
			return nil, fmt.Errorf("failed to parse config %s: %w", p, err)
		}

		mergeConfig(cfg, &overlay)
		loaded++
	}

	if loaded == 0 {
		return cfg, fmt.Errorf("no config files found (searched: %v)", paths)
	}

	return cfg, nil
}

// SaveConfig writes the config to a YAML file, creating dirs as needed.
func SaveConfig(cfg *Config, path string) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config dir: %w", err)
	}

	return os.WriteFile(path, data, 0644)
}

// mergeConfig overlays src onto dst (dst is mutated).
func mergeConfig(dst, src *Config) {
	// Workspace: overlay overrides (project-level takes precedence)
	if src.Workspace.Root != "" {
		dst.Workspace.Root = src.Workspace.Root
	}
	if src.Workspace.Sandbox {
		dst.Workspace.Sandbox = true
	}

	// Union file patterns
	seen := make(map[string]bool)
	for _, p := range dst.Files.Protect {
		seen[p] = true
	}
	for _, p := range src.Files.Protect {
		if !seen[p] {
			dst.Files.Protect = append(dst.Files.Protect, p)
		}
	}

	// Append CLI rules (project rules come after user defaults)
	dst.CLI.Rules = append(dst.CLI.Rules, src.CLI.Rules...)

	// Merge MCP servers (overlay overrides by name)
	if dst.MCP.Servers == nil {
		dst.MCP.Servers = make(map[string]MCPServer)
	}
	for name, server := range src.MCP.Servers {
		dst.MCP.Servers[name] = server
	}
}

// ── Default Config Bootstrap ──

// EnsureDefaultConfig creates ~/.curb/config.yml with sensible defaults if missing.
// Returns the config path.
func EnsureDefaultConfig() (string, error) {
	configDir := DefaultConfigDir()
	configPath := DefaultConfigPath()

	if err := os.MkdirAll(configDir, 0755); err != nil {
		return "", err
	}

	// Don't overwrite existing config
	if _, err := os.Stat(configPath); err == nil {
		return configPath, nil
	}

	// Write a hand-crafted default config with helpful comments
	defaultYAML := `# ─────────────────────────────────────────────────────────
#  Curb — Configuration File
#  Firewall for AI coding agents
#  https://github.com/om252345/curb
# ─────────────────────────────────────────────────────────
#
#  Location: ~/.curb/config.yml (user defaults)
#  Override: .curb.yml in project root (team-shared rules)
#  Audit logs: ~/.curb/curb.db (SQLite)
#
#  Changes are hot-reloaded every 2 seconds — no restart needed.
# ─────────────────────────────────────────────────────────

version: 1

# ─── Workspace Sandbox ───────────────────────────────────
# Restricts the AI agent to read/write ONLY within the
# workspace directory. Uses macOS sandbox-exec (kernel-level).
#
# ⚠️  macOS only — sandbox-exec is a macOS kernel feature.
#     On Linux/Windows, this setting has no effect (yet).
#
# How it works:
#   When enabled, 'curb run claude' wraps the agent inside
#   sandbox-exec with a profile that DENIES all file writes
#   outside the workspace. The kernel itself enforces this
#   — no process can bypass it, not even direct fs.writeFile().
#
# root: "." means the current directory when you run 'curb run'
workspace:
    root: "."
    sandbox: false  # Set to true to enable (macOS only)

# ─── File Protection ────────────────────────────────────
# Files matching these patterns are locked via chmod 444.
# AI agents using direct fs.writeFile() get EACCES errors.
# These patterns also protect against MCP filesystem writes.
files:
    protect:
        - .env*            # Environment variables and secrets
        - "*.pem"          # TLS/SSL certificates
        - "*.key"          # Private keys
        - id_rsa*          # SSH keys
        - "*.p12"          # PKCS12 keystores
        - credentials*     # Cloud provider credentials (AWS, GCP)
        - .npmrc           # npm auth tokens
        - .pypirc          # PyPI auth tokens

# ═══════════════════════════════════════════════════════════
#  CLI Command Rules
# ═══════════════════════════════════════════════════════════
# Commands are intercepted via PATH wrapper scripts.
# Each rule uses a CEL expression on the args list.
#
# How it works:
#   'curb run claude' prepends ~/.curb/bin/ to PATH.
#   Wrapper scripts call 'curb evaluate <cmd> <args>'
#   which checks rules below. If matched → blocked.
#
# CEL quick reference:
#   args.contains("push")           → true if "push" in args
#   !args.contains("--dry-run")     → true if --dry-run NOT in args
#   args.contains("a") && args.contains("b")  → both must match
cli:
    rules:
        # ─── Git Safety ─────────────────────────────────
        - name: No Force Push
          command: git
          condition: args.contains("push") && (args.contains("--force") || args.contains("-f"))
          action: block

        - name: No Hard Reset
          command: git
          condition: args.contains("reset") && args.contains("--hard")
          action: hitl

        - name: No Branch Delete
          command: git
          condition: args.contains("branch") && (args.contains("-D") || args.contains("--delete"))
          action: hitl

        - name: No Clean Untracked
          command: git
          condition: args.contains("clean") && (args.contains("-f") || args.contains("-d"))
          action: hitl

        - name: No Rebase Main
          command: git
          condition: args.contains("rebase") && (args.contains("main") || args.contains("master"))
          action: block

        - name: No Push to Main
          command: git
          condition: args.contains("push") && (args.contains("main") || args.contains("master"))
          action: block

        - name: No Checkout Force
          command: git
          condition: args.contains("checkout") && args.contains("--force")
          action: block

        # ─── Filesystem Safety ──────────────────────────
        - name: No Recursive Delete
          command: rm
          condition: args.contains("-rf") || args.contains("-r")
          action: block

        - name: No Root Chmod
          command: chmod
          condition: args.contains("777")
          action: block

        # ─── Package Manager Safety ─────────────────────
        - name: No Global Install
          command: npm
          condition: args.contains("install") && args.contains("-g")
          action: block

        - name: No Curl Pipe
          command: curl
          condition: args.contains("|")
          action: block

# ═══════════════════════════════════════════════════════════
#  MCP Server Policies
# ═══════════════════════════════════════════════════════════
# MCP tool calls are intercepted via a transparent proxy.
# Curb sits between the AI agent and the MCP server,
# evaluating every tool call against CEL policies below.
#
# How it works:
#   1. Curb discovers MCP servers from your IDE's mcp.json
#   2. It stores the original upstream command here
#   3. It rewrites mcp.json to route through curb
#   4. Every tool call is evaluated against these policies
#
# These default policies protect against common MCP risks.
# Servers listed here are only active if they exist in your
# IDE's mcp.json — unused servers are silently ignored.
#
# CEL variables for MCP policies:
#   mcp_args.<field>   → tool call arguments
#   mcp_args.path      → file path (filesystem tools)
#   mcp_args.message   → commit message (git tools)
# mcp:
#     servers:
#         # ─── Git MCP Server ─────────────────────────────
#         # Policies for @modelcontextprotocol/server-git
#         # Tools: git_status, git_diff, git_commit, git_add,
#         #        git_reset, git_log, git_checkout, git_push,
#         #        git_create_branch, git_branch, git_show
#         git:
#             upstream: "uvx mcp-server-git"
#             policies:
#                 - tool: git_reset
#                   condition: "true"
#                   action: block
#                   message: "git reset via MCP is blocked — too risky for autonomous agents"
# 
#                 - tool: git_commit
#                   condition: "true"
#                   action: hitl
#                   message: "Agent wants to commit — review the changes before approving"
# 
#                 - tool: git_checkout
#                   condition: mcp_args.branch_name == "main" || mcp_args.branch_name == "master"
#                   action: hitl
#                   message: "Agent switching to protected branch — approve?"
# 
#                 - tool: git_create_branch
#                   condition: mcp_args.base_branch == "main" || mcp_args.base_branch == "master"
#                   action: hitl
#                   message: "Agent branching off trunk — approve?"
# 
#         # ─── Filesystem MCP Server ──────────────────────
#         # Policies for @modelcontextprotocol/server-filesystem
#         # Tools: read_file, read_text_file, write_file, edit_file,
#         #        create_directory, list_directory, move_file,
#         #        search_files, get_file_info, directory_tree
#         filesystem:
#             upstream: "npx -y @modelcontextprotocol/server-filesystem"
#             policies:
#                 - tool: write_file
#                   condition: mcp_args.path.contains(".env") || mcp_args.path.contains(".key") || mcp_args.path.contains(".pem") || mcp_args.path.contains("id_rsa") || mcp_args.path.contains("credentials")
#                   action: block
#                   message: "Write to sensitive file blocked by Curb"
# 
#                 - tool: edit_file
#                   condition: mcp_args.path.contains(".env") || mcp_args.path.contains(".key") || mcp_args.path.contains(".pem") || mcp_args.path.contains("id_rsa") || mcp_args.path.contains("credentials")
#                   action: block
#                   message: "Edit of sensitive file blocked by Curb"
# 
#                 - tool: move_file
#                   condition: mcp_args.source.contains(".env") || mcp_args.source.contains(".key") || mcp_args.destination.contains(".env") || mcp_args.destination.contains(".key")
#                   action: block
#                   message: "Moving sensitive files is blocked by Curb"
# 
#                 - tool: write_file
#                   condition: mcp_args.path.contains(".npmrc") || mcp_args.path.contains(".pypirc") || mcp_args.path.contains(".p12")
#                   action: block
#                   message: "Write to auth token file blocked by Curb"
`

	if err := os.WriteFile(configPath, []byte(defaultYAML), 0644); err != nil {
		return "", err
	}

	return configPath, nil
}

// ConfigPaths returns the ordered list of config paths to load
// (user default first, then project override).
func ConfigPaths() []string {
	paths := []string{DefaultConfigPath()}

	// Check for project-level .curb.yml in current working directory
	if cwd, err := os.Getwd(); err == nil {
		projectCfg := filepath.Join(cwd, ".curb.yml")
		if _, err := os.Stat(projectCfg); err == nil {
			paths = append(paths, projectCfg)
		}
	}

	return paths
}
