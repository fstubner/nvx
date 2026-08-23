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

// applyLinuxNamespaces gives the target its own mount namespace, which is what
// stops a bind mount being used to reach around the Landlock rules.
//
// The PID namespace is deliberately NOT created here. It belongs to the sandbox
// supervisor (see supervisorCloneFlags): the teardown guarantee depends on nvx's
// own process being PID 1, and a second namespace rooted at the target would put
// the target beyond that guarantee.
//
// Neither is the USER namespace, any more. CLONE_NEWUSER here used to come with
// uid/gid mappings, and asking for a mapping makes the Go runtime write
// /proc/<child>/uid_map, gid_map and setgroups from the parent right after the
// clone. By that point the supervisor has already called landlock_restrict_self,
// and the ruleset grants nothing under /proc -- so the kernel refused the write
// and the target never started. Measured on WSL2 Ubuntu 24.04, kernel 6.6:
// mapped user namespace 0/8 launches, mount namespace alone 3/3.
//
// It surfaced as ENOENT rather than EACCES, which is why it read as a missing
// runtime for so long: the supervisor lives in its own PID namespace with the
// host's /proc still mounted, so the child's pid does not name any directory
// there and the open fails before permissions are ever consulted. The same
// launch in the host PID namespace reports "permission denied" instead. Both
// are the one write.
//
// Dropping it costs no containment. The supervisor is already cloned with
// CLONE_NEWUSER (platformLaunchNative), the target inherits that namespace, and
// membership in it is what makes this unprivileged CLONE_NEWNS possible at all.
// A second, nested namespace only handed the target a fresh one of its own to be
// root in.
func applyLinuxNamespaces(cmd *exec.Cmd, guestHome string) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWNS,
		// Don't propagate signals automatically
		Setpgid: true,
	}

	LogInfo("Linux namespace isolation active (NEWNS; user and PID namespaces owned by the supervisor)")
}

func closeTokenHandle(cmd *exec.Cmd) {}

