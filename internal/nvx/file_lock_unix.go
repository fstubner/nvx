//go:build !windows

package nvx

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

// tryLockFileExclusive takes the lock lockFileExclusive does without waiting for
// it. It reports false and no error when another file holds the lock.
func tryLockFileExclusive(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if err == syscall.EWOULDBLOCK {
		return false, nil
	}
	return false, err
}
