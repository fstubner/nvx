//go:build windows

package nvx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// runWinCmd runs a Windows system tool with a timeout so a stuck tool surfaces
// as an error instead of hanging. (An icacls write on a directory with a large
// subtree, such as the profile root, can run for many minutes while Windows
// propagates inheritance beneath it, so every privileged call is time-boxed.)
//
// name is a tool in the system directory -- "icacls", "reg",
// "CheckNetIsolation" -- and is taken from there, never from PATH. This ran
// exec.CommandContext with the bare name, and `nvx setup` calls it elevated:
// an Administrator running whatever a user-writable directory earlier in PATH
// chose to call CheckNetIsolation.exe. See systemToolPath.
func runWinCmd(timeout time.Duration, name string, args ...string) ([]byte, error) {
	tool, err := systemToolPath(name + ".exe")
	if err != nil {
		return nil, err
	}
	var out []byte
	for attempt := 0; ; attempt++ {
		out, err = runWinCmdOnce(timeout, name, tool, args...)
		if err == nil || attempt == winCmdRetries-1 || !processNeverStarted(err) {
			return out, err
		}
		time.Sleep(winCmdRetryPause)
	}
}

// Windows sometimes refuses to create a process at all, briefly.
//
// Measured on a hosted runner 2026-09-09: `fork/exec
// C:\Windows\system32\icacls.exe: The handle is invalid` out of
// prepareAppContainerFilesystem, in the same second that AppContainer launches
// were being refused and two probe children returned no output. The runner was
// momentarily unable to make processes; nothing about nvx or the command was
// wrong, and a plain re-run was clean.
//
// That reached a user as a sandbox refusing to start, because labelLowIntegrity
// is on every contained launch here. nvx already treats this exact error as
// transient when READING the probe child (stageProbeChild retries five times);
// the same error creating a process was fatal. This closes that asymmetry.
//
// Five attempts at 200ms, the same shape as stageProbeChild rather than a second
// invented one.
const winCmdRetries = 5

// winCmdRetryPause is a var so a test can drive the retry without sleeping.
var winCmdRetryPause = 200 * time.Millisecond

// runWinCmdOnce is a single attempt, replaceable so a test can make one fail.
// The condition being handled cannot be produced on demand -- it is a state of
// the machine -- so a seam is the only way the retry is checkable at all.
var runWinCmdOnce = func(timeout time.Duration, name, tool string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, tool, args...).CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("%s timed out after %s", name, timeout)
	}
	return out, err
}

// errorInvalidHandle is ERROR_INVALID_HANDLE, what Windows returns when it
// cannot hand out another handle.
const errorInvalidHandle = 6

// processNeverStarted reports whether err says the command was never launched.
//
// Deliberately narrow, in two ways that matter.
//
// The operation must be "fork/exec", which Go sets only when os.StartProcess
// itself failed. That is what makes a retry safe regardless of what the command
// would have done: it provably did not run, so re-running cannot repeat a side
// effect. Idempotence of icacls and CheckNetIsolation is then a second line of
// defence rather than the argument.
//
// And the code must be ERROR_INVALID_HANDLE, the one transient this has been
// measured producing. A missing executable also fails at fork/exec and must not
// be retried five times before saying so.
//
// Matched on the errno, not on the message. "The handle is invalid" is the
// English text; the same failure on a localised Windows says something else, and
// a check that reads as robust while never firing is worse than no check. The
// exhaustion signatures next door match text because they arrive from several
// layers with no common type -- here there is one.
func processNeverStarted(err error) bool {
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) || pathErr.Op != "fork/exec" {
		return false
	}
	var errno syscall.Errno
	return errors.As(pathErr.Err, &errno) && errno == errorInvalidHandle
}

var procGetTokenInformation = modAdvapi32.NewProc("GetTokenInformation")

var procGetDriveTypeW = modKernel32.NewProc("GetDriveTypeW")

const driveFixed = 3 // DRIVE_FIXED

// fixedDriveRoots returns the root directory of every fixed volume on the
// machine (skipping removable, network, and CD-ROM drives). Projects commonly
// live off the system drive, and a tool that resolves a path walks up to that
// volume's root — a stat an AppContainer cannot perform unless the root itself
// carries a grant, since "bypass traverse checking" does not cover reading the
// root's own attributes.
func fixedDriveRoots() []string {
	var roots []string
	for c := 'A'; c <= 'Z'; c++ {
		root := string(c) + `:\`
		p, err := syscall.UTF16PtrFromString(root)
		if err != nil {
			continue
		}
		t, _, _ := procGetDriveTypeW.Call(uintptr(unsafe.Pointer(p)))
		if t == driveFixed {
			roots = append(roots, root)
		}
	}
	return roots
}

// isElevated reports whether the current process token is elevated (admin).
func isElevated() bool {
	var token syscall.Token
	proc, _, _ := procGetCurrentProcess.Call()
	if r, _, _ := procOpenProcessToken.Call(proc, uintptr(TOKEN_QUERY), uintptr(unsafe.Pointer(&token))); r == 0 {
		return false
	}
	defer syscall.CloseHandle(syscall.Handle(token))

	const tokenElevation = 20 // TokenElevation
	var elevation uint32
	var retLen uint32
	r, _, _ := procGetTokenInformation.Call(
		uintptr(token), tokenElevation,
		uintptr(unsafe.Pointer(&elevation)), unsafe.Sizeof(elevation),
		uintptr(unsafe.Pointer(&retLen)),
	)
	return r != 0 && elevation != 0
}

// setupACLWrite is the permission write `nvx setup` makes when it removes an
// entry: the identity's this-folder-only entry on path, taken away when mask is 0.
//
// It was SetNamedSecurityInfoW, which re-runs Windows' auto-inheritance over
// everything beneath the directory, and a drive root's subtree is the whole
// volume. Measured 2026-09-01: a 932GB volume with 1GB free on a 5400rpm disk
// had not finished after 36 minutes. Measured 2026-10-04: setup --all-drives
// granted D:\ in 1s and E:\ in 3s, and was still on F:\ after 33 minutes. The
// entry is not inheritable, so nothing beneath the root can see it.
// writeThisFolderEntry writes the same list without the walk. Measured
// 2026-10-04 on a temp directory holding 20,000 files: 2.67 to 3.01 s with the
// walk, under 1 ms without, and no descendant's security descriptor changed
// either way.
//
// A variable so a test can stand in a write that stalls.
var setupACLWrite = writeThisFolderEntry

// undoRevokeTimeout bounds each revoke `nvx setup` performs. A variable so a
// test can shorten it.
var undoRevokeTimeout = directGrantTimeout

func revokeSidGrant(sidStr, path string) error {
	// Time-boxed like every grant. A revoke through an unbounded DACL write on
	// the profile root propagates over the whole tree, so it appeared to hang
	// with nothing to say which path. A revoke that does not finish in time is
	// now reported by name and counted as a failure, which the caller turns into
	// a non-zero exit and "remove the entries named above by hand".
	//
	// The write itself is setup's own, which removes a this-folder entry without
	// walking anything beneath it and writes nothing where there is no entry.
	// The bound stays for a filter driver that stalls any write.
	write := setupACLWrite
	if err := aclWriteWithin(path, undoRevokeTimeout, func() error {
		return write(path, sidStr, 0)
	}); err != nil {
		return fmt.Errorf("remove the permission on %s: %w", path, err)
	}
	return nil
}

func setLoopbackExempt(add bool, sidStr string) error {
	flag := "-a"
	if !add {
		flag = "-d"
	}
	out, err := runWinCmd(30*time.Second, "CheckNetIsolation", "LoopbackExempt", flag, "-p="+sidStr)
	if err != nil {
		return fmt.Errorf("CheckNetIsolation LoopbackExempt %s: %v (%s)", flag, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// setupLeftoverPaths lists where an older `nvx setup` could have written an
// entry: the drive root and Users folder of the system drive, the root and Users
// folder of every fixed volume and of the volumes the profile, nvx's home and
// the working directory are on, the profile root itself (the oldest versions
// granted it), and every path the last setup recorded.
//
// absent holds recorded paths that are not present now. A volume that is not
// attached cannot be cleaned now, and the caller names it.
func setupLeftoverPaths(nvxHome, workDir string) (paths, absent []string) {
	seen := map[string]bool{}
	add := func(p string) {
		if p == "" {
			return
		}
		c := filepath.Clean(p)
		if key := strings.ToLower(c); !seen[key] {
			seen[key] = true
			paths = append(paths, c)
		}
	}
	addUsers := func(vol string) {
		users := filepath.Join(vol+`\`, "Users")
		if info, err := os.Stat(users); err == nil && info.IsDir() {
			add(users)
		}
	}

	sysDrive := os.Getenv("SystemDrive")
	if sysDrive == "" {
		sysDrive = "C:"
	}
	add(sysDrive + `\`)
	add(filepath.Join(sysDrive+`\`, "Users"))
	add(os.Getenv("USERPROFILE"))
	for _, p := range []string{os.Getenv("USERPROFILE"), nvxHome, workDir} {
		if vol := filepath.VolumeName(p); vol != "" {
			add(vol + `\`)
			addUsers(vol)
		}
	}
	for _, root := range fixedDriveRoots() {
		add(root)
		addUsers(strings.TrimSuffix(root, `\`))
	}
	if st, ok := readWindowsSetupState(nvxHome); ok {
		for _, p := range st.GrantedPaths {
			if _, err := os.Stat(p); err != nil {
				absent = append(absent, p)
				continue
			}
			add(p)
		}
	}
	return paths, absent
}

// setupEntry is a path an older setup left permissions on, and the identities
// that hold one there.
type setupEntry struct {
	Path string
	SIDs []string
}

// findSetupLeftovers returns every path in paths that carries an entry for one
// of the identities. Reads only, so it needs no elevation.
func findSetupLeftovers(sids, paths []string, hasEntry func(sid, path string) bool) []setupEntry {
	var found []setupEntry
	for _, p := range paths {
		e := setupEntry{Path: p}
		for _, sid := range sids {
			if sid != "" && hasEntry(sid, p) {
				e.SIDs = append(e.SIDs, sid)
			}
		}
		if len(e.SIDs) > 0 {
			found = append(found, e)
		}
	}
	return found
}

// aclHasAnyEntry reports whether path carries an explicit entry for sidStr.
// An unreadable list answers no: there is nothing it can be shown to hold.
func aclHasAnyEntry(sidStr, path string) bool {
	_, ok, err := aclEntryFor(path, sidStr)
	return err == nil && ok
}

// setupIdentities returns the two identities an older setup granted: the
// capability it used from 0.5.1 on, and the shared AppContainer package it used
// before packages became per project. The second is empty if it cannot be
// derived.
//
// The package identity is derived without registering the profile, which used
// to leave a profile behind after --undo.
func setupIdentities() (capSID, legacySID string, err error) {
	capSID, err = deriveCapabilitySIDString(setupCapabilityName)
	if err != nil {
		return "", "", err
	}
	legacySID, _ = deriveAppContainerSIDString(stableSandboxProfile)
	return capSID, legacySID, nil
}

// setupCleanupOps are the machine-touching operations `nvx setup` performs, as
// parameters. Setup needs an Administrator terminal, so a test cannot reach the
// removal or the failure paths through the real ones.
type setupCleanupOps struct {
	elevated   func() bool
	hasEntry   func(sid, path string) bool
	revoke     func(sid, path string) error
	setExempt  func(add bool, sid string) error
	listExempt func() ([]string, error)
	clearState func(nvxHome string) error
}

func realSetupCleanupOps() setupCleanupOps {
	return setupCleanupOps{
		elevated:   isElevated,
		hasEntry:   aclHasAnyEntry,
		revoke:     revokeSidGrant,
		setExempt:  setLoopbackExempt,
		listExempt: listLoopbackExemptSIDs,
		clearState: clearWindowsSetupState,
	}
}

// runWindowsSetup is `nvx setup`: it removes what older nvx versions left on the
// machine, and adds nothing.
//
// Until 2026-10-06 it granted the sandbox read and list access to the root of
// every fixed volume and its Users folder, for tools that resolve a path all the
// way up to a drive root. The preload in sandbox_walkup_shim.js answers those
// stats, and contained npx, pnpm and bun 1.4.2 were measured installing on C:
// with every such grant removed, so nothing needs the grant. Setup is now the
// way to take back what an earlier one wrote.
//
// --undo is accepted and does the same thing, so existing scripts and
// documentation keep working. See parseSetupArgs.
func runWindowsSetup(nvxHome string) int {
	capSID, legacySID, err := setupIdentities()
	if err != nil {
		LogError("Could not derive the nvx sandbox capability: %v", err)
		return 1
	}
	workDir, _ := os.Getwd()
	return runWindowsSetupCleanup(nvxHome, workDir, capSID, legacySID, realSetupCleanupOps())
}

// runWindowsSetupCleanup finds what an older setup left, and removes it when
// the process is elevated.
//
// Finding it needs no elevation, so a machine with nothing to remove is told so
// from any terminal. When something is there and the terminal is not elevated,
// the entries are named before the command stops.
//
// Every failure is counted, and any of them makes this command fail. The
// loopback exemption is the worst one to be wrong about: while it is registered
// the egress allowlist is bypassable, so a tick over a failed removal would
// leave a user exempt who believes they are not. Each is reported by name as
// well, so the user can finish the job by hand.
//
// The profile root is in the paths on purpose. Earlier versions granted it, and
// revoking costs nothing where nothing was granted.
func runWindowsSetupCleanup(nvxHome, workDir, capSID, legacySID string, ops setupCleanupOps) int {
	paths, absent := setupLeftoverPaths(nvxHome, workDir)
	for _, p := range absent {
		LogInfo("Skipped %s, which setup recorded but is not present now.", p)
	}
	entries := findSetupLeftovers([]string{capSID, legacySID}, paths, ops.hasEntry)

	failures := 0
	exemptionFound := false
	if legacySID != "" {
		sids, listErr := ops.listExempt()
		switch {
		case listErr != nil:
			LogWarn("Could not check for a loopback exemption left by an older setup: %v", listErr)
			failures++
		case sidListContains(sids, legacySID):
			exemptionFound = true
		}
	}
	_, statErr := os.Stat(windowsSetupMarkerPath(nvxHome))
	stateFound := statErr == nil

	if len(entries) == 0 && !exemptionFound && !stateFound {
		if failures > 0 {
			LogError("nvx setup could not finish checking: %d item(s) above could not be read.", failures)
			return 1
		}
		LogSuccess("Nothing to remove. An older nvx setup left nothing on this machine.")
		return 0
	}

	if !ops.elevated() {
		LogError("nvx setup must run from an elevated (Administrator) terminal to remove what an older setup left:")
		for _, e := range entries {
			LogInfo("  sandbox access on %s", e.Path)
		}
		if exemptionFound {
			LogInfo("  a loopback exemption")
		}
		if stateFound {
			LogInfo("  the record of an earlier setup")
		}
		return 1
	}

	removed := 0
	for _, e := range entries {
		done := true
		for _, sid := range e.SIDs {
			if err := ops.revoke(sid, e.Path); err != nil {
				LogWarn("Could not remove the sandbox access on %s: %v", e.Path, err)
				failures++
				done = false
			}
		}
		if done {
			removed++
			LogInfo("Removed the sandbox access on %s.", e.Path)
		}
	}
	if exemptionFound {
		if removeLegacyLoopbackExemption(legacySID, ops.setExempt, ops.listExempt) {
			removed++
			LogInfo("Removed the loopback exemption.")
		} else {
			failures++
			LogWarn("While it is registered the egress allowlist can be bypassed through any reachable loopback service.")
		}
	}
	if stateFound {
		if err := ops.clearState(nvxHome); err != nil {
			LogWarn("Could not clear setup state: %v", err)
			failures++
		} else {
			removed++
			LogInfo("Removed the record of an earlier setup.")
		}
	}
	if failures > 0 {
		LogError("nvx setup did not finish: %d item(s) above could not be removed.", failures)
		LogInfo("Re-run in an Administrator terminal, or remove the entries named above by hand.")
		return 1
	}
	LogSuccess("Removed %d item(s) an older nvx setup left. nvx does not need any of them.", removed)
	return 0
}

// removeLegacyLoopbackExemption removes the loopback exemption older setups
// registered for the shared package, and reports whether it is gone.
//
// A failed delete is ambiguous. It is what a machine that never had the
// exemption returns, and it is also what a timeout or a missing tool returns.
// Setup used to read every failure as the first case and say there was nothing
// to remove. The exemption list settles it.
func removeLegacyLoopbackExemption(legacySid string,
	setExempt func(bool, string) error, list func() ([]string, error)) bool {
	delErr := setExempt(false, legacySid)
	sids, listErr := list()
	switch {
	case listErr == nil && sidListContains(sids, legacySid):
		LogWarn("The loopback exemption for %s is still registered: %v", legacySid, delErr)
		LogWarn("Remove it from an Administrator terminal: CheckNetIsolation LoopbackExempt -d -p=%s", legacySid)
		return false
	case listErr != nil && delErr != nil:
		LogWarn("Could not remove or check the loopback exemption for %s: %v; %v", legacySid, delErr, listErr)
		return false
	case delErr != nil:
		LogInfo("No loopback exemption to remove (the sandbox no longer needs one).")
	}
	return true
}
