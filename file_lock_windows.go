//go:build windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	procLockFileEx   = modKernel32.NewProc("LockFileEx")
	procUnlockFileEx = modKernel32.NewProc("UnlockFileEx")
)

const lockfileExclusiveLock = 0x00000002

// lockFileExclusive takes an exclusive lock on the first byte of f, blocking
// until it is available. Released by unlockFile or by closing the handle.
func lockFileExclusive(f *os.File) error {
	var ov syscall.Overlapped
	r, _, err := procLockFileEx.Call(f.Fd(), lockfileExclusiveLock, 0, 1, 0, uintptr(unsafe.Pointer(&ov)))
	if r == 0 {
		return err
	}
	return nil
}

func unlockFile(f *os.File) error {
	var ov syscall.Overlapped
	r, _, err := procUnlockFileEx.Call(f.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&ov)))
	if r == 0 {
		return err
	}
	return nil
}
