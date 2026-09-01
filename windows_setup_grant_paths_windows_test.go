//go:build windows

package main

// Which volumes `nvx setup` grants, and which it deliberately leaves alone.
//
// Setup granted every fixed volume unconditionally until 2026-09-01. The
// permission is narrow -- root-only RX, non-inheritable -- but the write costs
// time proportional to the size of the volume, because Windows re-runs
// auto-inheritance beneath the directory. Measured on the development machine: a
// 932GB volume with 1GB free on a 5400rpm disk had not finished after 36 minutes,
// and two more volumes were queued behind it, the last of which was the one
// holding the user's projects. The volume that mattered was granted last, behind
// two that held no projects at all.
//
// These assert the selection, not the permission: that the volumes a real path
// resolves up to are covered, that the rest are reported rather than silently
// dropped, and that --all-drives still means all.

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
		grant, skipped := windowsSetupGrantPaths(`C:\Users\someone\.nvx`, workDir, false)
		if !setupPathsContain(grant, root) {
			t.Errorf("setup run from %s did not grant that volume's root %q; a project there "+
				"would fail with a bare EPERM. Granted: %v", workDir, root, grant)
		}
		if setupPathsContain(skipped, root) {
			t.Errorf("%q was reported as skipped even though setup was run from it", root)
		}
	}
}

func TestSetupLeavesUnrelatedVolumesAloneAndSaysSo(t *testing.T) {
	const nvxHome = `C:\Users\someone\.nvx`
	const workDir = `C:\Users\someone`

	// Which volumes this machine has that setup has no reason to touch, worked
	// out from the SAME inputs the function is given -- never from what it
	// returned. The first version of this decided there was nothing to check when
	// the skipped list came back empty, which is precisely the broken state: with
	// the old grant-everything behaviour restored, the test skipped itself and
	// reported ok.
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
		t.Skip("every fixed volume on this machine is one setup needs; nothing to leave alone")
	}

	grant, skipped := windowsSetupGrantPaths(nvxHome, workDir, false)

	for _, root := range unrelated {
		if setupPathsContain(grant, root) {
			t.Errorf("setup granted %q, which holds neither nvx, the profile, nor the working "+
				"directory. That write costs time proportional to the size of the volume and buys "+
				"nothing. granted=%v", root, grant)
		}
		if !setupPathsContain(skipped, root) {
			t.Errorf("%q was left ungranted but not reported as skipped, so a project there would "+
				"fail with an error naming neither nvx nor the volume. skipped=%v", root, skipped)
		}
	}

	// Every fixed volume accounted for: granted or named. Neither is the silent case.
	for _, root := range fixedDriveRoots() {
		if !setupPathsContain(grant, root) && !setupPathsContain(skipped, root) {
			t.Errorf("fixed volume %q is neither granted nor reported as skipped; it would be "+
				"silently ungranted. granted=%v skipped=%v", root, grant, skipped)
		}
	}
}

func TestSetupAllDrivesStillCoversEveryFixedVolume(t *testing.T) {
	roots := fixedDriveRoots()
	if len(roots) == 0 {
		t.Skip("no fixed drives reported")
	}
	grant, skipped := windowsSetupGrantPaths(`C:\Users\someone\.nvx`, `C:\Users\someone`, true)
	for _, root := range roots {
		if !setupPathsContain(grant, root) {
			t.Errorf("--all-drives did not cover fixed volume %q: %v", root, grant)
		}
	}
	if len(skipped) != 0 {
		t.Errorf("--all-drives reported %v as skipped; it grants everything by definition", skipped)
	}
}

func TestSetupGrantPathsAreDeduplicated(t *testing.T) {
	grant, _ := windowsSetupGrantPaths(`C:\Users\someone\.nvx`, `C:\Users\someone\project`, true)
	seen := map[string]int{}
	for _, p := range grant {
		seen[strings.ToUpper(filepath.Clean(p))]++
	}
	for p, n := range seen {
		if n > 1 {
			// Each duplicate is a second expensive write of a permission already
			// made, on the slowest operation setup performs.
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
			"pay again for volumes already done, which is minutes each on a large disk", attempted)
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
