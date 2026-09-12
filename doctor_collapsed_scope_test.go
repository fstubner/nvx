package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReportCollapsedProjectScope(t *testing.T) {
	t.Run("home manifest collapses projects below it", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		if err := os.WriteFile(filepath.Join(home, "package.json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}

		workDir := filepath.Join(home, "projects", "one")
		if err := os.MkdirAll(workDir, 0o700); err != nil {
			t.Fatal(err)
		}
		chdirForCollapsedScopeTest(t, workDir)

		var reported bool
		output := captureCollapsedScopeOutput(t, func() {
			reported = reportCollapsedProjectScope()
		})
		if !reported {
			t.Fatal("reportCollapsedProjectScope() = false, want true")
		}
		// The reported path is the resolved one, because nvx compares resolved
		// paths: os.Getwd answers with symlinks resolved and os.UserHomeDir does
		// not. On macOS this temp home lives under /var, a link to /private/var,
		// so asserting the unresolved spelling failed there while passing on Linux
		// and Windows.
		resolvedHome, err := filepath.EvalSymlinks(home)
		if err != nil {
			resolvedHome = home
		}
		manifest := filepath.Join(resolvedHome, "package.json")
		if !strings.Contains(output, manifest) || !strings.Contains(output, "share one sandbox identity") {
			t.Fatalf("report output = %q, want manifest path %q and explanation", output, manifest)
		}
	})

	t.Run("project manifest at working directory is normal", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		workDir := filepath.Join(home, "project")
		if err := os.MkdirAll(workDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workDir, "package.json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		chdirForCollapsedScopeTest(t, workDir)

		if reportCollapsedProjectScope() {
			t.Fatal("reportCollapsedProjectScope() = true, want false")
		}
	})

	t.Run("no manifest does not report", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		workDir := filepath.Join(home, "project")
		if err := os.MkdirAll(workDir, 0o700); err != nil {
			t.Fatal(err)
		}
		chdirForCollapsedScopeTest(t, workDir)

		if reportCollapsedProjectScope() {
			t.Fatal("reportCollapsedProjectScope() = true, want false")
		}
	})

	// The false-positive half. A manifest in an ordinary ancestor is a monorepo,
	// and every package beneath it sharing one scope is that layout working as
	// intended. Without this, widening the rule to "any ancestor" would pass the
	// other three subtests while warning on every monorepo in existence.
	t.Run("an ordinary ancestor is a monorepo, not a collapse", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		monoRoot := filepath.Join(home, "mono")
		workDir := filepath.Join(monoRoot, "packages", "app")
		if err := os.MkdirAll(workDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(monoRoot, "package.json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		chdirForCollapsedScopeTest(t, workDir)

		if reportCollapsedProjectScope() {
			t.Fatal("a monorepo root was reported as collapsing scope; only the home directory " +
				"and a volume root should be")
		}
	})

	// The symlink case, which is the one CI caught. os.Getwd resolves symlinks
	// and os.UserHomeDir does not, so comparing one spelling against the other
	// reported nothing at all -- the check was dead on macOS, where the temporary
	// directories live under /var, itself a link to /private/var.
	//
	// Written with an explicit link rather than relying on a platform's own
	// layout, so Linux pins this too instead of leaving it to macOS alone. The
	// three subtests above cannot: on a filesystem with no link in the path,
	// removing the resolution changes nothing and they all still pass. Measured
	// -- that sabotage passed on Windows.
	t.Run("a home reached through a symlink is still detected", func(t *testing.T) {
		base := t.TempDir()
		realHome := filepath.Join(base, "real")
		if err := os.MkdirAll(realHome, 0o700); err != nil {
			t.Fatal(err)
		}
		linkedHome := filepath.Join(base, "linked")
		if err := os.Symlink(realHome, linkedHome); err != nil {
			// Phrased to match the skip reason CI's probe step allows; a reason it
			// does not recognise fails that step as a probe verifying nothing.
			t.Skipf("creating symlinks on Windows needs privilege or Developer Mode: %v", err)
		}
		if err := os.WriteFile(filepath.Join(realHome, "package.json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}

		// HOME is the LINK, and the working directory is reached through it, so
		// os.Getwd answers with the resolved path and the two spellings differ --
		// exactly the mismatch that hid the manifest.
		t.Setenv("HOME", linkedHome)
		t.Setenv("USERPROFILE", linkedHome)
		workDir := filepath.Join(linkedHome, "projects", "one")
		if err := os.MkdirAll(workDir, 0o700); err != nil {
			t.Fatal(err)
		}
		chdirForCollapsedScopeTest(t, workDir)

		if !reportCollapsedProjectScope() {
			t.Fatal("a home directory reached through a symlink was not detected, so the check is " +
				"comparing an unresolved path against a resolved one")
		}
	})
}

// TestTheCollapsedScopeCheckIsWiredIntoDoctor drives the command, not the
// function.
//
// Every subtest above calls reportCollapsedProjectScope directly, so all four
// would still pass with the call site in runDoctor deleted. That is the shape
// that shipped an unreached shim warning twice in this project, and why
// shell_profile_test.go carries the same kind of test for the integration check.
//
// Only the report is asserted. Whether the condition also makes doctor exit
// non-zero cannot be isolated here: a throwaway NVX_HOME is unhealthy for
// unrelated reasons -- no shims, not on PATH -- so the exit code would be
// non-zero either way and would prove nothing about this check.
func TestTheCollapsedScopeCheckIsWiredIntoDoctor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.WriteFile(filepath.Join(home, "package.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	workDir := filepath.Join(home, "projects", "one")
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatal(err)
	}
	chdirForCollapsedScopeTest(t, workDir)

	// fix=false, so doctor only diagnoses: nothing machine-wide is written and no
	// stub is needed for the persistent PATH or the shell profile.
	output := captureCollapsedScopeOutput(t, func() {
		_ = runDoctor(tempDir(t), false)
	})

	if !strings.Contains(output, "collapses sandbox isolation") {
		t.Fatalf("nvx doctor never mentioned the collapsing manifest, so the check is not reached "+
			"from runDoctor. nvx said:\n%s", output)
	}
}

func chdirForCollapsedScopeTest(t *testing.T, dir string) {
	t.Helper()
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldDir); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
}

func captureCollapsedScopeOutput(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	os.Stdout = w
	defer func() {
		os.Stdout = oldStdout
	}()

	fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	return string(output)
}
