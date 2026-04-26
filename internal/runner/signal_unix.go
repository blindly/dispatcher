//go:build !windows

package runner

import (
	"os/exec"
	"syscall"
)

// signalRC returns 128+sig (bash convention) if exitErr indicates the
// child was killed by a signal, otherwise 0. Used by both runCommand
// and runCommandTTY so that signal-killed exits surface uniformly and
// can be classified by isInterrupted.
func signalRC(exitErr *exec.ExitError) int {
	if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return 0
}
