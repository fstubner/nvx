//go:build !windows

package main

import (
	"os"
	"syscall"
)

// lockFileExclusive takes an exclusive advisory lock on f, blocking until it is
// available. Released by unlockFile or by closing the file.
func lockFileExclusive(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
