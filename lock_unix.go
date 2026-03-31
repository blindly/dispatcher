//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"syscall"
)

func acquireLock(dir string) int {
	lockPath := filepath.Join(dir, ".dispatch.lock")
	fd, err := syscall.Open(lockPath, syscall.O_CREAT|syscall.O_WRONLY, 0644)
	if err != nil {
		return -1
	}
	err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		syscall.Close(fd)
		return -1
	}
	return fd
}

func releaseLock(fd int, dir string) {
	syscall.Flock(fd, syscall.LOCK_UN)
	syscall.Close(fd)
	os.Remove(filepath.Join(dir, ".dispatch.lock"))
}
