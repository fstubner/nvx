package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `nvx doctor --fix` must not apply a persistent PATH change it did not report.
//
// The apply used to call repairPersistentPath(nvxHome, fix) directly while the
// offer was gated on the live PATH being wrong, so a machine with a healthy PATH
// and one unrelated failure was never told its User PATH would be touched, ran
// --fix for the other problem, and had it edited and announced afterwards. An
// acceptance pass hit exactly that and could not say what had changed.
//
// The seam records what it was asked to do. apply=true arriving without a
// preceding apply=false is the defect: it means something was written that no
// report had offered.
func TestDoctorFixOnlyAppliesAPathRepairItAlreadyChecked(t *testing.T) {
	var calls []bool // one entry per call: the apply flag

	restore := repairPersistentPath
	repairPersistentPath = func(_ string, apply bool) (bool, error) {
		calls = append(calls, apply)
		// A repair is available, which is what makes the apply reachable at all.
		return true, nil
	}
	t.Cleanup(func() { repairPersistentPath = restore })

	nvxHome := tempDir(t)
	_ = runDoctor(nvxHome, true)

	if len(calls) == 0 {
		t.Fatal("the persistent PATH was never consulted, so this test proves nothing about how it is applied")
	}
	if calls[0] != false {
		t.Fatal("the first call applied a change; doctor must find out whether a repair is available before making one")
	}
	sawApply := false
	for _, apply := range calls {
		if apply {
			sawApply = true
		}
	}
	if !sawApply {
		t.Fatal("--fix never applied the repair it had found; the fix half is missing")
	}
}

// Without --fix nothing may be written at all, whatever else is wrong.
func TestDoctorWithoutFixNeverAppliesAPathRepair(t *testing.T) {
	var applied bool

	restore := repairPersistentPath
	repairPersistentPath = func(_ string, apply bool) (bool, error) {
		if apply {
			applied = true
		}
		return true, nil
	}
	t.Cleanup(func() { repairPersistentPath = restore })

	nvxHome := tempDir(t)
	_ = runDoctor(nvxHome, false)

	if applied {
		t.Fatal("doctor edited the persistent PATH without --fix; it is a diagnosis command")
	}
}

// A temporary directory must never reach a persistent setting.
//
// Tests, agents and CI steps point NVX_HOME at a temp directory as a matter of
// course, and the User PATH outlives it. This machine still carries a
// `...\AppData\Local\Temp\nvxa\bin` entry written that way: dead once the
// directory is cleaned up, and until then a search-path entry that anything able
// to write the temp tree can drop an executable into.
func TestATemporaryNvxHomeIsNeverWrittenToThePersistentPath(t *testing.T) {
	if isWindows := os.PathSeparator == '\\'; !isWindows {
		t.Skip("repairPersistentPath only writes a persistent PATH on Windows")
	}
	tempHome := filepath.Join(os.TempDir(), "nvx-persistent-path-probe")
	// apply=false deliberately. The guard runs before any registry access, so this
	// asserts exactly the same thing, and it cannot write the real User PATH if the
	// guard is ever removed. The first version passed apply=true: sabotaging the
	// guard to check this test discriminates duly wrote
	// `%TEMP%\nvx-persistent-path-probe\bin` into the developer's persistent PATH,
	// which then had to be removed by hand. A test that verifies a refusal must not
	// perform the thing being refused when the refusal is missing.
	available, err := repairPersistentPathImpl(tempHome, false)
	if err == nil {
		t.Fatal("a temp NVX_HOME was accepted for the persistent PATH; the entry outlives the directory")
	}
	if available {
		t.Error("a refused repair must not also report itself as available")
	}
}

// ...and the check has to discriminate, or it would refuse every repair and the
// test above would pass against a function that never works.
//
// The directory is spelled out rather than taken from os.TempDir(), and both with
// and without a trailing separator, because that is the difference between the
// platforms and it is not something a test should be guessing at.
//
// Every path is assembled with filepath.Join and filepath.Separator, never
// written as a literal. A backslash is a separator on Windows and an ordinary
// character everywhere else, so a literal `C:\a\b` is one path element on Linux
// and this table would assert the opposite of what it means there.
func TestARealNvxHomeIsStillEligibleForPersistentPathRepair(t *testing.T) {
	sep := string(filepath.Separator)
	tmp := filepath.Join("home", "someone", "tmp")
	tmpSlash := tmp + sep // how macOS spells os.TempDir(); Windows does not

	cases := []struct {
		name string
		dir  string
		path string
		want bool
	}{
		{"inside temp", tmp, filepath.Join(tmp, "nvxa", "bin"), true},
		{"inside temp, dir has trailing separator", tmpSlash, filepath.Join(tmp, "nvxa", "bin"), true},
		{"the temp dir itself", tmp, tmp, true},
		{"the temp dir itself, spelled with a separator", tmpSlash, tmp, true},
		{"a real home", tmp, filepath.Join("home", "someone", ".nvx", "bin"), false},
		// A sibling whose name merely starts with the directory's must not match:
		// a prefix comparison without a separator says it does.
		{"temp-like sibling", tmp, filepath.Join(tmp+"2", "bin"), false},
		{"temp-like sibling, dir has trailing separator", tmpSlash, filepath.Join(tmp+"2", "bin"), false},
		{"case differs", tmp, filepath.Join(strings.ToUpper(tmp), "nvxa"), true},
		{"empty path", tmp, "", false},
		{"empty dir", "", filepath.Join(tmp, "nvxa"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := underDir(tc.path, tc.dir); got != tc.want {
				t.Fatalf("underDir(%q, %q) = %v, want %v", tc.path, tc.dir, got, tc.want)
			}
		})
	}

	// One check that the wiring reaches the real temp directory at all, so the
	// table above cannot be passing against a function nothing calls.
	if !underTempDir(filepath.Join(os.TempDir(), "nvxa", "bin")) {
		t.Error("underTempDir did not recognise a path inside the real temp directory")
	}
}
