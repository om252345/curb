package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	// Create a temporary directory for test configs
	tmpDir, err := os.MkdirTemp("", "curb-config-test")
	if err != nil {
		t.Fatalf("failed to create tmp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, "config.yml")
	yamlData := `
version: 1
workspace:
  root: /tmp/test
  sandbox: true
files:
  protect:
    - "*.env"
cli:
  rules:
    - name: "Block Force Push"
      command: git
      condition: args.contains("push") && args.contains("--force")
      action: block
mcp:
  servers:
    filesystem:
      upstream: "npx @mcp/filesystem"
`
	err = os.WriteFile(configPath, []byte(yamlData), 0644)
	if err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Version != 1 {
		t.Errorf("expected version 1, got %d", cfg.Version)
	}
	if cfg.Workspace.Root != "/tmp/test" {
		t.Errorf("expected root /tmp/test, got %s", cfg.Workspace.Root)
	}
	if len(cfg.Files.Protect) != 1 || cfg.Files.Protect[0] != "*.env" {
		t.Errorf("failed to parse file protections")
	}
	if len(cfg.CLI.Rules) != 1 || cfg.CLI.Rules[0].Name != "Block Force Push" {
		t.Errorf("failed to parse CLI rules")
	}
	if s, ok := cfg.MCP.Servers["filesystem"]; !ok || s.Upstream != "npx @mcp/filesystem" {
		t.Errorf("failed to parse MCP server config")
	}
}

func TestEnsureDefaultConfig(t *testing.T) {
	// We can't easily test the actual homedir version without mocking os.UserHomeDir
	// but we can test if it writes to a specific path if we had a non-global version.
	// Since EnsureDefaultConfig is hardcoded to DefaultConfigPath(), we'll just check
	// if we can at least call it or if we should refactor it for testability.
	
	// For now, let's just test that DefaultConfigDir() returns something sensible.
	dir := DefaultConfigDir()
	if dir == "" {
		t.Errorf("DefaultConfigDir returned empty string")
	}
}
