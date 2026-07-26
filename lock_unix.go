//go:build !windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func acquireLock(dir string) int {
	lockPath := filepath.Join(dir, ".dispatch.lock")
	fd, err := syscall.Open(lockPath, syscall.O_CREAT|syscall.O_WRONLY, 0644)
	if err != nil {
		// Not a contended lock — the lock file itself is unusable.
		fmt.Fprintf(os.Stderr, "Warning: cannot open lock file %s: %v\n", lockPath, err)
		return -1
	}
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if err != syscall.EWOULDBLOCK {
			fmt.Fprintf(os.Stderr, "Warning: cannot lock %s: %v\n", lockPath, err)
		}
		syscall.Close(fd)
		return -1
	}
	return fd
}

func releaseLock(fd int, dir string) {
	if err := syscall.Flock(fd, syscall.LOCK_UN); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: unlocking dispatch lock: %v\n", err)
	}
	if err := syscall.Close(fd); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: closing dispatch lock: %v\n", err)
	}
	lockPath := filepath.Join(dir, ".dispatch.lock")
	if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Warning: could not remove %s: %v\n", lockPath, err)
	}
}
