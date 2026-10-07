package nvx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A shim with nothing to run says what to do, and exits with the code the
// documentation lists for "the command to run was not found".
//
// It printed "Could not find real executable for node" and exited 1, the code a
// command that ran and failed also exits with. Doctor knew the fix ("nvx install
// lts") and the shim did not say it.
func TestAShimWithNothingToRunExits127AndNamesTheFix(t *testing.T) {
	inEmptyDir(t)
	t.Setenv("PATH", "")
	nvxHome := tempDir(t)

	var code int
	stderr := captureStderrHere(t, func() {
		code = runShim("node", []string{"-v"}, nvxHome)
	})

	if code != 127 {
		t.Errorf("exit code %d, want 127 (docs/exit-codes.md: the command to run was not found)", code)
	}
	for _, want := range []string{"Could not find real executable for node", "nvx install lts"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr does not contain %q:\n%s", want, stderr)
		}
	}
}

// What the person should do depends on why there is nothing to run.
func TestTheHintForAMissingExecutableFollowsTheCause(t *testing.T) {
	inEmptyDir(t)
	t.Setenv("PATH", "")

	t.Run("no Node.js at all", func(t *testing.T) {
		got := strings.Join(noRealExecutableHints("npm", tempDir(t)), "\n")
		if !strings.Contains(got, "nvx install lts") {
			t.Errorf("hint = %q", got)
		}
	})
	t.Run("no Bun at all", func(t *testing.T) {
		got := strings.Join(noRealExecutableHints("bun", tempDir(t)), "\n")
		if !strings.Contains(got, "nvx install bun") {
			t.Errorf("hint = %q", got)
		}
	})

	nvxHome := tempDir(t)
	versionDir := filepath.Join(nvxHome, "versions", "node", "v22.1.0")
	writeStubBinary(t, nodeBinaryPath(versionDir))
	t.Run("installed, no default", func(t *testing.T) {
		// Yarn and pnpm too, since corepack enable has no Node.js to run on.
		for _, cmd := range []string{"node", "yarn", "pnpm"} {
			got := strings.Join(noRealExecutableHints(cmd, nvxHome), "\n")
			if !strings.Contains(got, "nvx default") || !strings.Contains(got, "nvx list") || strings.Contains(got, "corepack") {
				t.Errorf("%s: hint = %q", cmd, got)
			}
		}
	})

	if err := CreateLink(runtimeCurrentLinkPath(nvxHome, "node"), versionDir); err != nil {
		t.Fatal(err)
	}
	t.Run("yarn and pnpm that corepack has not set up", func(t *testing.T) {
		for _, cmd := range []string{"yarn", "pnpm"} {
			got := strings.Join(noRealExecutableHints(cmd, nvxHome), "\n")
			if !strings.Contains(got, "corepack enable") {
				t.Errorf("%s: hint = %q", cmd, got)
			}
		}
	})
	t.Run("a Node.js without corepack", func(t *testing.T) {
		got := strings.Join(noRealExecutableHints("corepack", nvxHome), "\n")
		if !strings.Contains(got, "has no corepack") || !strings.Contains(got, "npm install -g corepack") {
			t.Errorf("hint = %q", got)
		}
	})
	t.Run("an install that lost a file", func(t *testing.T) {
		got := strings.Join(noRealExecutableHints("npm", nvxHome), "\n")
		// Deleting the folder repairs it. `nvx uninstall` refuses the default version.
		for _, want := range []string{"v22.1.0", "has no npm", "Delete " + versionDir, "nvx install v22.1.0"} {
			if !strings.Contains(got, want) {
				t.Errorf("hint %q does not contain %q", got, want)
			}
		}
	})
}

// A project's own command is not a runtime problem, and the hints about runtimes
// would send someone the wrong way.
func TestAMissingProjectCommandGetsNoRuntimeAdvice(t *testing.T) {
	stderr := captureStderrHere(t, func() { reportNoRealExecutable("vite", tempDir(t)) })
	if !strings.Contains(stderr, "Command not found: vite") || strings.Contains(stderr, "nvx install") {
		t.Errorf("unexpected report for a command nvx does not wrap:\n%s", stderr)
	}
}

// An unknown nvx command is a usage error, which docs/exit-codes.md reserves 2
// for. Run as a child because Main exits.
func TestAnUnknownNvxCommandExitsWithTheUsageCode(t *testing.T) {
	if word := os.Getenv("NVX_TEST_UNKNOWN_COMMAND"); word != "" {
		os.Args = []string{"nvx", word}
		Main()
		os.Exit(0) // not reached: an unknown command exits
	}
	for _, word := range []string{"instal", "frobnicate"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestAnUnknownNvxCommandExitsWithTheUsageCode$")
		cmd.Env = append(os.Environ(), "NVX_TEST_UNKNOWN_COMMAND="+word, "NVX_HOME="+tempDir(t))
		err := cmd.Run()
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("nvx %s: want a non-zero exit, got %v", word, err)
		}
		if got := exitErr.ExitCode(); got != 2 {
			t.Errorf("nvx %s exited %d, want 2 (a usage error)", word, got)
		}
	}
}
