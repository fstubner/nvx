package nvx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Leaving a project takes its project-bin directory off PATH.
//
// Every project gets its own shim directory under ~/.nvx/project-bin/<hash>,
// and CleanAndBuildPath puts the current project's at the front of PATH. It
// stripped stale entries for the OTHER per-project directory -- the npm prefix
// under <project>/.nvx/npm_global -- and not for this one. So after `cd` from
// project A into project B, A's shims stayed on PATH behind B's, and a command
// A's node_modules provided kept resolving in B, from A. For a name B does
// not have, that is the wrong tool running silently; for a name both have, it
// is B's until B's shim directory is regenerated, and A's after.
func TestLeavingAProjectRemovesItsProjectBinFromPath(t *testing.T) {
	nvxHome := tempDir(t)
	projectA := filepath.Join(tempDir(t), "a")
	projectB := filepath.Join(tempDir(t), "b")
	for _, p := range []string{projectA, projectB} {
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "package.json"), []byte(`{}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Standing in B, with A's project-bin still on PATH from an earlier visit.
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWd) }()
	if err := os.Chdir(projectB); err != nil {
		t.Fatal(err)
	}
	// B's directory is derived the way CleanAndBuildPath derives it: from the
	// working directory as the OS reports it, which on macOS resolves the
	// temp directory's symlink and so hashes differently from the path given
	// to Chdir.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	binA := projectBinDir(projectA, nvxHome)
	binB := projectBinDir(findProjectRoot(cwd), nvxHome)
	for _, d := range []string{binA, binB} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	system := filepath.Join(tempDir(t), "system")
	incoming := strings.Join([]string{binA, system}, string(filepath.ListSeparator))

	got := CleanAndBuildPath(incoming, nvxHome, "", "")
	parts := filepath.SplitList(got)

	if !containsDir(parts, binB) {
		t.Fatalf("the current project's shims are not on PATH: %v", parts)
	}
	if containsDir(parts, binA) {
		t.Fatalf("the previous project's shims are still on PATH after leaving it: %v", parts)
	}
	if !containsDir(parts, system) {
		t.Fatalf("an unrelated entry was dropped: %v", parts)
	}
}

func containsDir(parts []string, dir string) bool {
	for _, p := range parts {
		if strings.EqualFold(filepath.Clean(p), filepath.Clean(dir)) {
			return true
		}
	}
	return false
}
