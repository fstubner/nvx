//go:build linux

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

// Landlock ABI constants (linux/landlock.h).
const (
	landlockAccessFSExecute    = 1 << 0
	landlockAccessFSWriteFile  = 1 << 1
	landlockAccessFSReadFile   = 1 << 2
	landlockAccessFSWriteDir   = 1 << 3
	landlockAccessFSReadDir    = 1 << 4
	landlockAccessFSRemoveFile = 1 << 5
	landlockAccessFSRemoveDir  = 1 << 6
	landlockAccessFSMakeChar   = 1 << 7
	landlockAccessFSMakeDir    = 1 << 8
	landlockAccessFSMakeReg    = 1 << 9
	landlockAccessFSMakeSock   = 1 << 10
	landlockAccessFSMakeFifo   = 1 << 11
	landlockAccessFSMakeBlock  = 1 << 12
	landlockAccessFSMakeSym    = 1 << 13
	landlockAccessFSRefer      = 1 << 14
	landlockAccessFSTruncate   = 1 << 15

	// landlock_add_rule rule_type (linux/landlock.h).
	landlockRulePathBeneath = 1
	// flags value for landlock_create_ruleset that requests the ABI version.
	landlockCreateRulesetVersion = 1 << 0
	// O_PATH is stable at 0x200000 on the arches nvx targets (amd64/arm64).
	landlockOPath = 0x200000

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

// landlockABIVersion returns the kernel's Landlock ABI version, or -1 when
// Landlock is unavailable. Per the UAPI, the version query passes a NULL attr,
// size 0, and flags=LANDLOCK_CREATE_RULESET_VERSION.
func landlockABIVersion() int {
	r, errno := landlockCall(
		landlockSyscallCreateRuleset(),
		0, 0,
		uintptr(landlockCreateRulesetVersion),
		0, 0, 0,
	)
	if errno != 0 {
		return -1
	}
	return int(r)
}

// landlockSupportedAccess masks requested access bits down to what the running
// kernel's ABI supports: REFER arrived in ABI v2, TRUNCATE in ABI v3.
// Requesting unsupported bits makes landlock_create_ruleset fail with EINVAL.
func landlockSupportedAccess(requested uint64, abi int) uint64 {
	access := requested
	if abi < 2 {
		access &^= landlockAccessFSRefer
	}
	if abi < 3 {
		access &^= landlockAccessFSTruncate
	}
	return access
}

func landlockCreateRuleset(handledAccess uint64) (int, error) {
	attr := landlockRulesetAttr{handledAccessFs: handledAccess}
	fd, errno := landlockCall(
		landlockSyscallCreateRuleset(),
		uintptr(unsafe.Pointer(&attr)),
		unsafe.Sizeof(attr),
		0, // flags MUST be 0 to create a ruleset (nonzero is only for the ABI-version query)
		0, 0, 0,
	)
	if errno != 0 {
		return -1, errno
	}
	return int(fd), nil
}

func landlockAddRule(rulesetFD int, access uint64, path string) error {
	// PATH_BENEATH rules identify the directory by an open O_PATH descriptor
	// carried inside the attr struct — not by a path string syscall argument.
	parentFd, err := syscall.Open(path, landlockOPath|syscall.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open %q: %w", path, err)
	}
	defer syscall.Close(parentFd)

	attr := landlockPathBeneathAttr{allowedAccess: access, parentFd: int32(parentFd)}
	_, errno := landlockCall(
		landlockSyscallAddRule(),
		uintptr(rulesetFD),
		uintptr(landlockRulePathBeneath),
		uintptr(unsafe.Pointer(&attr)),
		0, // flags MUST be 0
		0, 0,
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

	abi := landlockABIVersion()
	if abi < 1 {
		return fmt.Errorf("landlock not supported (kernel 5.13+ required)")
	}

	writeAccess := landlockSupportedAccess(landlockAccessFull, abi)
	readAccess := landlockSupportedAccess(landlockAccessReadExec, abi)

	fd, err := landlockCreateRuleset(writeAccess)
	if err != nil {
		return fmt.Errorf("landlock_create_ruleset (ABI v%d): %w", abi, err)
	}
	defer syscall.Close(fd)

	for _, p := range []string{guestHome, workDir} {
		if p == "" {
			continue
		}
		if err := landlockAddRule(fd, writeAccess, p); err != nil {
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
		if err := landlockAddRule(fd, readAccess, root); err != nil {
			return fmt.Errorf("landlock read rule for %q: %w", root, err)
		}
	}

	if err := landlockRestrictSelf(fd); err != nil {
		return fmt.Errorf("landlock_restrict_self: %w", err)
	}
	return nil
}

func runLandlockExecChild(guestHome, workDir, nvxHome, networkMode, shimCommand string, proxyPort int, cmdPath string, args []string) int {
	if networkModeRequiresNamespace(networkMode) {
		if err := setupLoopbackNetworkNamespace(); err != nil {
			LogError("Network isolation failed (fail-closed): %v", err)
			return 1
		}
		LogInfo("Linux loopback-only network namespace active")
	}

	var egress *EgressProxy
	if strings.ToLower(networkMode) != "open" {
		policy, err := LoadPolicy(nvxHome)
		if err != nil {
			LogWarn("Failed to load policy: %v", err)
			policy = DefaultPolicy()
		}
		rt := runtimeForShim(shimCommand)
		egress, err = startEgressProxy(context.Background(), policy, rt)
		if err != nil {
			LogError("Egress proxy failed: %v", err)
			return 1
		}
		defer egress.Close()
	}

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
	cmd.Env = applyProxyEnv(os.Environ(), egress)
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
