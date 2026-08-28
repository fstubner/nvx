//go:build linux

package main

import (
	"os"
	"os/exec"
	"path/filepath"
)

// egressSocketName is the UNIX socket, inside the guest home, that the parent's
// egress proxy also listens on. The guest home already carries full Landlock
// read/write rights, so no extra rule is needed to reach it.
const egressSocketName = ".nvx-egress.sock"

// prepareEgressSocket exposes the parent's egress proxy on a UNIX socket so
// the contained process can reach it from inside its network namespace.
//
// A loopback-only netns has no route to any allowlisted host, so the proxy cannot
// live inside it. UNIX sockets are filesystem objects and are not namespaced by
// the network namespace, which makes them the one channel that crosses cleanly.
func prepareEgressSocket(egress *EgressProxy, guestHome string, netCtx *NetworkLaunchContext) error {
	if egress == nil || netCtx == nil || guestHome == "" {
		return nil
	}
	if !networkModeRequiresNamespace(netCtx.Mode) {
		return nil // no namespace, so the loopback TCP listeners are reachable as-is
	}
	sock := filepath.Join(guestHome, egressSocketName)
	if err := egress.ListenUnix(sock); err != nil {
		return err
	}
	netCtx.EgressSocketPath = sock
	return nil
}

// platformLaunchNative re-execs nvx as a Landlock child so restrictions are
// applied in the process that runs the target command.
func platformLaunchNative(config SandboxConfig, guestHome, workDir, cmdPath string, cleanEnv []string, netCtx NetworkLaunchContext) int {
	exe, err := os.Executable()
	if err != nil {
		LogError("Failed to resolve nvx executable: %v", err)
		return 1
	}

	args := []string{
		"__landlock-exec",
		"--guest-home=" + guestHome,
		"--work-dir=" + workDir,
		"--nvx-home=" + config.NvxHome,
		"--network-mode=" + netCtx.Mode,
		"--command=" + config.Command,
		"--egress-socket=" + netCtx.EgressSocketPath,
	}
	for _, root := range config.ReadExecRoots {
		args = append(args, "--read-exec="+root)
		// Said on Linux as well as Windows. Granting a contained process the right
		// to execute something from outside every default root is worth one line
		// of output, and its absence is how a policy entry that silently did
		// nothing would go unnoticed.
		LogInfo("Sandbox may read and execute from %s", root)
	}
	args = append(args, "--", cmdPath)
	args = append(args, config.Args...)

	cmd := exec.Command(exe, args...)
	cmd.Env = cleanEnv
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if workDir != "" {
		cmd.Dir = workDir
	}

	// Create the namespaces here, as clone flags, rather than having the child
	// unshare itself.
	//
	// unshare(CLONE_NEWNET) moves only the CALLING THREAD, and the Go runtime
	// schedules goroutines across threads freely -- so a self-unsharing child ends
	// up with some of its threads (and anything they open) still in the original
	// namespace. Measured: after an in-process unshare, 52 of 64 goroutines were
	// still in the old namespace and one reached the public internet. Supplying the
	// flag at clone time puts the whole child process in the new namespace from
	// birth, which is deterministic and needs no thread pinning.
	// CLONE_NEWUSER, with this user mapped to root inside it, is what makes the
	// rest of these flags possible without privileges.
	//
	// CLONE_NEWPID and CLONE_NEWNET both require CAP_SYS_ADMIN in the current
	// user namespace. Without CLONE_NEWUSER an ordinary user has none, so the
	// clone failed with EPERM and nvx fail-closed -- the Linux sandbox could not
	// start at all except as root. Measured on WSL2 Ubuntu 24.04 and on a hosted
	// Ubuntu runner: NEWPID|NEWNET is refused, NEWUSER|NEWPID|NEWNET succeeds,
	// for the same user in the same shell.
	//
	// It went unnoticed because every Linux smoke script skips when a namespace
	// cannot be created, so the one condition that proved the sandbox unusable
	// was also the condition that stopped anything checking it.
	//
	// The target-side clone in applyLinuxNamespaces has always done this; only
	// the supervisor's was missing it.
	cmd.SysProcAttr = supervisorSysProcAttr(netCtx.Mode)

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		LogError("Landlock sandbox execution failed: %v", err)
		return 1
	}
	return 0
}
