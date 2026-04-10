package interceptor

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/om252345/curb/internal/ipc"
)

// ansiRegex catches standard ANSI navigation codes (like arrow keys) and bracketed paste pairs (~ ending)
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z~]`)

// sanitizeBuffer processes raw keystrokes into a clean command string
func sanitizeBuffer(rawBuffer []byte) string {
	var cleanLine []rune

	// 1. Process line-editing keystrokes sequentially
	for _, char := range string(rawBuffer) {
		switch char {
		case '\x7f', '\b': // Backspace
			if len(cleanLine) > 0 {
				cleanLine = cleanLine[:len(cleanLine)-1] // Remove previous char
			}
		case '\x03', '\x15': // Ctrl+C (Interrupt) or Ctrl+U (Clear line)
			cleanLine = []rune{} // Wipe the buffer completely
		default:
			cleanLine = append(cleanLine, char)
		}
	}

	// 2. Strip any remaining ANSI escape sequences (Arrow keys, etc.)
	cleanString := ansiRegex.ReplaceAllString(string(cleanLine), "")

	// 3. Trim surrounding whitespace
	return strings.TrimSpace(cleanString)
}

func isJustArrowKeys(rawBuffer []byte) bool {
	cleanString := ansiRegex.ReplaceAllString(string(rawBuffer), "")
	return strings.TrimSpace(cleanString) == ""
}

// splitShellCommands naively parses multiple commands to secure sub-execution overrides
func splitShellCommands(cmdLine string) [][]string {
	// Pad delimiters so they detach from glued text (e.g. echo hi&&cat)
	cmdLine = strings.ReplaceAll(cmdLine, "&&", " && ")
	cmdLine = strings.ReplaceAll(cmdLine, "||", " || ")
	cmdLine = strings.ReplaceAll(cmdLine, ";", " ; ")
	cmdLine = strings.ReplaceAll(cmdLine, "|", " | ")

	var subcommands [][]string
	delimiters := map[string]bool{"&&": true, "||": true, ";": true, "|": true}

	args := strings.Fields(cmdLine)
	var currentCmd []string

	for _, arg := range args {
		if delimiters[arg] {
			if len(currentCmd) > 0 {
				subcommands = append(subcommands, currentCmd)
				currentCmd = []string{}
			}
		} else {
			currentCmd = append(currentCmd, arg)
		}
	}

	if len(currentCmd) > 0 {
		subcommands = append(subcommands, currentCmd)
	}

	return subcommands
}

// evaluateViaIPC sends the parsed command line to the Hub
func evaluateViaIPC(cmdLine string) (bool, string) {
	subcommands := splitShellCommands(cmdLine)
	if len(subcommands) == 0 {
		return true, ""
	}

	for _, subcmdArgs := range subcommands {
		req := ipc.IPCRequest{
			Method: "evaluate_cli",
			Params: subcmdArgs,
		}

		outBytes, err := json.Marshal(req)
		if err != nil {
			return false, fmt.Sprintf("failed to encode ipc request: %v", err)
		}

		respStr, err := ipc.DialCmdWithResponse(string(outBytes))
		if err != nil {
			// If hub is down, we pass through transparently with a warning
			return true, fmt.Sprintf("Hub is unreachable: %v", err)
		}

		var resp ipc.IPCResponse
		if err := json.Unmarshal([]byte(respStr), &resp); err != nil {
			return false, "Invalid response from Hub"
		}

		// If ANY command in the pipeline gets blocked, axe the entire string execution payload
		if !resp.Result.Allowed {
			return false, resp.Result.Reason
		}
	}

	return true, ""
}

// RunPTYSpoke delegates to the OS-specific implementation
func RunPTYSpoke() {
	runOSPTY()
}
