package interceptor

import (
	"testing"
)

func TestSanitizeBuffer(t *testing.T) {
	tests := []struct {
		name     string
		raw      []byte
		expected string
	}{
		{
			name:     "Simple command",
			raw:      []byte("ls -la\r"),
			expected: "ls -la",
		},
		{
			name:     "Backspace handling",
			raw:      []byte("lss\x7f -la"),
			expected: "ls -la",
		},
		{
			name:     "ANSI sequence stripping",
			raw:      []byte("ls\x1b[A -la"),
			expected: "ls -la",
		},
		{
			name:     "Ctrl+U handling",
			raw:      []byte("old command\x15new command"),
			expected: "new command",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeBuffer(tt.raw)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestSplitShellCommands(t *testing.T) {
	tests := []struct {
		input    string
		expected [][]string
	}{
		{
			input: "ls -la",
			expected: [][]string{
				{"ls", "-la"},
			},
		},
		{
			input: "ls && cat file",
			expected: [][]string{
				{"ls"},
				{"cat", "file"},
			},
		},
		{
			input: "echo hi | grep h; rm test",
			expected: [][]string{
				{"echo", "hi"},
				{"grep", "h"},
				{"rm", "test"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := splitShellCommands(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("expected %d subcommands, got %d", len(tt.expected), len(result))
				return
			}
			for i := range result {
				if len(result[i]) != len(tt.expected[i]) {
					t.Errorf("subcommand %d: expected length %d, got %d", i, len(tt.expected[i]), len(result[i]))
				}
			}
		})
	}
}
