//go:build windows

package main

// The test an independent acceptance pass said was missing, and it was right.
//
// TestSandboxCannotReachOtherProjects builds two fresh directories and grants
// them under the current scheme, so it only ever exercises the case where the fix
// applies. It cannot fail when the claim stops holding for a directory granted by
// an older nvx -- which is every project on every existing installation. The
// acceptance pass found 19 stale package-SID ACEs on the nvx repo itself and used
// them to read and WRITE that directory from a contained process, while that test
// passed.
//
// This constructs the legacy shape deliberately: an ACE for an AppContainer
// package SID, of the kind nvx used to write, on a directory it then has to clean.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestLegacyPackageSidGrantsAreRemoved(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (writes and removes real ACEs)")
	}

	// Two throwaway profiles stand in for "an older nvx" and "a test run from
	// months ago" -- the accumulation the acceptance pass measured.
	var legacySIDs []string
	for _, name := range []string{"nvx.sandbox.legacyone", "nvx.sandbox.legacytwo"} {
		sid, err := ensureAppContainerSID(name)
		if err != nil {
			t.Fatalf("profile %s: %v", name, err)
		}
		str, err := appContainerSidToString(sid)
		syscall.LocalFree(syscall.Handle(sid))
		if err != nil {
			t.Fatal(err)
		}
		legacySIDs = append(legacySIDs, str)
		defer deleteAppContainerProfile(name)
	}

	project := tempDir(t)
	for _, sid := range legacySIDs {
		grantArg := fmt.Sprintf("*%s:(OI)(CI)(M)", sid)
		if out, err := runWinCmd(30*time.Second, "icacls", project, "/grant", grantArg, "/c", "/q"); err != nil {
			t.Skipf("cannot write a legacy-style ACE here: %v (%s)", err, strings.TrimSpace(string(out)))
		}
	}

	found := staleAppContainerSIDsOn(project)
	if len(found) < len(legacySIDs) {
		t.Fatalf("only %d of %d legacy grants were detected: %v", len(found), len(legacySIDs), found)
	}

	// What a launch in this project does.
	removeStaleAppContainerGrant("", project)

	if left := staleAppContainerSIDsOn(project); len(left) != 0 {
		t.Errorf("legacy package-SID grants survived cleanup: %v\n"+
			"Every sandbox on the machine holds those SIDs, so this project stays readable "+
			"and writable from any of them.", left)
	}
}

// TestCleanupLeavesTheProjectCapabilityAlone is the other direction, and the way
// this fix could do real damage: the per-project capability grant is what makes
// the sandbox able to use the directory at all. Removing package SIDs must not
// touch it.
func TestCleanupLeavesTheProjectCapabilityAlone(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (writes and removes real ACEs)")
	}

	project := tempDir(t)
	capSID, err := scopeCapabilitySID(sandboxScopeForWorkDir(project))
	if err != nil {
		t.Fatal(err)
	}
	if err := grantSandboxModify(capSID, project); err != nil {
		t.Fatalf("grant project capability: %v", err)
	}

	removeStaleAppContainerGrant("", project)

	if !appContainerHasGrant(capSID, project) {
		t.Error("cleanup removed this project's own capability grant; the sandbox would lose access " +
			"to the working directory it is supposed to be able to use")
	}
}

// TestStaleSidScanIgnoresCapabilitySids pins the distinction the scan rests on,
// without touching the filesystem. Package SIDs (S-1-15-2-) are the old shared
// identity and are stale by definition; capability SIDs (S-1-15-3-) are the new
// per-project ones and are load-bearing.
func TestStaleSidScanIgnoresCapabilitySids(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{`C:\p S-1-15-2-1234567890-1234567890-1234:(OI)(CI)(M)`, true},
		{`C:\p S-1-15-3-1024-3171843943-17353025-3738650921:(OI)(CI)(M)`, false},
		{`C:\p BUILTIN\Administrators:(F)`, false},
	}
	for _, tc := range cases {
		got := appContainerPackageSID.FindString(tc.line) != ""
		if got != tc.want {
			t.Errorf("%q matched=%v, want %v", tc.line, got, tc.want)
		}
	}
}

// TestStaleSidScanSkipsInheritedAces keeps the cleanup from trying to remove ACEs
// it cannot remove. /remove:g only touches explicit entries, so an inherited one
// costs a process launch and achieves nothing -- and the drive-root grants
// `nvx setup` adds arrive inherited.
func TestStaleSidScanSkipsInheritedAces(t *testing.T) {
	dir := tempDir(t)
	// No ACEs of interest here, so the scan must come back empty rather than
	// reporting whatever inherited entries the temp directory carries.
	if sids := staleAppContainerSIDsOn(filepath.Clean(dir)); len(sids) != 0 {
		t.Errorf("scan reported %v on a directory with no explicit AppContainer grants", sids)
	}
}
