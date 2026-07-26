//go:build !windows

package runner

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/term"
)

// stripANSI removes common ANSI/VT100 escape sequences from s so that
// PTY-captured output is readable in notifications and logs.
func stripANSI(s string) string {
	// ECMA-48 CSI sequences: ESC [ ... final_byte
	// Also strips OSC sequences: ESC ] ... BEL/ST
	result := make([]byte, 0, len(s))
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' {
			i++
			if i >= len(s) {
				break
			}
			if s[i] == '[' {
				// CSI sequence: skip until final byte (0x40–0x7E)
				i++
				for i < len(s) && (s[i] < 0x40 || s[i] > 0x7E) {
					i++
				}
				if i < len(s) {
					i++
				}
				continue
			}
			if s[i] == ']' {
				// OSC sequence: skip until BEL (0x07) or ST (ESC \)
				i++
				for i < len(s) {
					if s[i] == '\x07' {
						i++
						break
					}
					if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '\\' {
						i += 2
						break
					}
					i++
				}
				continue
			}
			// Other 2-byte ESC sequences (ESC + one char)
			i++
			continue
		}
		result = append(result, s[i])
		i++
	}
	return string(result)
}

// runCommandTTY runs cmd attached to a pseudo-terminal, with the parent's
// stdin in raw mode and SIGWINCH forwarded so the child sees window resizes.
// Output is streamed live to os.Stdout and also captured into a buffer
// (with ANSI escapes stripped) so notify: output and logs get clean text.
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
	defer func() {
		// A failed restore leaves the user's terminal in raw mode, so say so.
		if err := term.Restore(stdinFd, oldState); err != nil {
			warnf("restoring terminal mode: %v", err)
		}
	}()

	// Bidirectional copy: parent stdin → PTY, PTY → parent stdout + capture buffer.
	// stdin→pty in a goroutine; pty→(stdout+buffer) blocks here until the
	// child closes the PTY.
	var buf bytes.Buffer
	go func() { _, _ = io.Copy(ptmx, os.Stdin) }()
	_, _ = io.Copy(io.MultiWriter(os.Stdout, &buf), ptmx)

	output := stripANSI(buf.String())

	waited = true
	err = cmd.Wait()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if rc := signalRC(exitErr); rc != 0 {
				return rc, output
			}
			return exitErr.ExitCode(), output
		}
		return -2, err.Error()
	}
	return 0, output
}
