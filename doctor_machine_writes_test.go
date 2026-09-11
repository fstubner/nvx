package main

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
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
// kept writing: on Windows profilePathFor asks pwsh for $PROFILE, which answers
// with the real Documents folder whatever HOME says, so every `go test` that
// reached runDoctor(home, true) appended nvx's integration line to the
// developer's own profile -- and to CI's, where the effect is visible. The
// Windows job's unit-test step logged
//
//	✔ Added the shell integration to C:\Users\runneradmin\Documents\PowerShell\Microsoft.PowerShell_profile.ps1
//
// after which every later pwsh step in that job printed "The term 'nvx' is not
// recognized" on startup, loading a line a test had planted.

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

// TestTheWindowsProfilePathIgnoresARedirectedHome states the fact the seam exists
// for, without performing the write that demonstrates it.
//
// Redirecting HOME and USERPROFILE is the usual way a test keeps its writes to
// itself, and for the profile write on Windows it does not work: profilePathFor
// asks pwsh for $PROFILE, and pwsh answers from the real Documents folder --
// OneDrive-redirected on many machines -- which no environment variable this test
// can set will move. Measured here: with SHELL unset, defaultShell() answers
// "powershell" and the path resolved to a location outside the temp home.
//
// So a test that reaches runDoctor(_, true) on Windows cannot be made safe by
// redirecting the home. It has to stub the write, which is what the seam and the
// caller check below are for. If this ever stops being true -- pwsh honouring
// USERPROFILE, or nvx resolving the profile itself -- this test fails and the
// stubbing can be reconsidered rather than cargo-culted.
func TestTheWindowsProfilePathIgnoresARedirectedHome(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows resolves the profile path outside HOME")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SHELL", "")
	t.Setenv("MSYSTEM", "")

	if sh := defaultShell(); sh != "powershell" {
		t.Fatalf("defaultShell() = %q with SHELL unset on Windows, want powershell; "+
			"the rest of this test is about that branch", sh)
	}
	profile := profilePathFor("powershell")
	if profile == "" {
		t.Skip("pwsh did not report a $PROFILE on this host")
	}
	if strings.HasPrefix(strings.ToLower(profile), strings.ToLower(home)) {
		t.Fatalf("profilePathFor resolved %s, inside the redirected home %s.\n"+
			"If that is now reliable, the profile write no longer needs stubbing in tests.", profile, home)
	}
	t.Logf("profile resolves to %s, outside the test home -- hence the seam", profile)
}

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
