//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// A loopback exemption left behind by an older `nvx setup` lets a contained
// process reach every service on the machine's loopback address, whatever the
// egress allowlist says.
//
// Windows normally refuses an AppContainer's connections to 127.0.0.1 outside its
// own package, and 0.5.0's relay depends on that: the proxy is reached over a UNIX
// socket and re-exposed on loopback inside the container, so no exemption is
// needed. Before 0.5.0 there was no relay, the proxy ran on the host's loopback,
// and an elevated `nvx setup` registered an exemption so the sandbox could reach
// it -- which also opened every other loopback listener: a local database, a
// daemon's TCP port, another project's dev server.
//
// 0.5.0 never registers one and removes it in `nvx setup`, but that command needs
// an Administrator terminal, and the same release tells users setup is no longer
// required. So on a machine that ran the older elevated setup the exemption simply
// stays, and nothing said so: the allowlist looked enforced, external hosts really
// were blocked, and loopback was wide open. Found by acceptance testing on
// 2026-08-19, by reading a host listener from inside a contained process.
//
// nvx cannot remove it without elevation, so it warns instead -- on every affected
// launch, not once, because this is a live weakening of the containment the
// command is being asked for and a one-shot notice is easy to miss.

// loopbackExemptRecheckTTL bounds how long a clear result is trusted. Only the
// clear answer is cached: while the exemption is present every launch re-checks,
// so the warning stops the moment an elevated `nvx setup` removes it, rather than
// nagging for a day after the user has done what it asked.
const loopbackExemptRecheckTTL = 24 * time.Hour

type loopbackExemptCheck struct {
	SID       string    `json:"sid"`
	CheckedAt time.Time `json:"checked_at"`
}

func loopbackExemptCachePath(nvxHome string) string {
	return filepath.Join(nvxHome, "loopback-exempt-clear.json")
}

// parseLoopbackExemptSIDs pulls the package SIDs out of `CheckNetIsolation
// LoopbackExempt -s` output. It matches the SIDs themselves rather than the
// "SID:" label, because the label is localized and because an entry whose profile
// has been deleted prints its name as "AppContainer NOT FOUND" while remaining
// exempt.
func parseLoopbackExemptSIDs(out string) []string {
	return appContainerPackageSID.FindAllString(out, -1)
}

func sidListContains(sids []string, sidStr string) bool {
	for _, s := range sids {
		if strings.EqualFold(strings.TrimSpace(s), strings.TrimSpace(sidStr)) {
			return true
		}
	}
	return false
}

// listLoopbackExemptSIDs reads the machine's exemption list. The read needs no
// elevation; only adding or removing does.
// listLoopbackExemptSIDs is a variable so a test can supply an exempt machine.
//
// The warning this feeds is the ONLY mitigation for a hole the enforcement matrix
// describes as making the allowlist "unreachable to trust", and it was covered by
// a single test that skips unless the machine already carries an exemption. That
// test skipped in CI and on every healthy developer machine, so the matrix's claim
// that the check "is now pinned by TestExemptMachineIsWarnedAbout" was true only
// on a broken machine. Registering a real exemption to test it needs elevation and
// would leave the tester's machine less safe than it found it, so the seam goes
// here -- the same shape as seatbeltExecPath for the macOS fail-closed test.
var listLoopbackExemptSIDs = func() ([]string, error) {
	out, err := runWinCmd(15*time.Second, "CheckNetIsolation", "LoopbackExempt", "-s")
	if err != nil {
		return nil, fmt.Errorf("CheckNetIsolation LoopbackExempt -s: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return parseLoopbackExemptSIDs(string(out)), nil
}

// sandboxIsLoopbackExempt reports whether sidStr currently holds a loopback
// exemption, consulting the cached clear result first so a healthy machine pays
// nothing on the launch path (the check costs ~70ms, against a ~1s launch).
func sandboxIsLoopbackExempt(nvxHome, sidStr string) (bool, error) {
	if loopbackExemptRecentlyClear(nvxHome, sidStr) {
		return false, nil
	}
	sids, err := listLoopbackExemptSIDs()
	if err != nil {
		return false, err
	}
	if sidListContains(sids, sidStr) {
		return true, nil
	}
	markLoopbackExemptClear(nvxHome, sidStr)
	return false, nil
}

func loopbackExemptRecentlyClear(nvxHome, sidStr string) bool {
	data, err := os.ReadFile(loopbackExemptCachePath(nvxHome))
	if err != nil {
		return false
	}
	var c loopbackExemptCheck
	if err := json.Unmarshal(data, &c); err != nil {
		return false
	}
	if !strings.EqualFold(c.SID, sidStr) {
		return false
	}
	return time.Since(c.CheckedAt) < loopbackExemptRecheckTTL
}

func markLoopbackExemptClear(nvxHome, sidStr string) {
	data, err := json.Marshal(loopbackExemptCheck{SID: sidStr, CheckedAt: time.Now()})
	if err != nil {
		return
	}
	_ = os.WriteFile(loopbackExemptCachePath(nvxHome), data, 0o600)
}

// warnIfSandboxLoopbackExempt tells the user their sandbox can reach the whole
// machine's loopback, and how to stop it. Silent under network.mode "open", where
// no egress restriction was asked for in the first place.
func warnIfSandboxLoopbackExempt(nvxHome, sidStr, mode string) {
	if strings.EqualFold(strings.TrimSpace(mode), "open") {
		return
	}
	exempt, err := sandboxIsLoopbackExempt(nvxHome, sidStr)
	if err != nil || !exempt {
		// A failed check is not treated as a finding: CheckNetIsolation is absent
		// on some SKUs, and an unreadable list says nothing about what is in it.
		return
	}
	// One line, not four.
	//
	// This printed four lines on every contained command: what the exemption is,
	// what it exposes, that the egress allowlist is bypassable through it, and the
	// removal command. Every clause was true and worth saying once -- but seen in
	// a real transcript it was most of the output of an ordinary npm invocation,
	// and a warning that dominates every command is one people stop reading. That
	// costs more than the detail buys.
	//
	// Still every launch rather than once a day: it is a live weakening of the
	// containment being asked for, and a warning shown once is a warning missed.
	// The detail moved to `nvx doctor`, which reports the full explanation and the
	// exact removal command, and which exits non-zero so it cannot be mistaken for
	// a clean bill of health.
	LogWarn("Sandbox loopback exemption active: contained code can reach any service on 127.0.0.1, and the egress allowlist can be bypassed through one. Run 'nvx doctor' to see how to remove it.")
}

// deriveAppContainerSIDString returns the SID string for a profile name without
// registering the profile. `nvx doctor` diagnoses and must not create anything;
// DeriveAppContainerSidFromAppContainerName answers for any valid name whether or
// not a profile exists, which is what the exemption list is keyed by.
func deriveAppContainerSIDString(profileName string) (string, error) {
	name, err := syscall.UTF16PtrFromString(profileName)
	if err != nil {
		return "", err
	}
	var sid uintptr
	hr, _, callErr := procDeriveAppContainerSidFromAppContainerName.Call(
		uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(&sid)),
	)
	if hr != 0 || sid == 0 {
		return "", fmt.Errorf("DeriveAppContainerSidFromAppContainerName(%q) hr=0x%X: %v", profileName, hr, callErr)
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	return appContainerSidToString(sid)
}

// reportSandboxWeakeners is the doctor hook for machine state that quietly
// weakens containment. It reports and counts against health -- doctor printing a
// failure line and still calling the install healthy is the shape of dishonesty
// this command keeps being caught by. Removing the exemption needs elevation, but
// it is one command and doctor prints it.
func reportSandboxWeakeners(nvxHome string) bool {
	weakened := reportStrandedSetupGrant(nvxHome)

	sidStr, err := deriveAppContainerSIDString(stableSandboxProfile)
	if err != nil {
		return weakened
	}
	exempt, err := sandboxIsLoopbackExempt(nvxHome, sidStr)
	if err != nil || !exempt {
		return weakened
	}
	fmt.Println("  [FAIL] the sandbox has a loopback exemption from an older 'nvx setup'")
	fmt.Println("         contained code can reach any service on 127.0.0.1, whatever the egress allowlist says")
	fmt.Printf("         remove it from an Administrator terminal: CheckNetIsolation LoopbackExempt -d -p=%s\n", sidStr)
	return true
}

// reportStrandedSetupGrant reports a completed `nvx setup` whose grants sit on an
// identity nothing launches under any more.
//
// `nvx setup` granted the AppContainer package SID until packages became per
// project. Those grants are still on disk and still name that old package, so
// they admit nothing: measured 2026-08-30, `npx` on such a machine fails with
// "EPERM: operation not permitted, lstat 'C:\\Users'" from npm's own dependency
// walker, while the same command succeeds on the released build. nvx said
// nothing, and doctor called the machine healthy.
//
// Reported here rather than only at launch because doctor is where someone looks
// after a command failed in a way they cannot read, and because the launch
// advisory is deliberately shown once per identity -- useful the first time, and
// gone by the time anyone goes looking.
func reportStrandedSetupGrant(nvxHome string) bool {
	prev, ok := readWindowsSetupState(nvxHome)
	if !ok || prev.AppContainerSID == "" {
		return false // setup was never run here; nothing to be stranded
	}
	current, err := deriveCapabilitySIDString(setupCapabilityName)
	if err != nil {
		return false
	}
	workDir, _ := os.Getwd()
	missing := strandedSetupGrantPaths(nvxHome, workDir, prev.AppContainerSID, current,
		func(p string) bool { return appContainerHasGrantFor(current, p, grantReadExec) })
	if len(missing) == 0 {
		return false
	}

	fmt.Println("  [FAIL] the sandbox has no drive-root access on " + strings.Join(missing, ", "))
	fmt.Println("         an earlier 'nvx setup' granted an identity nvx no longer uses")
	fmt.Println("         a tool that resolves paths up to a drive root may fail there with EPERM")
	fmt.Println("         re-run 'nvx setup' from an Administrator terminal, in the directory you")
	fmt.Println("         work in, so it covers that volume")
	fmt.Println("         (an EPERM on a path inside ~/.nvx is a different problem, and needs no")
	fmt.Println("         elevation -- nvx retries those itself)")
	return true
}

// strandedSetupGrantPaths returns the paths this machine needs and the current
// setup identity does not hold. Empty means nothing to report.
//
// It asks the permissions, not the record. Reporting straight off the recorded
// identity said FAIL on a machine where C:\, C:\Users and D:\ already carried the
// current identity's entry -- because the record is only written when a setup run
// COMPLETES, and a run interrupted part-way through a slow volume leaves it
// holding whatever identity was there before. Measured 2026-09-02: two cancelled
// runs, three volumes correctly granted, and doctor still reporting that none of
// them applied.
//
// Reporting the wrong thing here is expensive rather than untidy. This is the
// message someone reads after a contained command failed, and the version it
// replaces asserted that npx "fails there with EPERM" -- which sent a maintainer
// to a 46-minute elevated grant on a volume that had nothing to do with the
// failure they were chasing.
func strandedSetupGrantPaths(nvxHome, workDir, recordedSID, currentSID string, hasGrant func(string) bool) []string {
	if recordedSID == "" || strings.EqualFold(recordedSID, currentSID) {
		return nil
	}
	paths, _ := windowsSetupGrantPaths(nvxHome, workDir, false)
	var missing []string
	for _, p := range paths {
		if !hasGrant(p) {
			missing = append(missing, p)
		}
	}
	return missing
}
