//go:build linux || darwin

package interceptor

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/term"
)

// runOSPTY spawns the user's default shell under the PTY interceptor.
func runOSPTY() {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	RunPTYWithCommand(shell)
}

// RunPTYWithCommand spawns an arbitrary command under the PTY interceptor.
// All executed commands are evaluated against CLI rules via IPC before execution.
func RunPTYWithCommand(command string, args ...string) {
	c := exec.Command(command, args...)
	ptmx, err := pty.Start(c)
	if err != nil {
		fmt.Fprintf(os.Stderr, "PTY start error: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = ptmx.Close() }()

	// Handle terminal window resizes
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	go func() {
		for range ch {
			if err := pty.InheritSize(os.Stdin, ptmx); err != nil {
				// silent
			}
		}
	}()
	ch <- syscall.SIGWINCH

	// Switch terminal to raw mode
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		panic(err)
	}
	defer func() { _ = term.Restore(int(os.Stdin.Fd()), oldState) }()

	// Goroutine: copy PTY output → user's stdout
	go func() {
		_, _ = io.Copy(os.Stdout, ptmx)
	}()

	// Goroutine: read user stdin → intercept → forward to PTY
	// This runs in a goroutine so c.Wait() can unblock the main goroutine
	go func() {
		buf := []byte{}
		for {
			b := make([]byte, 1)
			_, err := os.Stdin.Read(b)
			if err != nil {
				break
			}

			if b[0] == '\r' || b[0] == '\n' {
				commandString := sanitizeBuffer(buf)
				if commandString != "" && !isJustArrowKeys(buf) {
					allowed, reason := evaluateViaIPC(commandString)
					if !allowed {
						_, _ = ptmx.Write([]byte{3}) // Ctrl+C to discard
						errStr := fmt.Sprintf("\r\n\033[31m[Curb Blocked] %s\033[0m\r\n", reason)
						_, _ = os.Stdout.Write([]byte(errStr))
						buf = []byte{}
						continue
					} else if reason != "" {
						warnStr := fmt.Sprintf("\r\n\033[33m[Curb Warning] %s. Bypassing Curb.\033[0m", reason)
						_, _ = os.Stdout.Write([]byte(warnStr))
					}
				}
				_, _ = ptmx.Write(b)
				buf = []byte{}
			} else {
				buf = append(buf, b[0])
				_, _ = ptmx.Write(b)
			}
		}
	}()

	// Block here until the child process exits.
	// When claude quits (Ctrl+C or /exit), this returns immediately.
	_ = c.Wait()
}

