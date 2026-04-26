//go:build windows

package runner

import (
	"os/exec"
)

// runCommandTTY is not implemented on Windows. The runtime gate
// `stdinIsTTY` will normally prevent us from getting here on Windows
// (Windows consoles are detected differently and creack/pty has limited
// Windows support), but if we do, return a clear error.
func runCommandTTY(_ *exec.Cmd, _ int) (int, string) {
	return -2, "interactive TTY mode is not supported on Windows"
}
