//go:build linux
// +build linux

package main

import (
	"os/exec"
	"syscall"
)

// applySandboxIsolation applies Unix-specific process isolation on Linux using namespaces.
func applySandboxIsolation(cmd *exec.Cmd, guestHome string) {
	applyLinuxNamespaces(cmd, guestHome)
}

// applyLinuxNamespaces configures the process with Linux kernel namespaces:
//   - CLONE_NEWNS: Isolate mount namespace (prevents bind-mount escape)
//   - CLONE_NEWPID: Isolate PID namespace (sandboxed process sees itself as PID 1)
//   - CLONE_NEWUSER: required for unprivileged namespace creation
func applyLinuxNamespaces(cmd *exec.Cmd, guestHome string) {
	cloneFlags := uintptr(0)

	// CLONE_NEWNS: new mount namespace
	cloneFlags |= syscall.CLONE_NEWNS

	// CLONE_NEWPID: new PID namespace
	cloneFlags |= syscall.CLONE_NEWPID

	// CLONE_NEWUSER: required for unprivileged namespace creation
	cloneFlags |= syscall.CLONE_NEWUSER

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: cloneFlags,
		// Map the current user into the new user namespace
		UidMappings: []syscall.SysProcIDMap{
			{
				ContainerID: 0,
				HostID:      syscall.Getuid(),
				Size:        1,
			},
		},
		GidMappings: []syscall.SysProcIDMap{
			{
				ContainerID: 0,
				HostID:      syscall.Getgid(),
				Size:        1,
			},
		},
		// Don't propagate signals automatically
		Setpgid: true,
	}

	LogInfo("Linux namespace isolation active (NEWNS|NEWPID|NEWUSER)")
}

func closeTokenHandle(cmd *exec.Cmd) {}

