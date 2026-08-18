//go:build !windows

package main

import (
	"errors"
	"os"
	"syscall"
)

// processIsRunning reports whether pid names a live process.
//
// os.FindProcess alone answers nothing here: on Unix it never fails, so the
// signal probe is what actually asks the kernel. Signal 0 performs the existence
// and permission checks without delivering anything.
//
// Errors resolve towards "running". A wrong "not running" deletes a live
// sandbox's home, which is the failure this is guarding against; a wrong
// "running" leaves a directory for the next cleanup.
func processIsRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	// EPERM means the process exists and belongs to another user.
	return errors.Is(err, syscall.EPERM)
}
