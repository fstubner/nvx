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

// Landlock ABI constants, from include/uapi/linux/landlock.h, bit for bit.
//
// Pinned by TestLandlockAccessConstantsMatchTheKernelHeader against the header's
// literal values, because this block once carried an invented WRITE_DIR at bit
// 3 -- the kernel has no such right; bit 3 is READ_DIR -- with every right above
// it shifted up by one. The name "ReadDir" in this package then denoted the
// kernel's REMOVE_DIR, so the read-only mask granted rmdir on the runtime tree
// and never granted listing anywhere. Measured on a 6.18 kernel before the
// fix: a contained process removed a directory under versions/. Tests written
// in terms of the same misnamed constants could not see it.
const (
	landlockAccessFSExecute    = 1 << 0
	landlockAccessFSWriteFile  = 1 << 1
	landlockAccessFSReadFile   = 1 << 2
	landlockAccessFSReadDir    = 1 << 3
	landlockAccessFSRemoveDir  = 1 << 4
	landlockAccessFSRemoveFile = 1 << 5
	landlockAccessFSMakeChar   = 1 << 6
	landlockAccessFSMakeDir    = 1 << 7
	landlockAccessFSMakeReg    = 1 << 8
	landlockAccessFSMakeSock   = 1 << 9
	landlockAccessFSMakeFifo   = 1 << 10
	landlockAccessFSMakeBlock  = 1 << 11
	landlockAccessFSMakeSym    = 1 << 12
	landlockAccessFSRefer      = 1 << 13 // ABI v2, Linux 5.19
	landlockAccessFSTruncate   = 1 << 14 // ABI v3, Linux 6.2
	landlockAccessFSIoctlDev   = 1 << 15 // ABI v5, Linux 6.10

	landlockRulePathBeneath = 1

	// LANDLOCK_CREATE_RULESET_VERSION: with a null attr and zero size, the
	// syscall returns the highest ABI version the kernel supports instead of a
	// ruleset fd.
	landlockCreateRulesetVersion = 1

	prSetNoNewPrivs = 38
	openPathFlag    = 0x200000
)

// landlockAccessReadExec is what a read-only root grants: read files, list
// directories, execute. Never write, remove, create or truncate.
var landlockAccessReadExec = uint64(
	landlockAccessFSExecute | landlockAccessFSReadFile | landlockAccessFSReadDir,
)

// landlockAccessV1 is every filesystem right in the first Landlock ABI (Linux
// 5.13): EXECUTE through MAKE_SYM. Later ABIs add one right each, and
// landlockHandledAccessForABI layers them on.
const landlockAccessV1 = uint64(
	landlockAccessFSExecute | landlockAccessFSWriteFile | landlockAccessFSReadFile |
		landlockAccessFSReadDir | landlockAccessFSRemoveDir | landlockAccessFSRemoveFile |
		landlockAccessFSMakeChar | landlockAccessFSMakeDir | landlockAccessFSMakeReg |
		landlockAccessFSMakeSock | landlockAccessFSMakeFifo | landlockAccessFSMakeBlock |
		landlockAccessFSMakeSym,
)

// landlockABIVersion asks the kernel which Landlock ABI it speaks. 0 means no
// Landlock at all: the syscall is missing, or the LSM is compiled out or not in
// the boot-time LSM list.
func landlockABIVersion() int {
	r, errno := landlockCall(
		landlockSyscallCreateRuleset(),
		0, 0, landlockCreateRulesetVersion,
		0, 0, 0,
	)
	if errno != 0 {
		return 0
	}
	return int(r)
}

// landlockHandledAccessForABI is the set of filesystem rights the sandbox asks
// the kernel to restrict, capped to what that ABI version knows.
//
// The cap is the whole point. A ruleset that names a right the kernel has never
// heard of is refused outright with EINVAL, not trimmed -- and this code used
// to pass every right through IOCTL_DEV unconditionally, so the real floor was
// the kernel that introduced IOCTL_DEV, Linux 6.10, while the error it printed
// on anything older said "5.13+ required". Debian 12 (6.1), RHEL 9 (5.14) and
// Ubuntu 22.04 (5.15) all failed closed with advice pointing at the wrong thing,
// and CI never saw it because the runner's kernel is new enough to take the
// full mask.
//
// A right the kernel does not handle is simply not restricted, which is how
// Landlock is documented to behave on older ABIs. That is the correct answer:
// refusing to run at all is not more secure than running with what the kernel
// offers, and the rights that arrive in later ABIs (linking across directories,
// truncation, device ioctls) are refinements on a boundary the v1 rights
// already draw.
//
// RESOLVE_UNIX (ABI v9) is deliberately never handled. Once handled, connecting
// to a UNIX socket created outside the sandbox needs an explicit rule, and the
// in-container egress relay dials the parent's UNIX socket per connection,
// after landlock_restrict_self. Handling it would cut every contained process
// off from the network on kernels new enough to offer it.
func landlockHandledAccessForABI(abi int) uint64 {
	if abi < 1 {
		return 0
	}
	handled := landlockAccessV1
	if abi >= 2 {
		handled |= landlockAccessFSRefer
	}
	if abi >= 3 {
		handled |= landlockAccessFSTruncate
	}
	// v4 added network rights only.
	if abi >= 5 {
		handled |= landlockAccessFSIoctlDev
	}
	return handled
}

// landlockHandledAccess is landlockHandledAccessForABI for the running kernel.
func landlockHandledAccess() uint64 {
	return landlockHandledAccessForABI(landlockABIVersion())
}

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

func applyLandlockSandbox(guestHome, workDir, nvxHome string, readExecRoots []string) error {
	return applyLandlockSandboxForABI(landlockABIVersion(), guestHome, workDir, nvxHome, readExecRoots)
}

// applyLandlockSandboxForABI applies the ruleset a kernel speaking the given ABI
// would get. Split from applyLandlockSandbox so a test can apply the v1 ruleset
// -- what a 5.13 kernel produces -- on whatever kernel actually runs the tests,
// and check it still contains. Without this seam the older-kernel path could
// only be believed, never run: CI's kernel accepts the full mask.
func applyLandlockSandboxForABI(abi int, guestHome, workDir, nvxHome string, readExecRoots []string) error {
	if err := prctlSetNoNewPrivs(); err != nil {
		return fmt.Errorf("prctl(NO_NEW_PRIVS): %w", err)
	}

	handled := landlockHandledAccessForABI(abi)
	if handled == 0 {
		return fmt.Errorf("landlock not available: the kernel reports no Landlock ABI (Linux 5.13+ with CONFIG_SECURITY_LANDLOCK, and \"landlock\" in the lsm= list, required)")
	}
	fd, err := landlockCreateRuleset(handled)
	if err != nil {
		return fmt.Errorf("landlock_create_ruleset (kernel ABI v%d, handled %#x): %w", abi, handled, err)
	}
	defer syscall.Close(fd)

	// A rule may only grant rights the ruleset handles; anything else is
	// EINVAL. The writable roots get everything the kernel restricts, and the
	// read-only masks below are v1 rights so are always within the handled set,
	// but masking is what makes that true by construction rather than by
	// coincidence.
	for _, p := range sandboxWritableRoots(guestHome, workDir) {
		if p == "" {
			continue
		}
		if err := landlockAddRule(fd, handled, p); err != nil {
			return fmt.Errorf("landlock rule for %q: %w", p, err)
		}
	}

	// Extra read/execute roots from isolation.filesystem.allow_read_exec. Same
	// rights as the system read-only roots below: read, list and execute, never
	// write. A missing path is skipped rather than fatal -- the parent already
	// warned about it, and a stale entry should not stop the command.
	//
	// These roots, `/usr/bin` and `/etc` could not be listed for as long as the
	// access constants were shifted (see the const block): the mask that was
	// meant to carry READ_DIR carried REMOVE_DIR instead. README recorded the
	// symptom as a Landlock limitation. It was this bug.
	for _, root := range readExecRoots {
		info, statErr := os.Stat(root)
		if statErr != nil || !info.IsDir() {
			continue
		}
		if err := landlockAddRule(fd, landlockAccessReadExec&handled, root); err != nil {
			return fmt.Errorf("landlock read/execute rule for %q: %w", root, err)
		}
	}

	for _, rule := range landlockReadOnlyRules(nvxHome) {
		if err := landlockAddRule(fd, rule.access&handled, rule.path); err != nil {
			return fmt.Errorf("landlock read rule for %q: %w", rule.path, err)
		}
	}

	if err := landlockRestrictSelf(fd); err != nil {
		return fmt.Errorf("landlock_restrict_self: %w", err)
	}
	return nil
}

func runLandlockExecChild(a supervisorExecArgs) int {
	guestHome, workDir, nvxHome := a.GuestHome, a.WorkDir, a.NvxHome
	networkMode, egressSocket := a.NetworkMode, a.EgressSocket
	cmdPath, args := a.CmdPath, a.CmdArgs
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

	if err := applyLandlockSandbox(guestHome, workDir, nvxHome, a.ReadExecRoots); err != nil {
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
