//go:build linux

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

	landlockRulePathBeneath = 1

	prSetNoNewPrivs = 38
	openPathFlag    = 0x200000
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
		0,
		0, 0, 0,
	)
	if errno != 0 {
		return -1, errno
	}
	return int(fd), nil
}

func landlockAddRule(rulesetFD int, access uint64, path string) error {
	parentFD, err := syscall.Open(path, openPathFlag|syscall.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer syscall.Close(parentFD)

	// #nosec G115 -- parentFD is a file descriptor from syscall.Open; the kernel's per-process limit is orders of magnitude below int32
	attr := landlockPathBeneathAttr{allowedAccess: access, parentFd: int32(parentFD)}
	_, errno := landlockCall(
		landlockSyscallAddRule(),
		uintptr(rulesetFD),
		uintptr(landlockRulePathBeneath),
		uintptr(unsafe.Pointer(&attr)),
		0,
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

// landlockRule is one path plus the access mask to grant beneath it.
type landlockRule struct {
	path   string
	access uint64
}

// landlockReadOnlyRules returns the read-only roots the sandbox grants, each
// paired with an access mask valid for that path's inode type. Paths that do not
// exist are skipped.
func landlockReadOnlyRules(nvxHome string) []landlockRule {
	paths := []string{
		"/usr", "/lib", "/lib64", "/bin", "/sbin", "/etc",
		"/dev/null", "/dev/urandom", "/dev/random", "/dev/zero",
	}
	if nvxHome != "" {
		// Grant the runtime trees, NOT all of nvxHome. That directory is nvx's own
		// control plane and credential store, and a contained process needs none
		// of it: tool_home holds credentials a trusted tool persisted (wrangler
		// tokens, gh auth), grants/ is the pin store the entire policy-trust
		// boundary depends on, policy.json is the baseline every project policy is
		// compared against, cache/bin-resolve.json maps command names to absolute
		// paths that nvx later executes *unsandboxed*, and sandbox_home holds other
		// concurrent sessions' guest homes.
		//
		// Landlock is allowlist-only -- there is no deny rule -- so narrowing the
		// grant is the only way to exclude them. The guest home is granted
		// separately with full access, including when it lives under tool_home.
		paths = append(paths,
			filepath.Join(nvxHome, "versions"), // runtimes: read+exec is the point
			filepath.Join(nvxHome, "bin"),      // shims: PATH still resolves nested node/npm here
			filepath.Join(nvxHome, "current"),  // symlink into versions; resolved at rule-add time
		)
	}

	var rules []landlockRule
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		access := landlockAccessReadExec
		if !info.IsDir() {
			// Landlock validates the requested rights against the inode type, so
			// a directory-only right on a non-directory is rejected with EINVAL.
			// Every /dev entry above is a character device, and applyLandlockSandbox
			// treats an add-rule failure as fatal -- so leaving READ_DIR set here
			// killed every Linux sandbox launch on every Linux system.
			access &^= landlockAccessFSReadDir
		}
		rules = append(rules, landlockRule{path: p, access: access})
	}
	return rules
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

	for _, p := range sandboxWritableRoots(guestHome, workDir) {
		if p == "" {
			continue
		}
		if err := landlockAddRule(fd, landlockAccessFull, p); err != nil {
			return fmt.Errorf("landlock rule for %q: %w", p, err)
		}
	}

	for _, rule := range landlockReadOnlyRules(nvxHome) {
		if err := landlockAddRule(fd, rule.access, rule.path); err != nil {
			return fmt.Errorf("landlock read rule for %q: %w", rule.path, err)
		}
	}

	if err := landlockRestrictSelf(fd); err != nil {
		return fmt.Errorf("landlock_restrict_self: %w", err)
	}
	return nil
}

func runLandlockExecChild(guestHome, workDir, nvxHome, networkMode, shimCommand, egressSocket, cmdPath string, args []string) int {
	// The network namespace is created by the parent as a clone flag, so this
	// process is already inside it (see platformLaunchNative for why it is not
	// unshared here). Loopback exists but starts down.
	if networkModeRequiresNamespace(networkMode) {
		if err := bringUpLoopback(); err != nil {
			LogError("Network isolation failed (fail-closed): %v", err)
			return 1
		}
		LogInfo("Linux loopback-only network namespace active")
	}

	// The egress proxy runs in the parent, outside this namespace, because a
	// loopback-only namespace has no route to any allowlisted host. Reach it
	// through a loopback TCP relay that forwards to the parent's UNIX socket.
	relayCtx, cancelRelay := context.WithCancel(context.Background())
	defer cancelRelay()

	var proxyEnvAddr string
	if egressSocket != "" && strings.ToLower(networkMode) != "open" {
		addr, stop, err := startProxyRelay(relayCtx, egressSocket)
		if err != nil {
			LogError("Egress relay failed (fail-closed): %v", err)
			return 1
		}
		defer stop()
		proxyEnvAddr = addr
	}

	if err := applyLandlockSandbox(guestHome, workDir, nvxHome); err != nil {
		LogError("Landlock isolation failed: %v", err)
		return 1
	}
	if err := applyLinuxNetworkSeccomp(networkMode); err != nil {
		LogError("Network seccomp failed: %v", err)
		return 1
	}

	cmd := exec.Command(cmdPath, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = applyRelayProxyEnv(os.Environ(), proxyEnvAddr)
	if workDir != "" {
		cmd.Dir = workDir
	}
	applyLinuxNamespaces(cmd, guestHome)

	LogInfo("Linux Landlock + namespace isolation active")
	if err := cmd.Start(); err != nil {
		LogError("Sandbox execution failed: %v", err)
		return 1
	}
	// Not cmd.Wait(): this process is PID 1 of a PID namespace, so orphaned
	// descendants reparent here and only an explicit wait4 loop will reap them.
	// Waiting in two places would race os/exec for the target's exit status.
	return reapUntilChildExits(cmd.Process.Pid)
}
