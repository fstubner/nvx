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
		manifest := filepath.Join(home, "package.json")
		if !strings.Contains(output, manifest) || !strings.Contains(output, "share one sandbox identity") {
			t.Fatalf("report output = %q, want manifest path and explanation", output)
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
