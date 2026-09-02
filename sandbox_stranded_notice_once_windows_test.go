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

	// The notice is detail now, so it is only visible with --verbose -- and an
	// earlier test in the same process may have left quiet mode on.
	quietFlag, verboseFlag = false, true
	t.Cleanup(func() { quietFlag, verboseFlag = false, false })

	first := captureStderr(t, func() { noteMissingElevatedGrants(nvxHome, 0, workDir) })
	if !strings.Contains(first, "no longer uses") {
		t.Fatalf("the stranded-setup notice did not print on a first verbose run with the grants missing:\n%s", first)
	}
	second := captureStderr(t, func() { noteMissingElevatedGrants(nvxHome, 0, workDir) })
	if strings.Contains(second, "no longer uses") {
		t.Fatalf("the stranded-setup notice printed again on the very next run. It is advisory -- "+
			"contained npx works with the grant stranded -- and repeating it on every install is "+
			"what made people stop reading nvx's warnings.\nsecond run printed:\n%s", second)
	}
}

// An elevated `nvx setup` is asked for once, after a package-manager command
// has failed, and never on the way in. Asking for Administrator rights on a
// drive root is a lot to ask, and the measured need for the grant is nil:
// installs and npx work without it. A missing grant used to be two warning
// lines on every install, which read as "required" and cost the person running
// nvx a 22-minute elevated write on a volume nothing of theirs had needed.
func TestSetupIsSuggestedOnlyAfterAFailure(t *testing.T) {
	nvxHome := tempDir(t)
	workDir := tempDir(t)
	quietFlag = false // an earlier test in the same process may have left it on
	orig := driveRootHasGrant
	driveRootHasGrant = func(string, string) bool { return false }
	t.Cleanup(func() { driveRootHasGrant = orig })

	before := captureStderr(t, func() { noteMissingElevatedGrants(nvxHome, 0, workDir) })
	if strings.TrimSpace(before) != "" {
		t.Fatalf("a launch with the drive-root grant missing printed something without --verbose:\n%s", before)
	}
	after := captureStderr(t, func() { remindAboutDriveRoots(nvxHome, workDir) })
	if !strings.Contains(after, "nvx setup") || !strings.Contains(after, "EPERM") {
		t.Fatalf("after a failed command with the grant missing, nvx did not say what setup is for:\n%s", after)
	}

	// And with the grant in place there is nothing to say even after a failure.
	driveRootHasGrant = func(string, string) bool { return true }
	if got := captureStderr(t, func() { remindAboutDriveRoots(nvxHome, workDir) }); strings.TrimSpace(got) != "" {
		t.Fatalf("setup was suggested although every drive root is already granted:\n%s", got)
	}
}
