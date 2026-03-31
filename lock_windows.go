//go:build windows

package main

import (
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
		windows.UnlockFileEx(lockFile, 0, 1, 0, ol)
		windows.CloseHandle(lockFile)
		lockFile = windows.InvalidHandle
	}
	os.Remove(filepath.Join(dir, ".dispatch.lock"))
}
