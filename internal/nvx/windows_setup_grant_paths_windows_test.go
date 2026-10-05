//go:build windows

package nvx

// Which volumes `nvx setup` grants, and which ones the notices after a failed
// command name.
//
// From 2026-09-01 until 2026-10-04 setup granted only the volumes a real path
// resolves up to, because each grant walked the whole volume: a 932GB volume
// with 1GB free on a 5400rpm disk had not finished after 36 minutes. The grant
// no longer walks anything, so setup covers every fixed volume again. The
// narrower selection stays for the notices, which name only the roots this run
// could have walked to.
//
// These assert the selection, not the permission: that the volumes a real path
// resolves up to are in the narrow set, that the rest are reported rather than
// silently dropped, and that setup itself covers every fixed volume.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupPathsContain(paths []string, want string) bool {
	for _, p := range paths {
		if strings.EqualFold(filepath.Clean(p), filepath.Clean(want)) {
			return true
		}
	}
	return false
}

func TestSetupGrantsTheVolumesRealPathsResolveTo(t *testing.T) {
	roots := fixedDriveRoots()
	if len(roots) == 0 {
		t.Skip("no fixed drives reported")
	}

	// A working directory on some volume must bring that volume in. Without it,
	// running setup from a project on H: would grant every volume except H:.
	for _, root := range roots {
		workDir := filepath.Join(root, "some", "project")
		grant := windowsSetupGrantPaths(`C:\Users\someone\.nvx`, workDir, false)
		if !setupPathsContain(grant, root) {
			t.Errorf("a run from %s does not list that volume's root %q, so a notice after "+
				"an EPERM there would not name it. Listed: %v", workDir, root, grant)
		}
	}
}

// The notices name only roots this run could have walked to. Naming every
// fixed volume read as a complete account of a failure, and listed volumes no
// project was on.
func TestTheNarrowSetLeavesUnrelatedVolumesOut(t *testing.T) {
	const nvxHome = `C:\Users\someone\.nvx`
	const workDir = `C:\Users\someone`

	// Which volumes this machine has that the run has no reason to name, worked
	// out from the SAME inputs the function is given -- never from what it
	// returned.
	needed := map[string]bool{}
	for _, p := range []string{os.Getenv("SystemDrive") + `\`, os.Getenv("USERPROFILE"), nvxHome, workDir} {
		if vol := filepath.VolumeName(p); vol != "" {
			needed[strings.ToUpper(vol)] = true
		}
	}
	var unrelated []string
	for _, root := range fixedDriveRoots() {
		if !needed[strings.ToUpper(filepath.VolumeName(root))] {
			unrelated = append(unrelated, root)
		}
	}
	if len(unrelated) == 0 {
		t.Skip("every fixed volume on this machine is one this run resolves to; nothing to leave out")
	}

	grant := windowsSetupGrantPaths(nvxHome, workDir, false)
	for _, root := range unrelated {
		if setupPathsContain(grant, root) {
			t.Errorf("%q holds neither nvx, the profile, nor the working directory, and is listed "+
				"anyway: %v", root, grant)
		}
	}
}

func TestSetupCoversEveryFixedVolume(t *testing.T) {
	roots := fixedDriveRoots()
	if len(roots) == 0 {
		t.Skip("no fixed drives reported")
	}
	grant := windowsSetupPaths(`C:\Users\someone\.nvx`, `C:\Users\someone`)
	for _, root := range roots {
		if !setupPathsContain(grant, root) {
			t.Errorf("plain `nvx setup` did not cover fixed volume %q: %v", root, grant)
		}
	}
}

func TestSetupGrantPathsAreDeduplicated(t *testing.T) {
	grant := windowsSetupPaths(`C:\Users\someone\.nvx`, `C:\Users\someone\project`)
	seen := map[string]int{}
	for _, p := range grant {
		seen[strings.ToUpper(filepath.Clean(p))]++
	}
	for p, n := range seen {
		if n > 1 {
			// Each duplicate is a second write of a permission already made.
			t.Errorf("path %q listed %d times; setup would grant it more than once", p, n)
		}
	}
}

// Setup resumes rather than starting over, and one failure does not lose the rest.
//
// Both were wrong until 2026-09-01 and both cost the same person the same thing.
// A grant that failed returned immediately, and the volume holding the user's
// projects is granted last -- so the single grant that mattered was the one most
// likely never to be attempted. Nothing skipped work already done either, so a
// cancelled run had to pay for every completed volume again, at minutes each.
func TestSetupSkipsGrantsAlreadyInPlace(t *testing.T) {
	var attempted []string
	failed := runWindowsSetupGrants(
		[]string{`C:\`, `C:\Users`, `H:\`},
		func(p string) bool { return p == `C:\` || p == `C:\Users` },
		func(p string) error { attempted = append(attempted, p); return nil },
	)
	if failed != 0 {
		t.Errorf("no grant failed, but %d were counted as failures", failed)
	}
	if len(attempted) != 1 || attempted[0] != `H:\` {
		t.Errorf("expected only the ungranted path to be written, got %v; re-running setup would "+
			"write again to volumes already done", attempted)
	}
}

func TestSetupContinuesPastAFailedGrantAndCountsIt(t *testing.T) {
	var attempted []string
	failed := runWindowsSetupGrants(
		// F: stands in for the slow volume; H: for the one holding the projects,
		// which the old code granted last and therefore never reached.
		[]string{`F:\`, `G:\`, `H:\`},
		func(string) bool { return false },
		func(p string) error {
			attempted = append(attempted, p)
			if p == `F:\` {
				return errors.New("did not complete within 2m0s")
			}
			return nil
		},
	)
	if !slicesContain(attempted, `H:\`) {
		t.Errorf("a failure on %q stopped setup before it reached %q -- the volume the projects are "+
			"on. Attempted: %v", `F:\`, `H:\`, attempted)
	}
	if failed != 1 {
		t.Errorf("expected exactly one failure to be counted, got %d; setup must not report success "+
			"for a permission it did not manage to grant", failed)
	}
}

func slicesContain(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}
