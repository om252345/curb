//go:build windows

package proc

import (
	"os"
)

// Suspend pauses the execution of the process
// On Windows, true suspension without injecting into native threads is not natively supported by the Go os package.
// For Curb v0.1, this acts as a safe NOOP to allow Windows compilation and fallback to merely intercepting output.
func Suspend(p *os.Process) error {
	// Soft-pause / NOOP
	return nil
}

// Resume unpauses the execution of the process
func Resume(p *os.Process) error {
	// Soft-pause / NOOP
	return nil
}
