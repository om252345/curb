package evaluator

import (
	"testing"
)

func TestPeelCommand(t *testing.T) {
	tests := []struct {
		name          string
		raw           string
		expectedLeaf  string
		expectedDepth int
		expectedArgs  []string
	}{
		{
			name:          "Simple command",
			raw:           "ls -la",
			expectedLeaf:  "ls",
			expectedDepth: 0,
			expectedArgs:  []string{"ls", "-la"},
		},
		{
			name:          "Shell wrap",
			raw:           "sh -c 'rm -rf /'",
			expectedLeaf:  "rm",
			expectedDepth: 1,
			expectedArgs:  []string{"rm", "-rf", "/"},
		},
		{
			name:          "Double wrap",
			raw:           "bash -c \"sh -c 'git push --force'\"",
			expectedLeaf:  "git",
			expectedDepth: 2,
			expectedArgs:  []string{"git", "push", "--force"},
		},
		{
			name:          "Base64 obfuscation",
			raw:           "echo cm0gLXJmIC8= | base64 --decode",
			expectedLeaf:  "rm",
			expectedDepth: 1,
			expectedArgs:  []string{"rm", "-rf", "/"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := PeelCommand(tt.raw)
			if ctx.Leaf != tt.expectedLeaf {
				t.Errorf("expected leaf %s, got %s", tt.expectedLeaf, ctx.Leaf)
			}
			if ctx.Depth != tt.expectedDepth {
				t.Errorf("expected depth %d, got %d", tt.expectedDepth, ctx.Depth)
			}
			if len(ctx.Args) != len(tt.expectedArgs) {
				t.Errorf("expected %d args, got %d", len(tt.expectedArgs), len(ctx.Args))
			}
		})
	}
}

func TestCELContains(t *testing.T) {
	env, err := CreateCELEnv(nil)
	if err != nil {
		t.Fatalf("failed to create CEL env: %v", err)
	}

	cmdCtx := CommandContext{
		Args: []string{"git", "push", "--force"},
	}

	tests := []struct {
		condition string
		expected  bool
	}{
		{"args.contains('push')", true},
		{"args.contains('--force')", true},
		{"args.contains('pull')", false},
		{"args.contains('git')", true},
	}

	for _, tt := range tests {
		result, err := EvaluateRule(env, tt.condition, cmdCtx, "", "")
		if err != nil {
			t.Errorf("condition %s failed: %v", tt.condition, err)
			continue
		}
		if result != tt.expected {
			t.Errorf("condition %s expected %v, got %v", tt.condition, tt.expected, result)
		}
	}
}
