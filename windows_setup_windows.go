//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// runWinCmd runs an external command with a timeout so a stuck tool surfaces as
// an error instead of hanging. (icacls can hang indefinitely when a filter
// driver intercepts writes to certain paths, e.g. the OneDrive/Defender-guarded
// profile root — so every privileged call is time-boxed.)
func runWinCmd(timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("%s timed out after %s", name, timeout)
	}
	return out, err
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
	// so it needs no grant and is deliberately excluded (its ACL write hangs
	// behind the OneDrive/Defender filter driver). Cover another volume's roots
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

// setupProgressEvery is how often a running grant says it is still running.
//
// A permission write on a drive root can take many minutes -- the write itself is
// tiny, but SetNamedSecurityInfoW re-runs Windows' auto-inheritance over
// everything beneath the directory, linear in the number of entries (measured in
// sandbox_ancestor_skip_windows.go: 773ms at 5000, 3.1s at 20000), and a drive
// root's subtree is the whole volume. Measured 2026-09-01: a 932GB volume with
// 1GB free on a 5400rpm disk had not finished after 36 minutes.
//
// Saying so periodically is the difference between "slow" and "hung", and it is
// what setup owes anyone deciding whether to keep waiting.
const setupProgressEvery = 30 * time.Second

// grantSidReadExecThisFolder grants the identity read/execute on path alone, and
// keeps saying it is still working until it finishes.
//
// NOT time-bounded, and that is a correction rather than an oversight. A bounded
// version shipped first, on the reasoning that an abandoned write lands anyway --
// which is true of the runtime ancestor walk, whose process keeps running, and
// false here. Measured 2026-09-02: setup was interrupted part-way through a
// 1118GB volume and the root carried NO entry for the identity afterwards. The
// write does not commit until the propagation behind it finishes, so ending the
// process early loses the whole thing.
//
// A deadline therefore could not make this safer; it could only guarantee that a
// volume needing longer than the deadline was never granted, after burning the
// deadline on every attempt. What was actually wrong was paying this cost for
// volumes nothing uses, and that is fixed in windowsSetupGrantPaths. What is left
// is a genuinely long operation, so it reports progress and says plainly what
// interrupting costs.
func grantSidReadExecThisFolder(sidStr, path string) error {
	done := make(chan error, 1)
	go func() {
		// This folder only: no inheritance flags, so nothing below it is affected.
		done <- grantACL(path, sidStr, aclMaskReadExec, 0)
	}()

	started := time.Now()
	tick := time.NewTicker(setupProgressEvery)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			if err != nil {
				return fmt.Errorf("grant read/execute on %s: %w", path, err)
			}
			return nil
		case <-tick.C:
			LogInfo("  ... still working on %s (%s so far). Stopping now loses this volume's progress entirely.",
				path, time.Since(started).Round(time.Second))
		}
	}
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
		// Already granted? Say so and move on. This is what makes setup resumable:
		// re-running after a cancelled or interrupted attempt skips the volumes that
		// finished rather than paying for them again, and that payment is measured
		// in minutes on a large disk.
		if hasGrant(p) {
			LogInfo("Sandbox stat access on %s is already in place.", p)
			continue
		}
		LogInfo("Granting sandbox stat access on %s ... (minutes on a large or full volume; "+
			"let it finish -- interrupting loses this volume's progress)", p)
		started := time.Now()
		if err := grant(p); err != nil {
			failed++
			LogError("Failed to grant sandbox stat access on %s after %s: %v", p, time.Since(started).Round(time.Second), err)
			LogInfo("Continuing with the remaining paths; re-run 'nvx setup' afterwards to retry this one.")
			continue
		}
		LogInfo("Granted %s in %s.", p, time.Since(started).Round(time.Second))
	}
	return failed
}

// windowsSetupGrantPaths splits the ancestor roots into the ones this run will
// grant and the fixed volumes it will leave alone.
//
// Setup used to grant every fixed volume unconditionally. The permission is
// narrow and stays narrow -- root-only RX, non-inheritable, measured not to reach
// any subdirectory -- but its cost is proportional to the size of the volume, so
// granting a drive that will never hold a project buys nothing and can cost more
// than everything else combined. See setupGrantTimeout for the measurement.
//
// The default is therefore the volumes known to matter: the system drive, the
// profile's volume, nvx's own home, and the directory setup was run from. The
// rest are named in the output rather than silently dropped, because a project on
// an ungranted volume fails with a bare EPERM from npm that mentions neither the
// volume nor nvx. --all-drives restores the old behaviour.
//
// windowsAncestorGrantPaths stays the FULL list on purpose: --undo has to take
// back what any older setup granted, including volumes this one would skip.
func windowsSetupGrantPaths(nvxHome, workDir string, allDrives bool) (grant, skipped []string) {
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

	for _, root := range fixedDriveRoots() {
		if allDrives {
			add(root)
			continue
		}
		if !seen[strings.ToLower(filepath.Clean(root))] {
			skipped = append(skipped, root)
		}
	}
	return grant, skipped
}

func revokeSidGrant(sidStr, path string) error {
	if err := revokeACL(path, sidStr); err != nil {
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
func runWindowsSetup(nvxHome string, undo, allDrives bool) int {
	if !isElevated() {
		LogError("nvx setup must run from an elevated (Administrator) terminal.")
		LogInfo("It grants the nvx sandbox drive-root stat access for tools that need it. Egress is allowlisted either way. Undo later with: nvx setup --undo")
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
	// is derived here for the revoke sweep below and for nothing else.
	legacySidStr := ""
	if legacySid, lerr := ensureAppContainerSID(stableSandboxProfile); lerr == nil {
		defer syscall.LocalFree(syscall.Handle(legacySid))
		if s, serr := appContainerSidToString(legacySid); serr == nil {
			legacySidStr = s
		}
	}

	if undo {
		return runWindowsSetupUndo(nvxHome, sidStr, legacySidStr,
			revokeSidGrant, setLoopbackExempt, clearWindowsSetupState)
	}

	workDir, _ := os.Getwd()
	paths, skippedDrives := windowsSetupGrantPaths(nvxHome, workDir, allDrives)
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
	// it, including for users who ran an earlier setup. Best-effort: on a machine
	// that never had it, CheckNetIsolation simply reports nothing to delete.
	if legacySidStr != "" {
		if err := setLoopbackExempt(false, legacySidStr); err != nil {
			LogInfo("No loopback exemption to remove (the sandbox no longer needs one).")
		}
	}
	if err := writeWindowsSetupState(nvxHome, windowsSetupState{
		AppContainerSID: sidStr,
		GrantedPaths:    paths,
		LoopbackExempt:  false,
	}); err != nil {
		LogWarn("Setup applied, but recording state failed: %v", err)
	}

	// Named, not silently dropped. A project on one of these gets a bare EPERM from
	// npm that mentions neither nvx nor the volume, so the only way to connect the
	// two is to have been told here.
	if len(skippedDrives) > 0 {
		LogInfo("Left alone: %s. These volumes are not where nvx, your profile or this "+
			"directory live, and granting one costs time proportional to its size.",
			strings.Join(skippedDrives, ", "))
		LogInfo("Keep a project on one of them? Run 'nvx setup' from that volume, or 'nvx setup --all-drives'.")
	}

	if failed > 0 {
		LogError("nvx sandbox setup did not finish: %d path(s) above could not be granted.", failed)
		LogInfo("Windows sometimes completes an abandoned permission write minutes later. Re-run " +
			"'nvx setup' (elevated) -- anything already in place is skipped, so it resumes rather than starting over.")
		return 1
	}

	LogSuccess("nvx sandbox setup complete.")
	LogInfo("Drive-root access granted, for tools that resolve paths that far. Undo with: nvx setup --undo (elevated).")
	LogInfo("Egress is allowlisted with or without this step; setup is not required for it.")
	return 0
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
	undoPaths := append(windowsAncestorGrantPaths(), filepath.Clean(os.Getenv("USERPROFILE")))
	for _, p := range undoPaths {
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
