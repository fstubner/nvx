//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
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

	// The staged copy is named after the build it came from, rather than a fixed
	// "nvx.exe" that each new build has to replace in place.
	//
	// Windows refuses to replace a running executable. With one fixed name, a
	// supervisor still alive from an earlier build -- which happens whenever a
	// contained process hangs, and asynchronous piped stdio still hangs -- made
	// every later contained launch fail at staging with a bare "Access is denied",
	// naming a path the user had no reason to connect to their stuck process. The
	// only cure was finding and killing it by hand. Observed on this machine after
	// a rebuild left a supervisor holding the old copy.
	//
	// Distinct names remove the conflict rather than handling it: builds coexist,
	// nothing is ever replaced, and a leftover copy is inert rather than blocking.
	// Size and modification time identify the build, matching what the previous
	// staleness check compared -- and deliberately not a content hash, which would
	// mean reading ten megabytes on a launch path measured in milliseconds.
	dest := filepath.Join(dir, supervisorNameFor(src))
	if staged, serr := os.Stat(dest); serr == nil && staged.Size() == src.Size() {
		clearLegacySupervisorArtifacts(dir)
		return dest, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}

	// Stage via a per-process temporary name so a concurrent nvx run cannot
	// observe a half-written binary.
	tmp := fmt.Sprintf("%s.%d.tmp", dest, os.Getpid())
	if err := copyFile(self, tmp, 0o700); err != nil {
		return "", fmt.Errorf("stage the sandbox supervisor: %w", err)
	}
	// Carry the source's timestamp across so the name stays stable across runs.
	if err := os.Chtimes(tmp, src.ModTime(), src.ModTime()); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("stage the sandbox supervisor: %w", err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		// A concurrent run staging the identical build can win the race; its copy
		// is byte-for-byte this one. Any other failure is real.
		if staged, serr := os.Stat(dest); serr == nil && staged.Size() == src.Size() {
			return dest, nil
		}
		return "", fmt.Errorf("stage the sandbox supervisor: %w", err)
	}
	clearLegacySupervisorArtifacts(dir)
	return dest, nil
}

// Windows error codes that mean the executable image itself is unusable, as
// opposed to the launch being refused. A staged copy in this state is permanent:
// the name encodes the source's size and timestamp, and the reuse check compares
// size, so a corruption that preserves size -- an antivirus quarantine stub, a
// cloud-sync placeholder, a bad sector -- is invisible to it and every later
// contained launch fails identically with nothing in the product able to clear
// it. Found by an acceptance pass zeroing 512 bytes in place.
const (
	errorBadExeFormat    = 193  // not a valid Win32 application
	errorInvalidImage    = 577  // image hash could not be verified
	errorFileCorrupt     = 1392 // the file or directory is corrupted and unreadable
	errorDiskCorrupt     = 1393 // the disk structure is corrupted and unreadable
	errorFileInvalidCode = 1006 // the volume for a file has been externally altered
)

// stagedImageIsUnusable reports whether err says the staged binary itself is
// broken, which is worth one automatic re-stage. Deliberately narrow: a launch
// refused for permissions or policy must not trigger a silent retry loop.
func stagedImageIsUnusable(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	switch uintptr(errno) {
	case errorBadExeFormat, errorInvalidImage, errorFileCorrupt, errorDiskCorrupt, errorFileInvalidCode:
		return true
	}
	return false
}

// restageSupervisor discards a staged copy and writes it again, for use when the
// copy on disk turned out to be unusable.
func restageSupervisor(nvxHome, staged string) error {
	if err := os.Remove(staged); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("discard the unusable supervisor copy: %w", err)
	}
	_, err := stageAppContainerSupervisor(nvxHome)
	return err
}

// supervisorNameFor derives a per-build filename. Two builds differing in either
// size or timestamp get different names, which is the point.
func supervisorNameFor(src os.FileInfo) string {
	return fmt.Sprintf("nvx-%d-%d.exe", src.Size(), src.ModTime().UnixNano())
}

// clearLegacySupervisorArtifacts removes what per-build naming replaced: the old
// fixed-name copy, and temporary files a killed run left behind. It deliberately
// does NOT touch other builds' copies.
//
// Pruning other builds used to happen here, on every launch, which made the
// "builds coexist" claim false: it kept exactly the copy the running process
// wanted and deleted the rest, so alternating a release and a dev build re-copied
// ten megabytes each time. Worse, it reintroduced a narrower version of the race
// this naming scheme exists to remove -- a second nvx of a different build could
// delete the first's copy in the window between staging it and executing it.
//
// Reclaiming that disk is `nvx cleanup`'s job, where nothing is mid-launch.
func clearLegacySupervisorArtifacts(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			continue
		}
		isLegacyFixedName := strings.EqualFold(name, "nvx.exe")
		isLeftoverTemp := strings.HasPrefix(name, "nvx") && strings.HasSuffix(name, ".tmp")
		if !isLegacyFixedName && !isLeftoverTemp {
			continue
		}
		// Failures ignored: a copy that will not delete is one something is still
		// using, which is exactly when it must be left alone.
		_ = os.Remove(filepath.Join(dir, name))
	}
}

// pruneUnusedSupervisors reclaims staged copies from builds other than the one
// running now. Called from `nvx cleanup`, never from a launch, so it cannot race
// a sandbox that is about to execute one.
func pruneUnusedSupervisors(nvxHome string) {
	if nvxHome == "" {
		return
	}
	dir := filepath.Join(nvxHome, "sandbox-exec", "supervisor")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	keep := ""
	if self, serr := os.Executable(); serr == nil {
		if resolved, rerr := filepath.EvalSymlinks(self); rerr == nil {
			self = resolved
		}
		if src, sterr := os.Stat(self); sterr == nil {
			keep = supervisorNameFor(src)
		}
	}

	removed := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || name == keep || !strings.HasPrefix(name, "nvx") {
			continue
		}
		if !strings.HasSuffix(name, ".exe") && !strings.HasSuffix(name, ".tmp") {
			continue
		}
		// A copy another nvx is executing will refuse to delete. That is the
		// correct outcome, not an error worth reporting.
		if err := os.Remove(filepath.Join(dir, name)); err == nil {
			removed++
		}
	}
	if removed > 0 {
		LogInfo("Removed %d unused sandbox supervisor copy(ies).", removed)
	}
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
func runAppContainerExecChild(a supervisorExecArgs) int {
	workDir, networkMode, egressSocket := a.WorkDir, a.NetworkMode, a.EgressSocket
	cmdPath, args := a.CmdPath, a.CmdArgs

	relayCtx, cancelRelay := context.WithCancel(context.Background())
	defer cancelRelay()

	// Publish any exposed ports before the target starts, so a dev server that
	// prints its URL immediately is reachable by the time anyone reads it.
	for _, port := range a.ExposePorts {
		startExposeTunnels(relayCtx, a.GuestHome, port)
	}

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
