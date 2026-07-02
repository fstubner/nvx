//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

// Landlock ABI constants (linux/landlock.h).
const (
	landlockAccessFSExecute   = 1 << 0
	landlockAccessFSWriteFile = 1 << 1
	landlockAccessFSReadFile  = 1 << 2
	landlockAccessFSWriteDir  = 1 << 3
	landlockAccessFSReadDir   = 1 << 4
	landlockAccessFSRemoveFile = 1 << 5
	landlockAccessFSRemoveDir  = 1 << 6
	landlockAccessFSMakeChar  = 1 << 7
	landlockAccessFSMakeDir   = 1 << 8
	landlockAccessFSMakeReg   = 1 << 9
	landlockAccessFSMakeSock  = 1 << 10
	landlockAccessFSMakeFifo  = 1 << 11
	landlockAccessFSMakeBlock = 1 << 12
	landlockAccessFSMakeSym   = 1 << 13
	landlockAccessFSRefer     = 1 << 14
	landlockAccessFSTruncate  = 1 << 15

	landlockScopeFile = 1

	prSetNoNewPrivs = 38
)

var (
	landlockAccessFull = uint64(
		landlockAccessFSExecute | landlockAccessFSWriteFile | landlockAccessFSReadFile |
			landlockAccessFSWriteDir | landlockAccessFSReadDir |
			landlockAccessFSRemoveFile | landlockAccessFSRemoveDir |
			landlockAccessFSMakeChar | landlockAccessFSMakeDir | landlockAccessFSMakeReg |
			landlockAccessFSMakeSock | landlockAccessFSMakeFifo | landlockAccessFSMakeBlock |
			landlockAccessFSMakeSym | landlockAccessFSRefer | landlockAccessFSTruncate,
	)
	landlockAccessReadExec = uint64(
		landlockAccessFSExecute | landlockAccessFSReadFile | landlockAccessFSReadDir,
	)
)

type landlockRulesetAttr struct {
	handledAccessFs uint64
}

type landlockPathBeneathAttr struct {
	allowedAccess uint64
	parentFd      int32
	reserved      uint32
}

func landlockCall(trap uintptr, a1, a2, a3, a4, a5, a6 uintptr) (uintptr, syscall.Errno) {
	r, _, errno := syscall.Syscall6(trap, a1, a2, a3, a4, a5, a6)
	return r, errno
}

func landlockCreateRuleset(handledAccess uint64) (int, error) {
	attr := landlockRulesetAttr{handledAccessFs: handledAccess}
	fd, errno := landlockCall(
		landlockSyscallCreateRuleset(),
		uintptr(unsafe.Pointer(&attr)),
		unsafe.Sizeof(attr),
		uintptr(landlockScopeFile),
		0, 0, 0,
	)
	if errno != 0 {
		return -1, errno
	}
	return int(fd), nil
}

func landlockAddRule(rulesetFD int, access uint64, path string) error {
	attr := landlockPathBeneathAttr{allowedAccess: access, parentFd: -1}
	pathC, err := syscall.BytePtrFromString(path)
	if err != nil {
		return err
	}
	_, errno := landlockCall(
		landlockSyscallAddRule(),
		uintptr(rulesetFD),
		uintptr(landlockScopeFile),
		uintptr(unsafe.Pointer(&attr)),
		unsafe.Sizeof(attr),
		uintptr(unsafe.Pointer(pathC)),
		0,
	)
	if errno != 0 {
		return errno
	}
	return nil
}

func landlockRestrictSelf(rulesetFD int) error {
	_, errno := landlockCall(
		landlockSyscallRestrictSelf(),
		uintptr(rulesetFD),
		0, 0, 0, 0, 0,
	)
	if errno != 0 {
		return errno
	}
	return nil
}

func prctlSetNoNewPrivs() error {
	_, _, errno := syscall.RawSyscall6(
		prctlSyscall(),
		uintptr(prSetNoNewPrivs),
		1, 0, 0, 0, 0,
	)
	if errno != 0 {
		return errno
	}
	return nil
}

func applyLandlockSandbox(guestHome, workDir, nvxHome string) error {
	if err := prctlSetNoNewPrivs(); err != nil {
		return fmt.Errorf("prctl(NO_NEW_PRIVS): %w", err)
	}

	fd, err := landlockCreateRuleset(landlockAccessFull)
	if err != nil {
		return fmt.Errorf("landlock not supported (kernel 5.13+ required): %w", err)
	}
	defer syscall.Close(fd)

	for _, p := range []string{guestHome, workDir} {
		if p == "" {
			continue
		}
		if err := landlockAddRule(fd, landlockAccessFull, p); err != nil {
			return fmt.Errorf("landlock rule for %q: %w", p, err)
		}
	}

	readOnlyRoots := []string{
		"/usr", "/lib", "/lib64", "/bin", "/sbin", "/etc",
		"/dev/null", "/dev/urandom", "/dev/random", "/dev/zero",
	}
	if nvxHome != "" {
		readOnlyRoots = append(readOnlyRoots, nvxHome)
	}
	for _, root := range readOnlyRoots {
		if _, err := os.Stat(root); err != nil {
			continue
		}
		if err := landlockAddRule(fd, landlockAccessReadExec, root); err != nil {
			return fmt.Errorf("landlock read rule for %q: %w", root, err)
		}
	}

	if err := landlockRestrictSelf(fd); err != nil {
		return fmt.Errorf("landlock_restrict_self: %w", err)
	}
	return nil
}

func runLandlockExecChild(guestHome, workDir, nvxHome, networkMode string, proxyPort int, cmdPath string, args []string) int {
	if err := applyLandlockSandbox(guestHome, workDir, nvxHome); err != nil {
		LogError("Landlock isolation failed: %v", err)
		return 1
	}
	if err := applyLinuxNetworkSeccomp(networkMode, proxyPort); err != nil {
		LogError("Network seccomp failed: %v", err)
		return 1
	}

	cmd := exec.Command(cmdPath, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if workDir != "" {
		cmd.Dir = workDir
	}
	applyLinuxNamespaces(cmd, guestHome)

	LogInfo("Linux Landlock + namespace isolation active")
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		LogError("Sandbox execution failed: %v", err)
		return 1
	}
	return 0
}
