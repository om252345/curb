//go:build windows

package interceptor

import (
	"fmt"
	"io"
	"os"

	"github.com/aymanbagabas/go-pty"
	"golang.org/x/term"
)

func runOSPTY() {
	shell := os.Getenv("COMSPEC")
	if shell == "" {
		shell = "cmd.exe"
	}

	ptmx, err := pty.New()
	if err != nil {
		fmt.Fprintf(os.Stderr, "PTY create error: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = ptmx.Close() }()

	c := ptmx.Command(shell)
	if err := c.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Cmd start error: %v\n", err)
		os.Exit(1)
	}

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		panic(err)
	}
	defer func() { _ = term.Restore(int(os.Stdin.Fd()), oldState) }()

	go func() {
		_, _ = io.Copy(os.Stdout, ptmx)
	}()

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
					_, _ = ptmx.Write([]byte{3})
					
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

	_ = c.Wait()
}
