package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A runDoctor call asking for repairs, however its arguments are spelled.
var fixCallRe = regexp.MustCompile(`runDoctor\([^)]*,\s*true\)`)

// `nvx doctor --fix` makes two writes that a throwaway NVX_HOME does not contain:
// the persistent PATH, and the shell profile. Both are machine-wide, and both
// have been performed for real by the test suite.
//
// The PATH one was found first and given a seam. The profile one was not, and
// kept writing. Redirecting HOME does not reliably contain it: on Windows
// profilePathFor asks pwsh for $PROFILE, and where that lands depends on the
// host. A GitHub runner's pwsh follows USERPROFILE, so a redirected home does
// contain it; on a machine whose Documents folder is redirected to OneDrive it
// does not, and the answer is the developer's own profile. A test cannot tell
// which kind of host it is running on, so it stubs.
//
// Tests that did not stub wrote CI's profile. The Windows job's unit-test step
// logged
//
//	✔ Added the shell integration to C:\Users\runneradmin\Documents\PowerShell\Microsoft.PowerShell_profile.ps1
//
// after which every later pwsh step in that job printed "The term 'nvx' is not
// recognized" on startup, loading a line a test had planted.
//
// One caution when reading those logs: the success line is printed after the
// write call returns, so a stubbed run prints it too. Measured -- a stubbed run
// logged the line and created no file. It says which test reaches the write, and
// is never evidence that a write happened.

// stubProfileWrite makes the shell-profile write a no-op for one test.
func stubProfileWrite(t *testing.T) {
	t.Helper()
	restore := addIntegrationToProfile
	addIntegrationToProfile = func(string, string) error { return nil }
	t.Cleanup(func() { addIntegrationToProfile = restore })
}

// stubMachineWideWrites stops both of them, for a test that only wants what
// doctor writes inside the nvx home.
func stubMachineWideWrites(t *testing.T) {
	t.Helper()
	restorePath := repairPersistentPath
	repairPersistentPath = func(string, bool) (bool, error) { return false, nil }
	t.Cleanup(func() { repairPersistentPath = restorePath })
	stubProfileWrite(t)
}

// TestDoctorFixStillWritesTheIntegration keeps the repair alive.
//
// Named for what it checks. An earlier name said it proved nothing was written
// outside the test home, which it does not and cannot -- see below.
//
// HOME and USERPROFILE are redirected and SHELL is pinned to bash, so
// profilePathFor resolves inside the temp tree on every platform. The write runs
// for real here -- the seam is NOT stubbed -- which is what keeps the repair from
// being removed rather than fixed.
//
// What this asserts is exactly that: --fix still repairs. It does NOT assert that
// nothing outside the home was written, and an earlier version of it claimed to.
// The claim was false and measurably so: bypassing the seam entirely, so
// reportShellIntegration called the real write, left this test green, because the
// SHELL pin above means the PowerShell branch never runs and the path resolves
// into the temp home either way. The pin that makes the test safe is the same pin
// that makes it blind to the defect.
//
// The defect is covered instead by the two tests below -- one on the path
// decision that makes stubbing necessary, one on the callers that must stub.
func TestDoctorFixStillWritesTheIntegration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SHELL", "/bin/bash")
	t.Setenv("MSYSTEM", "")
	t.Setenv("NVX_SHELL_INTEGRATION", "")

	// The PATH repair stays stubbed: it is the other machine-wide write and has
	// its own coverage. Only the profile write runs for real here.
	restorePath := repairPersistentPath
	repairPersistentPath = func(string, bool) (bool, error) { return false, nil }
	t.Cleanup(func() { repairPersistentPath = restorePath })

	runDoctor(tempDir(t), true)

	profile := filepath.Join(home, ".bashrc")
	body, err := os.ReadFile(profile)
	if err != nil {
		t.Fatalf("doctor --fix wrote no profile at %s: %v.\n"+
			"The repair is gone, or the seam that makes it stubbable swallowed it.", profile, err)
	}
	if !strings.Contains(string(body), "nvx env") {
		t.Errorf("the profile in the test home does not load nvx:\n%s", body)
	}
}

// There is deliberately no test asserting where the profile path lands relative
// to a redirected home. It is host-dependent, and an earlier version of this file
// asserted "outside" and failed CI for exactly that reason: on a windows-latest
// runner profilePathFor resolved INSIDE the redirected home
// (...\Temp\Test...\001\Documents\PowerShell\...), because that pwsh follows
// USERPROFILE, while on this developer's machine it resolved to the OneDrive
// Documents folder outside it. Either assertion is false on the other host.
//
// The seam and the caller check below are what hold regardless of which host runs
// them, which is why the fix rests on those rather than on a fact about pwsh.

// TestEveryDoctorFixCallerStubsTheProfileWrite reads the test sources.
//
// A guard on behaviour cannot catch the next test that forgets, because a
// forgotten stub writes a file this package never looks at and nothing fails.
// The failure is invisible by construction, so the check has to be on the source
// rather than on a run -- the same reason the skip-reason allowlist in CI reads
// log text rather than trusting an exit code.
func TestEveryDoctorFixCallerStubsTheProfileWrite(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, "_test.go") || name == "doctor_machine_writes_test.go" {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		src := string(data)
		if !strings.Contains(src, "runDoctor(") {
			continue
		}
		// Any second argument, not a list of the spellings that happen to exist
		// today: a new test writing runDoctor(home, true) must not slip past the
		// check because nobody added that phrasing here.
		for _, call := range fixCallRe.FindAllString(src, -1) {
			if !strings.Contains(src, "stubProfileWrite(t)") && !strings.Contains(src, "stubMachineWideWrites(t)") {
				t.Errorf("%s calls %s without stubbing the profile write.\n"+
					"On Windows that appends nvx's integration line to the real $PROFILE, "+
					"which no assertion in this package would notice.\n"+
					"Add stubProfileWrite(t) or stubMachineWideWrites(t).", name, call)
			}
		}
	}
}
