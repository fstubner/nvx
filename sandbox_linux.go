//go:build linux
// +build linux

package main

import (
	"os/exec"
	"syscall"
)

// applySandboxIsolation on Linux is handled by the Landlock child path.
func applySandboxIsolation(cmd *exec.Cmd, guestHome string) {
	_ = cmd
	_ = guestHome
}

// applyLinuxNamespaces configures the process with Linux kernel namespaces:
//   - CLONE_NEWNS: Isolate mount namespace (prevents bind-mount escape)
//   - CLONE_NEWUSER: required for unprivileged namespace creation
//
// The PID namespace is deliberately NOT created here. It belongs to the sandbox
// supervisor (see supervisorCloneFlags): the teardown guarantee depends on nvx's
// own process being PID 1, and a second namespace rooted at the target would put
// the target beyond that guarantee.
func applyLinuxNamespaces(cmd *exec.Cmd, guestHome string) {
	cloneFlags := uintptr(0)

	// CLONE_NEWNS: new mount namespace
	cloneFlags |= syscall.CLONE_NEWNS

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

	LogInfo("Linux namespace isolation active (NEWNS|NEWUSER; PID namespace owned by the supervisor)")
}

func closeTokenHandle(cmd *exec.Cmd) {}

