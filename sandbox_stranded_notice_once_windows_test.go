//go:build windows

package main

// The stranded-setup notice is shown once per identity and path, like the
// plain "cannot read the drive root" advisory next to it.
//
// It used to repeat on every package-manager run, on the reasoning that a
// stranded grant "BREAKS npx today". Measured 2026-09-01 and 2026-09-02: it does
// not -- contained npx runs unelevated with those grants stranded, because the
// only entry npm's walk needs is inside nvx's own home. A two-line warning on
// every install about a condition that breaks nothing was the loudest thing on
// the screen, and the person reading it had stopped. The case it was written
// for is still covered by remindAboutStrandedSetup, after a real failure.

import (
	"strings"
	"testing"
)

func TestTheStrandedSetupNoticeIsShownOnce(t *testing.T) {
	nvxHome := tempDir(t)
	if err := writeWindowsSetupState(nvxHome, windowsSetupState{
		AppContainerSID: "S-1-15-2-1-2-3-4-5-6-7", // a package identity nothing launches under
		GrantedPaths:    []string{`C:\`, `C:\Users`},
	}); err != nil {
		t.Fatal(err)
	}
	workDir := tempDir(t)

	// A machine on which no drive root is granted, whatever this one's really are.
	orig := driveRootHasGrant
	driveRootHasGrant = func(string, string) bool { return false }
	t.Cleanup(func() { driveRootHasGrant = orig })

	first := captureStderr(t, func() { noteMissingElevatedGrants(nvxHome, 0, workDir) })
	if !strings.Contains(first, "no longer uses") {
		t.Fatalf("the stranded-setup notice did not print on a first run with the grants missing:\n%s", first)
	}
	second := captureStderr(t, func() { noteMissingElevatedGrants(nvxHome, 0, workDir) })
	if strings.Contains(second, "no longer uses") {
		t.Fatalf("the stranded-setup notice printed again on the very next run. It is advisory -- "+
			"contained npx works with the grant stranded -- and repeating it on every install is "+
			"what made people stop reading nvx's warnings.\nsecond run printed:\n%s", second)
	}
}
