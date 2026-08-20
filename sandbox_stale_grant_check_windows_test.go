//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The exposure README has always disclosed -- a project nvx used before 0.5.0
// stays writable by every current sandbox -- had no way to be observed. An
// acceptance pass found 19 such grants live on this repository while `doctor`
// reported a healthy install and exited 0.
//
// These assert the observation, not the underlying containment: that a project
// carrying leftover package-SID grants is reported, counted against health, and
// cleaned under --fix, and that a clean project stays quiet.
func TestStaleProjectGrantsAreFoundReportedAndFixed(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (writes and removes real ACEs)")
	}

	sid, err := ensureAppContainerSID("nvx.sandbox.staleprobe")
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	sidStr, err := appContainerSidToString(sid)
	syscall.LocalFree(syscall.Handle(sid))
	if err != nil {
		t.Fatal(err)
	}
	defer deleteAppContainerProfile("nvx.sandbox.staleprobe")

	project := t.TempDir()
	// package.json so findProjectRoot treats this as the project root, which is
	// what the check reports on.
	if err := os.WriteFile(filepath.Join(project, "package.json"), []byte(`{"name":"p"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// A clean project must not be reported. Establishing this first means a later
	// "found" result cannot be something the scanner reports about every directory.
	if reportStaleProjectGrants(project, false) {
		t.Fatal("a project with no leftover grants was reported as carrying them")
	}

	grantArg := fmt.Sprintf("*%s:(OI)(CI)(M)", sidStr)
	if out, err := runWinCmd(30*time.Second, "icacls", project, "/grant", grantArg, "/c", "/q"); err != nil {
		t.Skipf("cannot write a legacy-style ACE here: %v (%s)", err, strings.TrimSpace(string(out)))
	}

	rep := scanStaleProjectGrants(project)
	if len(rep.SIDs) == 0 {
		t.Fatal("a planted package-SID grant was not found; the check cannot see the exposure it exists for")
	}
	if !sidListContains(rep.SIDs, sidStr) {
		t.Errorf("found %v, which does not include the planted SID %s", rep.SIDs, sidStr)
	}

	// Reporting must not mutate: doctor without --fix diagnoses only, which is the
	// rule that moved the PATH repair behind the flag in the first place.
	if !reportStaleProjectGrants(project, false) {
		t.Error("a project carrying leftover grants was not reported")
	}
	if len(scanStaleProjectGrants(project).SIDs) == 0 {
		t.Error("reporting without --fix removed the grant; doctor must not write unless asked")
	}

	if reportStaleProjectGrants(project, true) {
		t.Error("--fix reported the grant as still present after removing it")
	}
	if remaining := scanStaleProjectGrants(project).SIDs; len(remaining) != 0 {
		t.Errorf("--fix left %d grant(s) behind: %v", len(remaining), remaining)
	}
}

// A capability SID is the per-project identity 0.5.0 replaced the shared package
// SID with. Removing one would revoke the grant the running sandbox depends on,
// so the scanner must never treat it as stale.
func TestStaleGrantScanIgnoresPerProjectCapabilitySIDs(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (writes and removes real ACEs)")
	}

	project := t.TempDir()
	capSID, err := scopeCapabilitySID(project)
	if err != nil {
		t.Skipf("cannot derive a capability SID here: %v", err)
	}
	grantArg := fmt.Sprintf("*%s:(OI)(CI)(M)", capSID)
	if out, err := runWinCmd(30*time.Second, "icacls", project, "/grant", grantArg, "/c", "/q"); err != nil {
		t.Skipf("cannot write a capability ACE here: %v (%s)", err, strings.TrimSpace(string(out)))
	}

	if rep := scanStaleProjectGrants(project); len(rep.SIDs) != 0 {
		t.Errorf("the per-project capability SID was reported as a stale grant (%v); --fix would revoke the sandbox's own access", rep.SIDs)
	}
}
