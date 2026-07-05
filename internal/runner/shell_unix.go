//go:build !windows

package runner

// DefaultShell returns the default shell for Unix systems.
func DefaultShell() string {
	return "/bin/bash"
}