//go:build !windows && !linux
// +build !windows,!linux

package main

import (
	"os/exec"
	"runtime"
	"syscall"
)

// applySandboxIsolation applies Unix-specific process isolation.
// On macOS and other Unix systems, namespace support isn't available, so we rely
// on environment scrubbing and home redirection only.
func applySandboxIsolation(cmd *exec.Cmd, guestHome string) {
	// macOS / FreeBSD / other Unix: no kernel namespace support.
	// Environment scrubbing + HOME redirection is the isolation boundary.
	LogInfo("OS-level isolation not available on %s; using environment isolation only", runtime.GOOS)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		// Create a new process group so signals don't propagate uncontrolled
		Setpgid: true,
	}
}

func closeTokenHandle(cmd *exec.Cmd) {}

