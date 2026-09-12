package nvx

import (
	"os"
	"path/filepath"
	"testing"
)

// And through the front door: an unpinned project file that switches
// isolated_tools on is ignored, not honoured.
//
// The unit test above proves the classification; this proves LoadPolicy acts
// on it. The two are separated by MergePolicies and the pin lookup, and a
// correct policyLoosens that LoadPolicy consulted too late -- after ProjectDir
// was already set from the local file -- would pass the first and fail this.
func TestAnUnpinnedProjectFileCannotSwitchIsolatedToolsOn(t *testing.T) {
	tmp := tempDir(t)
	projectDir := filepath.Join(tmp, "project")
	nvxHome := filepath.Join(tmp, ".nvx")
	for _, d := range []string{projectDir, nvxHome} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	body := `{"environment": {"isolated_tools": true}}`
	if err := os.WriteFile(filepath.Join(projectDir, ".nvx-policy.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWd) }()
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadPolicy(nvxHome)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Environment.IsolatedTools {
		t.Fatal("an unpinned project policy switched isolated_tools on; that puts <project>/.nvx/npm_global " +
			"-- a directory the repository controls -- on the user's PATH with no prompt")
	}
	if loaded.ProjectDir != "" {
		t.Fatalf("ProjectDir was set to %q from an unpinned project policy; the npm prefix would be "+
			"resolved into the repository", loaded.ProjectDir)
	}
}

// Turning on isolated_tools is a loosening, and needs the trust prompt like
// any other.
//
// environment.isolated_tools moves the npm global prefix from the per-version
// directory under ~/.nvx to <project>/.nvx/npm_global, and the shell
// integration puts that prefix on PATH on every cd. That is a directory the
// repository controls, on the user's PATH, ahead of the system: a checked-out
// project can ship its own `.nvx/npm_global` with whatever binaries it likes in
// it. MergePolicies honours a project file that sets it, and policyLoosens did
// not look at it, so the one project-file setting that puts repository content
// on PATH was the one that never asked.
func TestEnablingIsolatedToolsIsALoosening(t *testing.T) {
	base := func() Policy {
		p := DefaultPolicy()
		normalizePolicy(&p)
		return p
	}

	t.Run("a project file switching it on", func(t *testing.T) {
		before := base()
		after := base()
		after.Environment.IsolatedTools = true
		if !policyLoosens(before, after) {
			t.Fatal("enabling isolated_tools was not treated as a loosening; a checked-in policy " +
				"could put a repository-controlled directory on PATH with no prompt")
		}
	})

	t.Run("leaving it off is not a loosening", func(t *testing.T) {
		before := base()
		after := base()
		if policyLoosens(before, after) {
			t.Fatal("an unchanged isolated_tools setting was treated as a loosening")
		}
	})

	t.Run("already on globally is not a loosening", func(t *testing.T) {
		before := base()
		before.Environment.IsolatedTools = true
		after := base()
		after.Environment.IsolatedTools = true
		if policyLoosens(before, after) {
			t.Fatal("a project file agreeing with a global isolated_tools=true was treated as a loosening; " +
				"asking permission for nothing trains people to say yes")
		}
	})
}
