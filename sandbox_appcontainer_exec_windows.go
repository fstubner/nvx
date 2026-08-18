//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// stageAppContainerSupervisor puts a copy of the running nvx binary somewhere the
// AppContainer can be granted read+execute on, and returns its path.
//
// A copy rather than a grant on the original because nvx can be installed
// anywhere -- Program Files, a build tree, a CI tool cache -- and an unelevated
// icacls cannot necessarily write that ACL. Staging under nvxHome is a path nvx
// owns by construction.
//
// Not ensureAppContainerCommand, which is the equivalent for target binaries:
// that stages the whole containing DIRECTORY, and nvx.exe's directory is often
// nvxHome itself. Copying that would hand the sandbox read access to the grant
// store, the policy baseline and every persistent tool profile -- the exact
// directories the Landlock rules go out of their way to exclude.
//
// The copy is skipped when an up-to-date one is already staged, so the cost is
// paid once per nvx build rather than once per sandboxed command.
func stageAppContainerSupervisor(nvxHome string) (string, error) {
	if nvxHome == "" {
		return "", fmt.Errorf("no nvx home directory")
	}
	self, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate the nvx executable: %w", err)
	}
	if resolved, rerr := filepath.EvalSymlinks(self); rerr == nil {
		self = resolved
	}
	src, err := os.Stat(self)
	if err != nil {
		return "", fmt.Errorf("stat the nvx executable: %w", err)
	}

	dir := filepath.Join(nvxHome, "sandbox-exec", "supervisor")
	dest := filepath.Join(dir, "nvx.exe")
	if supervisorIsCurrent(dest, src) {
		return dest, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}

	// Stage via a per-process temporary name so a concurrent nvx run cannot
	// observe a half-written binary, and so replacing it never has to write
	// through a copy another sandbox is currently executing.
	tmp := fmt.Sprintf("%s.%d.tmp", dest, os.Getpid())
	if err := copyFile(self, tmp, 0o700); err != nil {
		return "", fmt.Errorf("stage the sandbox supervisor: %w", err)
	}
	// Carry the source's timestamp across so the staleness check above is exact.
	if err := os.Chtimes(tmp, src.ModTime(), src.ModTime()); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("stage the sandbox supervisor: %w", err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		// A concurrent run may have staged the same binary and be executing it,
		// which on Windows blocks the replace. That copy is as good as this one.
		if supervisorIsCurrent(dest, src) {
			return dest, nil
		}
		return "", fmt.Errorf("stage the sandbox supervisor: %w", err)
	}
	return dest, nil
}

func supervisorIsCurrent(dest string, src os.FileInfo) bool {
	staged, err := os.Stat(dest)
	if err != nil {
		return false
	}
	return staged.Size() == src.Size() && staged.ModTime().Equal(src.ModTime())
}

// runAppContainerExecChild is the supervisor nvx runs INSIDE the AppContainer,
// mirroring runLandlockExecChild on Linux. The parent launches
// `nvx __appcontainer-exec ... -- <cmd> <args>` into the container; this then
// hosts the egress relay and spawns the real target, which inherits the container
// from its parent's token.
//
// The extra process exists because of where the relay has to live. The parent's
// egress proxy sits outside the container, on a UNIX socket the container can
// reach; but npm, node and every other tool take only host:port in HTTP_PROXY.
// So something inside the container must own a loopback listener that forwards to
// that socket, and only a process already inside can bind an address the target
// is allowed to dial -- Windows blocks AppContainer loopback to anything outside
// the container, but permits it within.
//
// Isolation itself is not applied here, unlike the Landlock supervisor: the
// AppContainer was applied by the parent's CreateProcess call and is inherited,
// so by the time this runs the containment is already in force.
func runAppContainerExecChild(workDir, networkMode, egressSocket, cmdPath string, args []string) int {
	relayCtx, cancelRelay := context.WithCancel(context.Background())
	defer cancelRelay()

	var proxyEnvAddr string
	if egressSocket != "" && windowsEgressNeedsRelay(networkMode) {
		addr, stop, err := startProxyRelay(relayCtx, egressSocket)
		if err != nil {
			// Fail closed. Continuing would leave the target with no proxy and no
			// network, which reads as a broken install rather than a blocked one.
			LogError("Egress relay failed (fail-closed): %v", err)
			return 1
		}
		defer stop()
		proxyEnvAddr = addr
	}

	cmd := exec.Command(cmdPath, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// The parent set HTTP_PROXY to its own TCP listener, which is unreachable from
	// in here. Point it at the relay instead; applyRelayProxyEnv strips the
	// inherited values first, so nothing can fall back to the unreachable address.
	cmd.Env = applyRelayProxyEnv(os.Environ(), proxyEnvAddr)
	if workDir != "" {
		cmd.Dir = workDir
	}

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		LogError("Sandbox execution failed: %v", err)
		return 1
	}
	return 0
}
