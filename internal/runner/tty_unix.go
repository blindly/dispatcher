//go:build !windows

package runner

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

// runCommandTTY runs cmd attached to a pseudo-terminal, with the parent's
// stdin in raw mode and SIGWINCH forwarded so the child sees window resizes.
// Output is streamed live to os.Stdout — nothing is captured into a buffer
// or log file (PTY output contains ANSI escape codes that pollute logs).
//
// The timeout argument is accepted for signature symmetry with the buffered
// path but is ignored: ad-hoc TTY commands run until they exit on their own
// or the user interrupts them.
func runCommandTTY(cmd *exec.Cmd, _ int) (int, string) {
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return -2, fmt.Sprintf("pty start: %v", err)
	}
	defer func() { _ = ptmx.Close() }()

	// Ensure the child is reaped on every exit path after pty.Start succeeds.
	// On the happy path the explicit cmd.Wait() below sets waited=true first,
	// so the deferred call becomes a no-op there.
	waited := false
	defer func() {
		if !waited {
			_ = cmd.Wait()
		}
	}()

	// Forward SIGWINCH so the child sees terminal resizes.
	winchCh := make(chan os.Signal, 1)
	signal.Notify(winchCh, syscall.SIGWINCH)
	defer signal.Stop(winchCh)
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-winchCh:
				_ = pty.InheritSize(os.Stdin, ptmx)
			case <-done:
				return
			}
		}
	}()
	winchCh <- syscall.SIGWINCH // trigger initial size sync

	// Put parent stdin into raw mode so keystrokes (including Ctrl+C)
	// pass through to the child unmodified.
	stdinFd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(stdinFd)
	if err != nil {
		return -2, fmt.Sprintf("raw mode: %v", err)
	}
	defer func() { _ = term.Restore(stdinFd, oldState) }()

	// Bidirectional copy: parent stdin → PTY, PTY → parent stdout.
	// stdin→pty in a goroutine; pty→stdout blocks here until the
	// child closes the PTY.
	go func() { _, _ = io.Copy(ptmx, os.Stdin) }()
	_, _ = io.Copy(os.Stdout, ptmx)

	waited = true
	err = cmd.Wait()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			// Ctrl+C in the PTY is delivered to the child as SIGINT via the
			// terminal line discipline. signalRC surfaces it as 128+sig so
			// runJob can classify the exit as "interrupted" not "failed".
			if rc := signalRC(exitErr); rc != 0 {
				return rc, ""
			}
			return exitErr.ExitCode(), ""
		}
		return -2, err.Error()
	}
	return 0, ""
}
