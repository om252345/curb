//go:build !windows

package proc

import (
	"os"
	"syscall"
)

// Suspend pauses the execution of the process
func Suspend(p *os.Process) error {
	if p == nil {
		return nil
	}
	return p.Signal(syscall.SIGSTOP)
}

// Resume unpauses the execution of the process
func Resume(p *os.Process) error {
	if p == nil {
		return nil
	}
	return p.Signal(syscall.SIGCONT)
}
