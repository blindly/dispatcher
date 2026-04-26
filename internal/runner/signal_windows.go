//go:build windows

package runner

import "os/exec"

// signalRC always returns 0 on Windows — Windows processes don't have
// the Unix concept of signal-induced termination that we want to
// classify as "interrupted".
func signalRC(_ *exec.ExitError) int {
	return 0
}
