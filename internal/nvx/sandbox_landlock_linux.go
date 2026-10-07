//go:build linux

package nvx

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
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
	// connect() and addressed sendmsg() to a pathname UNIX socket created
	// outside the Landlock domain. ABI v9.
	landlockAccessFSResolveUnix = 1 << 16

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
// RESOLVE_UNIX (ABI v9) is handled, and only the writable roots grant it. The
// sockets nvx itself provides (egress, --connect, loopback) all live in the
// guest home, a writable root, so they stay reachable. The supervisor's relays
// dial them from threads Landlock never restricted in any case, because
// landlock_restrict_self applies to the calling thread alone. This is a second
// layer under the sandbox's own filesystem view (enterSandboxRoot), which
// already hides host sockets on kernels without v9.
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
	// v6 to v8 added no filesystem rights.
	if abi >= 9 {
		handled |= landlockAccessFSResolveUnix
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
	return landlockAddRuleFD(rulesetFD, access, parentFD)
}

// landlockAddRuleFD adds the rule for a directory the caller has already opened.
func landlockAddRuleFD(rulesetFD int, access uint64, parentFD int) error {
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
func landlockReadOnlyRules(nvxHome string, privateProc bool) []landlockRule {
	paths := []string{
		"/usr", "/lib", "/lib64", "/bin", "/sbin", "/etc",
		"/dev/null", "/dev/urandom", "/dev/random", "/dev/zero",
	}
	// Only when the sandbox has a procfs of its own. Without the private mount
	// this path is the HOST's /proc, and granting it would hand contained code
	// every process on the machine -- cmdline for all of them, environ for the
	// user's own, which is where credentials are. See mountPrivateProc for what
	// needs /proc and why the grant travels with the mount rather than alone.
	if privateProc {
		paths = append(paths, "/proc")
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
		// The current symlink is resolved at rule-add time.
		paths = append(paths, sandboxRuntimeReadRoots(nvxHome)...)
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
		if p == "/dev/null" {
			// Writable, or the commonest idiom for discarding output fails:
			// `cmd >/dev/null` in any shell, and every spawn Node makes with
			// stdio 'ignore', which opens /dev/null for writing. Measured on
			// Linux 6.18 inside the sandbox before this: both EACCES. Writing
			// here discards the bytes, so it reaches nothing.
			access |= landlockAccessFSWriteFile
		}
		rules = append(rules, landlockRule{path: p, access: access})
	}
	return rules
}

func applyLandlockSandbox(guestHome, workDir, nvxHome string, readExecRoots []string, privateProc bool) error {
	return applyLandlockSandboxForABI(landlockABIVersion(), guestHome, workDir, nvxHome, readExecRoots, privateProc)
}

// applyLandlockSandboxForABI applies the ruleset a kernel speaking the given ABI
// would get. Split from applyLandlockSandbox so a test can apply the v1 ruleset
// -- what a 5.13 kernel produces -- on whatever kernel actually runs the tests,
// and check it still contains. Without this seam the older-kernel path could
// only be believed, never run: CI's kernel accepts the full mask.
func applyLandlockSandboxForABI(abi int, guestHome, workDir, nvxHome string, readExecRoots []string, privateProc bool) error {
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

	// The sandbox's /tmp is the guest home's tmp directory shown a second time (see
	// sandboxTmpDir). Landlock walks up from a file by mount, so the guest home's
	// rule does not reach it by that path and the directory needs a rule of its
	// own. Skipped when there is none, as in a test that has no guest skeleton.
	// Opened without following a link, as the bind was.
	if tmp := sandboxTmpDir(guestHome); tmp != "" {
		tmpFD, err := openDirNoFollow(tmp)
		if err != nil {
			return fmt.Errorf("landlock rule for %q: %w", tmp, err)
		}
		err = landlockAddRuleFD(fd, handled, tmpFD)
		_ = syscall.Close(tmpFD)
		if err != nil {
			return fmt.Errorf("landlock rule for %q: %w", tmp, err)
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

	for _, rule := range landlockReadOnlyRules(nvxHome, privateProc) {
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
	// no_new_privs, landlock_restrict_self and unshare(CLONE_NEWNS) below each
	// apply to the calling OS thread only, and the fork in cmd.Start inherits
	// from whichever thread it runs on. Unlocked, the goroutine can move between
	// those calls and the target can be forked from a thread none of them touched.
	// Measured 2026-09-23 on Linux 6.18 with the same sequence in a standalone
	// program: 164 of 300 children created a file Landlock should have refused
	// when work separated restricting from forking, 1 of 300 with nothing in
	// between, and 0 of 300 either way with this lock. Never unlocked: the
	// process exits when this returns.
	runtime.LockOSThread()

	// nvx asks for an interrupt with supervisorInterruptSignal. Registered before
	// the setup below, so a request that lands during it waits in the channel and
	// reaches the target once it runs. The other signals are registered just
	// before the target starts, as they were.
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, supervisorInterruptSignal)

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

	// The in-sandbox half of --connect, on the same relay pattern and for the
	// same reason: the service is outside this namespace, and a UNIX socket is
	// what crosses. Started before the sandbox closes around this process, so a
	// listener that cannot bind stops the run rather than leaving the tool to
	// discover it.
	if len(a.ConnectPorts) > 0 {
		stopConnect, cerr := startContainedConnectListeners(relayCtx, guestHome, a.ConnectPorts)
		if cerr != nil {
			LogError("Could not open the sandbox's path to a host service: %v", cerr)
			return 1
		}
		defer stopConnect()
	}

	// The in-sandbox half of --expose. It dials out to the parent's socket and
	// parks, so it needs nothing from the namespace but its own loopback. Started
	// before the sandbox closes around this process, like the relays above.
	for _, port := range a.ExposePorts {
		startExposeTunnels(relayCtx, guestHome, port)
	}

	// network.mode loopback: every loopback TCP connection goes to the host's
	// service of that name, rather than to this namespace's empty loopback.
	//
	// nvx's own listeners are excluded, so the egress proxy and any --connect
	// port keep reaching what they were built to reach instead of taking a hop
	// through this.
	//
	// A failure here warns and carries on. Without the rules the sandbox keeps
	// the reach it had before they existed -- loopback destinations through the
	// egress proxy -- which is narrower than intended, so there is nothing to
	// fail closed against, and refusing to run would turn a missing iptables into
	// a broken sandbox.
	if loopbackRedirectMode(networkMode) {
		exclude := []int{portOfAddr(proxyEnvAddr)}
		for _, m := range a.ConnectPorts {
			exclude = append(exclude, m.Inside)
		}
		stopRedirect, rerr := startLoopbackRedirect(relayCtx, guestHome, exclude)
		if rerr != nil {
			LogWarn("network.mode loopback cannot redirect this sandbox's loopback traffic (%v).", rerr)
			LogInfo("Services on 127.0.0.1 stay reachable through nvx's proxy, so HTTP and HTTPS still work; a raw connection to a local service does not.")
		} else {
			defer stopRedirect()
			LogDetail("Loopback services on this machine are reachable from the sandbox")
		}
	}

	// A procfs of the sandbox's own, before Landlock restricts this process.
	// Bun cannot run a script or an install without /proc; the grant below is
	// made only if this succeeds, because the alternative is granting the host's
	// -- see mountPrivateProc.
	mountNSErr := enterPrivateMountNamespace()
	privateProc := mountNSErr == nil
	if privateProc {
		if err := mountProc(); err != nil {
			privateProc = false
			LogWarn("Could not give the sandbox its own /proc (%v); it stays denied, and a runtime that reads it (Bun) will not run contained.", err)
		}
	} else {
		LogWarn("Could not give the sandbox its own /proc (%v); it stays denied, and a runtime that reads it (Bun) will not run contained.", mountNSErr)
	}
	// Stop the target creating a user namespace of its own, which it could
	// otherwise use to escape the run-time .env watcher. Before Landlock, which
	// then keeps /proc read-only. See denyNestedUserNamespaces.
	if err := denyNestedUserNamespaces(); err != nil {
		LogError("Could not stop the sandbox creating nested user namespaces (fail-closed): %v", err)
		return 1
	}
	// The repository's git metadata, read-only, before Landlock refuses mounts.
	if err := mountGitMetadataReadOnly(workDir, mountNSErr); err != nil {
		LogError("Could not make this repository's .git read-only for the sandbox (fail-closed): %v", err)
		return 1
	}
	// The project's dotenv files, unreadable, for the same reason and in the same
	// way.
	launchMask, err := maskDotenvFiles(workDir, guestHome, mountNSErr)
	if err != nil {
		LogError("Could not hide this project's .env files from the sandbox (fail-closed): %v", err)
		return 1
	}
	// The filesystem view, before Landlock for the same reason as /proc. Fail
	// closed. Without it, every UNIX socket on the host is one connect() away on
	// kernels below Landlock ABI v9. See enterSandboxRoot.
	visible := sandboxVisiblePaths(guestHome, workDir, nvxHome, a.ReadExecRoots, privateProc, networkMode)
	plan := sandboxBindPlan(visible)
	if err := enterSandboxRoot(plan, sandboxTmpDir(guestHome)); err != nil {
		LogError("Could not build the sandbox's filesystem view (fail-closed): %v", err)
		return 1
	}
	// The mask for dotenv files that appear during the run, while this thread can
	// still mount. Without it the run goes on with the launch's masks only.
	var watchMask string
	var watchMaskInfo os.FileInfo
	if workDir != "" {
		watchMask, watchMaskInfo, err = createDotenvWatchMask()
		if err != nil {
			warnDotenvWatch(err)
		}
	}
	if err := applyLandlockSandbox(guestHome, workDir, nvxHome, a.ReadExecRoots, privateProc); err != nil {
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
	// Pass nvx's termination on to the target. This process is PID 1 of its
	// namespace, and a signal sent to it from outside only arrives when a handler
	// exists. Without one the Go runtime's default ends this process, and PID 1
	// dying SIGKILLs the target before it can run its own shutdown.
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	if err := cmd.Start(); err != nil {
		LogError("Sandbox execution failed: %v", err)
		return 1
	}
	targetPid := cmd.Process.Pid
	if watchMask != "" {
		startDotenvWatcher(targetPid, workDir, watchMask, []os.FileInfo{launchMask, watchMaskInfo}, plan)
	}
	go func() {
		for sig := range sigs {
			if out, ok := signalForTarget(sig); ok {
				_ = syscall.Kill(targetPid, out)
			}
		}
	}()
	// Not cmd.Wait(): this process is PID 1 of a PID namespace, so orphaned
	// descendants reparent here and only an explicit wait4 loop will reap them.
	// Waiting in two places would race os/exec for the target's exit status.
	return reapUntilChildExits(cmd.Process.Pid)
}

// signalForTarget is what the supervisor passes to the target for a signal it
// received, and whether it passes anything.
//
// A SIGINT here is the terminal's own copy of Ctrl-C. The target shares this
// process's foreground group (see applyLinuxNamespaces), so it has the interrupt
// already, and passing this one on delivers a second. Measured 2026-10-07, a
// contained node counted two SIGINTs for one Ctrl-C when the supervisor passed it
// on. nvx asks for an interrupt it forwards with supervisorInterruptSignal.
func signalForTarget(sig os.Signal) (syscall.Signal, bool) {
	switch sig {
	case syscall.SIGINT:
		return 0, false
	case supervisorInterruptSignal:
		return syscall.SIGINT, true
	}
	s, ok := sig.(syscall.Signal)
	return s, ok
}
