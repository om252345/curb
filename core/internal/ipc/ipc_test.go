package ipc

import (
	"testing"

	"github.com/om252345/curb/internal/config"
	"github.com/om252345/curb/internal/evaluator"
	"github.com/om252345/curb/internal/state"
)

func TestEvaluateCLI(t *testing.T) {
	cache := state.NewPolicyCache()
	cache.ClearAndReload([]config.CLIRule{
		{
			Name:      "Block RM RF",
			Command:   "rm",
			Condition: "args.contains('-rf')",
			Action:    "block",
		},
		{
			Name:      "Ask for GIT",
			Command:   "git",
			Condition: "args.contains('push')",
			Action:    "ask",
		},
	})

	env, _ := evaluator.CreateCELEnv(nil)

	tests := []struct {
		name     string
		args     []string
		expected bool
	}{
		{
			name:     "RM safe",
			args:     []string{"rm", "file.txt"},
			expected: true,
		},
		{
			name:     "RM dangerous",
			args:     []string{"rm", "-rf", "/"},
			expected: false,
		},
		{
			name:     "Safe command",
			args:     []string{"ls", "-la"},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowed, _ := EvaluateCLI(tt.args, cache, env, nil)
			if allowed != tt.expected {
				t.Errorf("expected allowed=%v, got %v", tt.expected, allowed)
			}
		})
	}
}
