package nvx

import (
	"os"
	"path/filepath"
	"testing"
)

// `nvx auto` reports failure when it could not do what the directory asked.
//
// It used to always exit 0. A project pinned to a version you do not have
// printed "Run 'nvx install node@22'" and then reported success, so a script
// that ran `nvx auto && npm test` carried on with the wrong runtime. That is the
// silent-failure shape this project keeps finding in other tools.
//
// The nuance is the whole design. The shell integration runs auto on EVERY cd
// and pipes it into eval; if that returned non-zero in any directory without a
// match, every prompt in every such directory would show a failing status. So
// the code is reported only when stdout is a terminal -- a person who typed it --
// and never to the hook, which is piped.
func TestAutoReportsFailureToAPersonButNotToTheCdHook(t *testing.T) {
	orig := stdoutIsTerminal
	t.Cleanup(func() { stdoutIsTerminal = orig })

	for _, tc := range []struct {
		name     string
		unmet    int
		terminal bool
		want     int
	}{
		{"person, directory asked for something missing", 1, true, 1},
		{"person, several missing", 3, true, 1},
		{"person, nothing was asked for", 0, true, 0},
		{"cd hook, directory asked for something missing", 1, false, 0},
		{"cd hook, nothing was asked for", 0, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdoutIsTerminal = func() bool { return tc.terminal }
			if got := autoExitCode(tc.unmet); got != tc.want {
				t.Errorf("autoExitCode(unmet=%d, terminal=%v) = %d, want %d",
					tc.unmet, tc.terminal, got, tc.want)
			}
		})
	}
}

// The counting is WIRED IN: runAuto itself returns non-zero, not just the
// helper above.
//
// autoExitCode passing proves the decision, not that anything ever reaches it.
// This calls runAuto against a real directory whose .nvmrc names a version that
// cannot resolve, which is the case a user actually hits.
func TestRunAutoReturnsNonZeroWhenTheDirectoryAsksForSomethingMissing(t *testing.T) {
	orig := stdoutIsTerminal
	t.Cleanup(func() { stdoutIsTerminal = orig })
	stdoutIsTerminal = func() bool { return true } // a person typed it

	dir := tempDir(t)
	if err := os.WriteFile(filepath.Join(dir, ".nvmrc"), []byte("99.99.99\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	if got := runAuto(tempDir(t), "bash"); got == 0 {
		t.Error("runAuto reported success for a directory pinned to a version that is not installed. " +
			"A script doing `nvx auto && npm test` would run the tests on the wrong runtime.")
	}
}

// And the ordinary directory still succeeds, or every cd in a plain folder
// would look like a failure.
func TestRunAutoSucceedsWhereNothingIsDeclared(t *testing.T) {
	orig := stdoutIsTerminal
	t.Cleanup(func() { stdoutIsTerminal = orig })
	stdoutIsTerminal = func() bool { return true }

	dir := tempDir(t)
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	if got := runAuto(tempDir(t), "bash"); got != 0 {
		t.Errorf("runAuto returned %d in a directory that declares no runtime; that is the ordinary case", got)
	}
}
