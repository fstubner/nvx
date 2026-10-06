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

// windowsAncestorGrantPaths lists the system-owned directories the sandbox must
// be able to stat/traverse (npm and other tools walk ancestors up to the drive
// root). Granted this-folder-only, so contents of sibling directories stay
// inaccessible.
func windowsAncestorGrantPaths() []string {
	seen := map[string]bool{}
	var paths []string
	add := func(p string) {
		if p == "" {
			return
		}
		c := filepath.Clean(p)
		key := strings.ToLower(c)
		if !seen[key] {
			seen[key] = true
			paths = append(paths, c)
		}
	}

	sysDrive := os.Getenv("SystemDrive")
	if sysDrive == "" {
		sysDrive = "C:"
	}
	add(sysDrive + `\`)
	add(filepath.Join(sysDrive+`\`, "Users"))
	// The profile root (C:\Users\<user>) already grants ALL APPLICATION PACKAGES,
	// so it needs no grant and is deliberately excluded (its ACL write propagates
	// over the whole profile tree). Cover another volume's roots
	// only if the profile lives off the system drive.
	if up := os.Getenv("USERPROFILE"); up != "" {
		if vol := filepath.VolumeName(up); vol != "" && !strings.EqualFold(vol, sysDrive) {
			add(vol + `\`)
			add(filepath.Join(vol+`\`, "Users"))
		}
	}
	// Every other fixed volume's root too: a project living off the system drive
	// (H:\work\...) makes tools resolve paths up to that root, and without a
	// grant there the stat fails with a bare EPERM on e.g. "H:\". Root only —
	// this-folder-only RX, so the volume's contents stay governed by their own
	// ACLs and nothing below the root becomes readable by this.
	for _, root := range fixedDriveRoots() {
		add(root)
	}
	return paths
}

// setupACLWrite is the permission write setup and --undo make on each path: a
// this-folder-only entry for the identity, or its removal when mask is 0.
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

// grantSidReadExecThisFolder grants the identity read/execute on path alone.
//
// Not time-bounded. A bounded version shipped first and lost the whole grant
// whenever the propagating write behind it was cut short. Measured 2026-09-02:
// an interrupted 1118GB volume carried no entry afterwards. The write is now
// local to the directory, so there is nothing long to bound.
func grantSidReadExecThisFolder(sidStr, path string) error {
	if err := setupACLWrite(path, sidStr, aclMaskReadExec); err != nil {
		return fmt.Errorf("grant read/execute on %s: %w", path, err)
	}
	return nil
}

// runWindowsSetupGrants grants each path that does not already carry the grant,
// and reports how many could not be granted.
//
// The two operations are parameters for the same reason runWindowsSetupUndo's
// are: setup needs an Administrator terminal, so neither the resume path nor the
// failure path can be reached from the gate otherwise -- and they are the two
// that used to be wrong. A grant that failed aborted the whole run, and since the
// volume holding the user's projects was granted last, the grant most likely to
// be lost was the one that mattered.
func runWindowsSetupGrants(paths []string, hasGrant func(string) bool, grant func(string) error) (failed int) {
	for _, p := range paths {
		// Already granted? Say so and move on, so a re-run writes only what is
		// missing.
		if hasGrant(p) {
			LogInfo("Sandbox read and list access on %s is already in place.", p)
			continue
		}
		LogInfo("Granting sandbox read and list access on %s ...", p)
		started := time.Now()
		if err := grant(p); err != nil {
			failed++
			LogError("Failed to grant sandbox read and list access on %s after %s: %v", p, time.Since(started).Round(time.Millisecond), err)
			LogInfo("Continuing with the remaining paths; re-run 'nvx setup' afterwards to retry this one.")
			continue
		}
		LogInfo("Granted %s in %s.", p, time.Since(started).Round(time.Millisecond))
	}
	return failed
}

// windowsSetupGrantPaths lists the ancestor roots a path in this run resolves
// up to, and with allDrives the root of every other fixed volume too.
//
// `nvx setup` passes allDrives. From 2026-09-01 until 2026-10-04 it granted
// only the volumes known to matter, because each grant cost time proportional
// to the size of the volume. The grant no longer walks the volume (see
// setupACLWrite), so there is no cost left to save.
//
// The notices printed after a failed command list only the narrower set. It
// holds the system drive and the volumes of the profile, nvx's own home and
// the working directory, which are the roots this run could have walked to.
//
// windowsAncestorGrantPaths stays the FULL list on purpose: --undo has to take
// back what any older setup granted.
func windowsSetupGrantPaths(nvxHome, workDir string, allDrives bool) (grant []string) {
	seen := map[string]bool{}
	add := func(p string) {
		if p == "" {
			return
		}
		c := filepath.Clean(p)
		key := strings.ToLower(c)
		if !seen[key] {
			seen[key] = true
			grant = append(grant, c)
		}
	}

	sysDrive := os.Getenv("SystemDrive")
	if sysDrive == "" {
		sysDrive = "C:"
	}
	add(sysDrive + `\`)
	add(filepath.Join(sysDrive+`\`, "Users"))

	// The volumes a real path on this machine resolves up to. USERPROFILE and
	// NVX_HOME are where npx stages, and workDir is where the person running
	// setup actually works -- which is the volume the old behaviour reached last,
	// behind every volume that did not need it.
	for _, p := range []string{os.Getenv("USERPROFILE"), nvxHome, workDir} {
		vol := filepath.VolumeName(p)
		if vol == "" {
			continue
		}
		add(vol + `\`)
		if strings.EqualFold(vol, sysDrive) {
			continue
		}
		// Only if it is really there. The system drive always has one; another
		// volume may not, and granting a path that does not exist fails -- which
		// would count as a failure and take a healthy setup to a non-zero exit for
		// a directory nothing was ever going to look in.
		users := filepath.Join(vol+`\`, "Users")
		if info, err := os.Stat(users); err == nil && info.IsDir() {
			add(users)
		}
	}

	if allDrives {
		for _, root := range fixedDriveRoots() {
			add(root)
		}
	}
	return grant
}

// windowsSetupPaths is every path `nvx setup` grants: the ones a real path
// resolves up to, and the root of every fixed volume.
func windowsSetupPaths(nvxHome, workDir string) []string {
	return windowsSetupGrantPaths(nvxHome, workDir, true)
}

// undoRevokeTimeout bounds each revoke `nvx setup --undo` performs. A variable
// so a test can shorten it.
var undoRevokeTimeout = directGrantTimeout

func revokeSidGrant(sidStr, path string) error {
	// Time-boxed like every grant. The undo swept every ancestor path and the
	// profile root through an unbounded DACL write; on the profile root that
	// write propagates over the whole tree, so `--undo` after a setup on a
	// large profile appeared to hang, with nothing to say which path. A revoke
	// that does not finish in time is now reported by name and counted as a
	// failure, which the caller already turns into a non-zero exit and "remove
	// the entries named above by hand".
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

// runWindowsSetup performs the optional elevated setup that grants the
// AppContainer sandbox stat access on drive roots, for tools that resolve paths
// that far up. It is idempotent and reversible via --undo.
//
// It is no longer needed for egress. Until 0.5.0 this was also where the loopback
// exemption was registered, without which the sandbox could not reach the egress
// proxy at all -- so allowlisted egress was an elevated opt-in and the default was
// an unrestricted direct connection. The in-container relay reaches the proxy over
// a UNIX socket instead, which needs no exemption and no elevation.
func runWindowsSetup(nvxHome string, undo bool) int {
	if !isElevated() {
		LogError("nvx setup must run from an elevated (Administrator) terminal.")
		LogInfo("It grants the nvx sandbox read and list access on drive roots for tools that need it. Egress is allowlisted either way. Undo later with: nvx setup --undo")
		return 1
	}

	LogInfo("Preparing the nvx sandbox identity ...")

	// Granted to a CAPABILITY, not to an AppContainer package.
	//
	// This used to grant the package SID, which worked only while every sandbox on
	// the machine shared one package. Packages are per-project now -- that is what
	// stops one sandbox reaching another's loopback listeners -- so a grant made
	// here could not name them; they do not exist until a project is first run.
	// Every launch carries this capability, so one elevated grant still covers all
	// of them.
	sidStr, err := deriveCapabilitySIDString(setupCapabilityName)
	if err != nil {
		LogError("Could not derive the nvx sandbox capability: %v", err)
		return 1
	}

	// The package identity older versions granted. Nothing launches under it any
	// more, but --undo has to be able to take back what an older setup gave, so it
	// is derived here for the revoke sweep below and for nothing else. It is
	// derived without registering the profile, which used to leave a profile
	// behind after --undo.
	legacySidStr, _ := deriveAppContainerSIDString(stableSandboxProfile)

	if undo {
		return runWindowsSetupUndo(nvxHome, sidStr, legacySidStr,
			revokeSidGrant, setLoopbackExempt, clearWindowsSetupState)
	}

	workDir, _ := os.Getwd()
	paths := windowsSetupPaths(nvxHome, workDir)
	failed := runWindowsSetupGrants(paths,
		func(p string) bool { return appContainerHasGrantFor(sidStr, p, grantReadExec) },
		func(p string) error { return grantSidReadExecThisFolder(sidStr, p) })
	// Setup used to register a loopback exemption here, because reaching the egress
	// proxy meant dialling a listener OUTSIDE the container -- which Windows blocks
	// for AppContainers without one. The in-container relay removed that need: the
	// proxy is reached over a UNIX socket and re-exposed on loopback inside the
	// container, where no exemption applies.
	//
	// So the exemption is now a permission granted for no remaining reason -- it
	// lets the sandbox reach every other loopback listener on the machine. Remove
	// it, including for users who ran an earlier setup. On a machine that never
	// had it, CheckNetIsolation reports nothing to delete.
	exemptionLeft := legacySidStr != "" &&
		!removeLegacyLoopbackExemption(legacySidStr, setLoopbackExempt, listLoopbackExemptSIDs)
	if err := writeWindowsSetupState(nvxHome, windowsSetupState{
		AppContainerSID: sidStr,
		GrantedPaths:    paths,
		LoopbackExempt:  false,
	}); err != nil {
		LogWarn("Setup applied, but recording state failed: %v", err)
	}

	if failed > 0 {
		LogError("nvx sandbox setup did not finish: %d path(s) above could not be granted.", failed)
		LogInfo("Re-run 'nvx setup' (elevated). Anything already in place is skipped, so it resumes rather than starting over.")
		return 1
	}
	if exemptionLeft {
		LogError("nvx sandbox setup did not finish: the loopback exemption above is still registered.")
		return 1
	}

	LogSuccess("nvx sandbox setup complete.")
	LogInfo("Drive-root access granted, for tools that resolve paths that far. Undo with: nvx setup --undo (elevated).")
	LogInfo("Egress is allowlisted with or without this step; setup is not required for it.")
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

// windowsSetupUndoPaths is every path --undo revokes.
//
// The fixed ancestor list alone missed what setup grants from where it runs:
// the Users directory on the nvx home's or working directory's volume, and a
// working directory on a volume that is not fixed. The paths the last setup
// recorded cover those. The ones this directory would grant are added too, so
// an undo run where setup ran works without the record.
func windowsSetupUndoPaths(nvxHome, workDir string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p == "" {
			return
		}
		c := filepath.Clean(p)
		if key := strings.ToLower(c); !seen[key] {
			seen[key] = true
			out = append(out, c)
		}
	}
	for _, p := range windowsAncestorGrantPaths() {
		add(p)
	}
	add(os.Getenv("USERPROFILE"))
	grant := windowsSetupGrantPaths(nvxHome, workDir, false)
	for _, p := range grant {
		add(p)
	}
	if st, ok := readWindowsSetupState(nvxHome); ok {
		for _, p := range st.GrantedPaths {
			// A volume that is not attached now cannot be revoked now. Named, so
			// the user knows the record is about to be cleared without it.
			if _, err := os.Stat(p); err != nil {
				LogInfo("Skipped %s, which setup granted but is not present now.", p)
				continue
			}
			add(p)
		}
	}
	return out
}

// runWindowsSetupUndo takes back what setup granted, and reports whether it
// managed to.
//
// The three operations are parameters so this can be tested with them failing.
// Without that the counting below is unverifiable: undo needs an Administrator
// terminal, so the path where a revoke fails cannot be exercised in the gate at
// all, and it is exactly the path that used to print a tick regardless.
func runWindowsSetupUndo(
	nvxHome, sidStr, legacySidStr string,
	revokeGrant func(sid, path string) error,
	setExempt func(bool, string) error,
	clearState func(string) error,
) int {
	// The profile root is deliberately excluded from the GRANT sweep (its ACL
	// write propagates over the whole profile tree and cannot finish in any budget
	// nvx would accept), but earlier versions did grant it, and README/SECURITY.md
	// tell users --undo removes it. Revoking is cheap where nothing was granted,
	// so sweep it here even though it is not granted here.
	//
	// Every failure below is counted, and any of them makes this command fail.
	//
	// It used to warn on each one and then print "nvx sandbox setup removed."
	// at exit 0 regardless. The loopback exemption is the worst of them to be
	// wrong about: while it is registered, this codebase's own words are that
	// the egress allowlist is bypassable -- so a user could run --undo, see a
	// tick, and still be exempt. That is the same fail-open already closed for
	// `grants reset --all`, which returns 1 when it leaves a record behind.
	//
	// Reported per item as well as counted, because "3 things could not be
	// removed" without saying which leaves the user no way to finish the job by
	// hand.
	failures := 0
	workDir, _ := os.Getwd()
	for _, p := range windowsSetupUndoPaths(nvxHome, workDir) {
		if err := revokeGrant(sidStr, p); err != nil {
			LogWarn("Could not remove grant on %s: %v", p, err)
			failures++
		}
		// Anyone who ran an older setup has the grant on the package identity
		// instead. Removing only the capability would leave that one behind,
		// and --undo is documented as removing what setup added.
		if legacySidStr != "" {
			if err := revokeGrant(legacySidStr, p); err != nil {
				LogWarn("Could not remove the older grant on %s: %v", p, err)
				failures++
			}
		}
	}
	if legacySidStr != "" {
		if err := setExempt(false, legacySidStr); err != nil {
			LogWarn("Could not remove loopback exemption: %v", err)
			LogWarn("While it is registered the egress allowlist can be bypassed through any reachable loopback service.")
			failures++
		}
	}
	if err := clearState(nvxHome); err != nil {
		LogWarn("Could not clear setup state: %v", err)
		failures++
	}
	if failures > 0 {
		LogError("nvx sandbox setup was NOT fully removed: %d item(s) above could not be undone.", failures)
		LogInfo("Re-run in an Administrator terminal, or remove the entries named above by hand.")
		return 1
	}
	LogSuccess("nvx sandbox setup removed.")
	return 0
}
