package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// These drive runDoctor rather than diagnosePath, which is the whole point.
//
// An independent acceptance pass found the missing-bash-shim check unreachable:
// runDoctor regenerated the shims BEFORE diagnosing, so the files it looked for
// had just been recreated and it never reported one missing. Deleting every
// extensionless shim and running `nvx doctor` printed "intercepting commands
// correctly" and exit 0, having quietly put them back.
//
// The test written alongside that check passed throughout, because it called
// diagnosePath directly and so never exercised the regeneration that defeated it.
// Testing the unit while the defect lived in the caller is how a fix ships dead.

// TestDoctorDoesNotWriteShimsWithoutFix pins the read-only contract for the whole
// command, not for one function inside it.
func TestDoctorDoesNotWriteShimsWithoutFix(t *testing.T) {
	nvxHome := t.TempDir()
	shimDir := filepath.Join(nvxHome, "bin")

	if code := runDoctor(nvxHome, false); code == 0 {
		t.Error("doctor reported health for an nvx home with no shims at all")
	}

	if entries, err := os.ReadDir(shimDir); err == nil && len(entries) > 0 {
		t.Errorf("doctor created %d file(s) in %s without --fix; a command named after "+
			"diagnosis must not write, and writing here also hides the very problem it "+
			"is meant to report", len(entries), shimDir)
	}
}

// TestDoctorFixWritesShims is the other half: the repair has to actually repair,
// or moving it behind the flag would just remove the behaviour.
func TestDoctorFixWritesShims(t *testing.T) {
	nvxHome := t.TempDir()

	runDoctor(nvxHome, true)

	shimDir := filepath.Join(nvxHome, "bin")
	entries, err := os.ReadDir(shimDir)
	if err != nil {
		t.Fatalf("doctor --fix wrote no shim directory: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("doctor --fix created no shims")
	}

	// On Windows the extensionless shim is the one bash needs, and its absence is
	// the condition doctor now has to be able to see.
	if runtime.GOOS == "windows" {
		if _, err := os.Stat(filepath.Join(shimDir, "npm")); err != nil {
			t.Errorf("doctor --fix did not write the extensionless npm shim: %v", err)
		}
	}
}

// TestDoctorReportsMissingShimsRatherThanSilentlyFixingThem reproduces the
// acceptance pass's exact manoeuvre: write a complete set, delete the
// extensionless ones, and ask doctor. It must notice, not paper over.
func TestDoctorReportsMissingShimsRatherThanSilentlyFixingThem(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("extensionless shims only differ from the POSIX ones on Windows")
	}
	nvxHome := t.TempDir()
	if err := generateShims(nvxHome); err != nil {
		t.Fatal(err)
	}
	shimDir := filepath.Join(nvxHome, "bin")

	for _, cmd := range coreShimCommands() {
		_ = os.Remove(filepath.Join(shimDir, cmd))
	}

	if code := runDoctor(nvxHome, false); code == 0 {
		t.Error("doctor reported health with every bash shim deleted; a bare `npm` in Git " +
			"Bash would run unwrapped and the user would be told everything was fine")
	}
	if _, err := os.Stat(filepath.Join(shimDir, "npm")); err == nil {
		t.Error("doctor recreated the deleted shim without --fix, which is what made the " +
			"missing-shim check unreachable in the first place")
	}
}
