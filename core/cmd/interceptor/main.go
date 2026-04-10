package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

type IPCRequest struct {
	Method string   `json:"method"`
	Params []string `json:"params"`
}

type IPCResponse struct {
	Result IPCResult `json:"result"`
}

type IPCResult struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
}

func main() {
	// The wrapper script explicitly passes the target command as the first argument.
	// os.Args[0] = "/path/to/curb-interceptor"
	// os.Args[1] = "git" (or "python", "node", etc.)
	// os.Args[2:] = ["push", "--force"]
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "⚠️ [Curb Interceptor] Missing target command.")
		os.Exit(1)
	}

	targetCmd := os.Args[1]
	var cmdArgs []string
	if len(os.Args) > 2 {
		cmdArgs = os.Args[2:]
	}

	// 1. Dial IPC socket
	conn, err := net.Dial("unix", "/tmp/curb.sock")
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠️ [Curb Interceptor] curb not running or unreachable. Failing OPEN.\n")
		executeRealBinary(targetCmd, cmdArgs)
		return
	}
	defer conn.Close()

	// 2. Send evaluate_cli JSON-RPC
	// We want the params array to look like ["git", "push", "--force"] so Curb can parse it easily
	params := []string{targetCmd}
	params = append(params, cmdArgs...)

	req := IPCRequest{
		Method: "evaluate_cli",
		Params: params,
	}

	outBytes, err := json.Marshal(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠️ [Curb Interceptor] JSON marshal failed. Failing OPEN.\n")
		executeRealBinary(targetCmd, cmdArgs)
		return
	}

	_, err = fmt.Fprintf(conn, "%s\n", string(outBytes))
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠️ [Curb Interceptor] IPC Write failed. Failing OPEN.\n")
		executeRealBinary(targetCmd, cmdArgs)
		return
	}

	// 3. Read JSON Response
	reader := bufio.NewReader(conn)
	respLine, err := reader.ReadString('\n')
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠️ [Curb Interceptor] IPC Read failed. Failing OPEN.\n")
		executeRealBinary(targetCmd, cmdArgs)
		return
	}

	var resp IPCResponse
	if err := json.Unmarshal([]byte(respLine), &resp); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️ [Curb Interceptor] Invalid JSON response. Failing OPEN.\n")
		executeRealBinary(targetCmd, cmdArgs)
		return
	}

	// 4. Enforce Decision
	if !resp.Result.Allowed {
		// Print in ANSI Red text
		fmt.Fprintf(os.Stderr, "\033[31m⛔ Curb Guardrail Blocked Execution\nReason: %s\033[0m\n", resp.Result.Reason)
		os.Exit(1)
	}

	// Allowed => execute real binary
	executeRealBinary(targetCmd, cmdArgs)
}

func executeRealBinary(targetCmd string, cmdArgs []string) {
	// Find real binary in PATH backing out our own interceptor path
	realPath := findRealBinary(targetCmd)
	if realPath == "" {
		fmt.Fprintf(os.Stderr, "⚠️ [Curb Interceptor] Could not resolve real binary for %s\n", targetCmd)
		os.Exit(127)
	}

	cmd := exec.Command(realPath, cmdArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()

	// Forward OS signals to child process
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		for sig := range sigChan {
			if cmd.Process != nil {
				cmd.Process.Signal(sig)
			}
		}
	}()

	err := cmd.Run()

	if err != nil {
		// Exit with same code
		if exitError, ok := err.(*exec.ExitError); ok {
			os.Exit(exitError.ExitCode())
		}
		os.Exit(1)
	}

	os.Exit(0)
}

func findRealBinary(cmdName string) string {
	pathEnv := os.Getenv("PATH")
	paths := strings.Split(pathEnv, string(os.PathListSeparator))

	for _, dir := range paths {
		// Bypass the extension's injected PATH
		if strings.Contains(dir, ".curb") && strings.Contains(dir, ".vscode") || strings.Contains(dir, "curb-interceptor") {
			continue
		}

		fullPath := filepath.Join(dir, cmdName)
		if fileInfo, err := os.Stat(fullPath); err == nil && !fileInfo.IsDir() {
			// Ensure it has executable permissions (rough check for *nix, fine for MVP)
			if fileInfo.Mode()&0111 != 0 {
				return fullPath
			}
		}
	}
	return ""
}
