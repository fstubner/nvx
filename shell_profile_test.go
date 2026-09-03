package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The line doctor writes must be one the profile is then seen to load.
//
// These two functions are a pair: profileLoadsIntegration is the guard that
// stops `nvx doctor --fix` appending a second copy, and its input is whatever
// addIntegrationToProfile wrote. If they ever disagree -- a change to the line's
// wording, a change to what the matcher looks for -- doctor appends the
// integration again on every run, and the profile grows a copy per invocation.
func TestTheLineDoctorWritesIsTheLineItThenRecognises(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "powershell"} {
		t.Run(shell, func(t *testing.T) {
			profile := filepath.Join(t.TempDir(), "profile")

			if profileLoadsIntegration(profile) {
				t.Fatal("a profile that does not exist was reported as loading nvx")
			}
			if err := os.WriteFile(profile, []byte("export FOO=1\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if profileLoadsIntegration(profile) {
				t.Fatal("a profile with no nvx line was reported as loading nvx")
			}

			if err := addIntegrationToProfile(profile, shell); err != nil {
				t.Fatalf("addIntegrationToProfile: %v", err)
			}
			if !profileLoadsIntegration(profile) {
				body, _ := os.ReadFile(profile)
				t.Fatalf("doctor --fix wrote the integration and would not see it, so it would "+
					"write it again on every run. Profile:\n%s", body)
			}
			// What was there before must survive: this appends to a file the user owns.
			body, _ := os.ReadFile(profile)
			if !strings.Contains(string(body), "export FOO=1") {
				t.Errorf("the existing profile contents were lost:\n%s", body)
			}
		})
	}
}

// A commented-out line is not integration. Someone who disabled it deliberately
// should be told version switching is off, not told everything is fine -- and
// the naive substring match on "nvx env" would have said fine.
func TestACommentedOutIntegrationDoesNotCount(t *testing.T) {
	profile := filepath.Join(t.TempDir(), ".zshrc")
	if err := os.WriteFile(profile, []byte("# eval \"$(nvx env --shell=zsh)\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if profileLoadsIntegration(profile) {
		t.Error("a commented-out integration line was counted as active")
	}
}

// Each shell gets its own dialect, matching what the installers write. A line in
// the wrong syntax is one the shell reports as an error on every startup, in the
// file it reads first.
func TestTheIntegrationLineMatchesTheShell(t *testing.T) {
	for _, tc := range []struct{ shell, want string }{
		{"powershell", "Invoke-Expression"},
		{"bash", `eval "$(nvx env --shell=bash)"`},
		{"zsh", `eval "$(nvx env --shell=zsh)"`},
	} {
		if got := integrationLineFor(tc.shell); !strings.Contains(got, tc.want) {
			t.Errorf("%s line %q does not contain %q", tc.shell, got, tc.want)
		}
	}
}

// The check is actually WIRED INTO doctor, verified through the built binary.
//
// The tests above call the functions directly and would all pass with the call
// site deleted -- the exact shape that let a shim warning ship unreached twice
// in this project. This runs the real `nvx doctor`.
//
// SHELL is pinned to bash deliberately. Left unset on Windows, defaultShell()
// answers "powershell" and profilePathFor asks pwsh for $PROFILE, which reports
// a path under the real Documents folder (redirected to OneDrive on this
// machine) that a scratch USERPROFILE does not move. A --fix run would then
// append to the developer's own profile.
func TestTheIntegrationCheckIsWiredIntoDoctor(t *testing.T) {
	exe := filepath.Join(tempDir(t), "nvx"+exeSuffixForTest())
	if out, err := runGoBuild(exe); err != nil {
		t.Skipf("cannot build nvx here: %v\n%s", err, out)
	}

	home := tempDir(t)
	doctor := func(args ...string) string {
		cmd := execCommandForTest(exe, append([]string{"doctor"}, args...)...)
		cmd.Env = append(os.Environ(),
			"NVX_HOME="+tempDir(t), "NVX_TRACE=",
			"HOME="+home, "USERPROFILE="+home,
			"SHELL=/bin/bash", "MSYSTEM=",
			"NVX_SHELL_INTEGRATION=")
		out, _ := cmd.CombinedOutput()
		return string(out)
	}

	profile := filepath.Join(home, ".bashrc")
	if got := doctor(); !strings.Contains(got, profile) {
		t.Fatalf("doctor never mentioned %s, so a shell that does not load nvx is reported as fine.\n"+
			"That is what a deleted call site looks like, and the direct tests above cannot see it.\n"+
			"nvx said:\n%s", profile, got)
	}

	if got := doctor("--fix"); !strings.Contains(got, profile) {
		t.Fatalf("doctor --fix said nothing about %s:\n%s", profile, got)
	}
	body, err := os.ReadFile(profile)
	if err != nil {
		t.Fatalf("doctor --fix did not create the profile: %v", err)
	}
	if n := strings.Count(string(body), "nvx env"); n != 1 {
		t.Fatalf("the profile loads nvx %d times after one --fix, want 1:\n%s", n, body)
	}

	// Re-running must not append a second copy.
	doctor("--fix")
	body, _ = os.ReadFile(profile)
	if n := strings.Count(string(body), "nvx env"); n != 1 {
		t.Errorf("a second 'doctor --fix' left %d copies of the integration, want 1:\n%s", n, body)
	}
}
