//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

var lockFile windows.Handle = windows.InvalidHandle

func acquireLock(dir string) int {
	lockPath := filepath.Join(dir, ".dispatch.lock")
	pathPtr, err := windows.UTF16PtrFromString(lockPath)
	if err != nil {
		return -1
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_WRITE,
		0, // no sharing
		nil,
		windows.CREATE_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return -1
	}

	// Try non-blocking exclusive lock
	ol := new(windows.Overlapped)
	err = windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, ol)
	if err != nil {
		windows.CloseHandle(handle)
		return -1
	}

	lockFile = handle
	return int(uintptr(unsafe.Pointer(&handle)))
}

func releaseLock(fd int, dir string) {
	if lockFile != windows.InvalidHandle {
		ol := new(windows.Overlapped)
		if err := windows.UnlockFileEx(lockFile, 0, 1, 0, ol); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: unlocking dispatch lock: %v\n", err)
		}
		if err := windows.CloseHandle(lockFile); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: closing dispatch lock: %v\n", err)
		}
		lockFile = windows.InvalidHandle
	}
	lockPath := filepath.Join(dir, ".dispatch.lock")
	if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Warning: could not remove %s: %v\n", lockPath, err)
	}
}
