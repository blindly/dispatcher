//go:build windows

package runner

// DefaultShell returns the default shell for Windows systems.
func DefaultShell() string {
	return "powershell"
}