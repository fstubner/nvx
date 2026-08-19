package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The escape an independent acceptance pass found, and the two properties that
// close it. It needs both: either alone leaves the attack open.
//
// `nvx use` puts the project-bin directory near the front of PATH, ahead of
// System32. That directory used to live inside the project, so a contained
// postinstall could write a file called `git` into it and wait. The next `git`
// the developer typed ran that file with their full user token — every credential
// store, every project, unrestricted network. No sandbox bug: the containment
// held and was routed around by a directory nvx itself put on PATH.

// TestProjectBinDirIsNotInsideTheProject is the first half. A directory a
// contained process can write to must not be on the developer's PATH.
func TestProjectBinDirIsNotInsideTheProject(t *testing.T) {
	project := t.TempDir()
	nvxHome := t.TempDir()

	dir := projectBinDir(project, nvxHome)

	if dirWithin(dir, project) {
		t.Errorf("project-bin is inside the project (%s); a contained install can write there, "+
			"and it sits ahead of System32 on PATH", dir)
	}
	if !dirWithin(dir, nvxHome) {
		t.Errorf("project-bin is outside nvxHome (%s); nvxHome is the directory the sandbox "+
			"cannot write", dir)
	}
	// Two projects must not collide, or one project could plant for another.
	other := t.TempDir()
	if projectBinDir(other, nvxHome) == dir {
		t.Error("two different projects share one project-bin directory")
	}
}

// TestPlantedBinaryDoesNotBecomeAShim is the second half. node_modules/.bin is
// itself writable by a contained install, so relocating the directory is not
// enough on its own: a postinstall can create node_modules/.bin/git and wait for
// the next regeneration to shim it onto PATH.
func TestPlantedBinaryDoesNotBecomeAShim(t *testing.T) {
	project := t.TempDir()
	nvxHome := t.TempDir()
	binDir := filepath.Join(project, "node_modules", ".bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}

	// A name that certainly resolves on PATH already: the Go toolchain running
	// this test. Using a real one keeps the test honest about what "shadow" means.
	shadow := "go"
	if resolveCommandOnPath(shadow, os.Getenv("PATH")) == "" {
		t.Skip("no `go` on PATH to shadow")
	}
	for _, name := range []string{shadow, shadow + ".cmd"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("payload"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// And an ordinary project CLI, which must still be wrapped.
	for _, name := range []string{"nvx-fixture-cli", "nvx-fixture-cli.cmd"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("real"), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	if err := generateProjectBinShims(project, nvxHome); err != nil {
		t.Fatalf("generateProjectBinShims: %v", err)
	}

	shimDir := projectBinDir(project, nvxHome)
	if _, err := os.Stat(filepath.Join(shimDir, shadow)); err == nil {
		t.Errorf("a planted node_modules/.bin/%s was shimmed onto PATH ahead of the real one; "+
			"typing %s would run whatever the install left there", shadow, shadow)
	}
	if _, err := os.Stat(filepath.Join(shimDir, "nvx-fixture-cli")); err != nil {
		t.Errorf("an ordinary project CLI was not wrapped: %v; the feature is gone rather than fixed", err)
	}
}

// TestProjectBinPruningRemovesWhatIsNoLongerThere covers the third way a file
// reaches that directory: generation only ever added, so anything that got in
// stayed on PATH forever, including entries from a package since removed.
func TestProjectBinPruningRemovesWhatIsNoLongerThere(t *testing.T) {
	project := t.TempDir()
	nvxHome := t.TempDir()
	binDir := filepath.Join(project, "node_modules", ".bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "nvx-fixture-cli"), []byte("real"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := generateProjectBinShims(project, nvxHome); err != nil {
		t.Fatal(err)
	}

	shimDir := projectBinDir(project, nvxHome)
	stray := filepath.Join(shimDir, "left-over-from-somewhere")
	if err := os.WriteFile(stray, []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := generateProjectBinShims(project, nvxHome); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stray); err == nil {
		t.Error("a file with no matching node_modules/.bin entry survived regeneration and stays on PATH")
	}
	if _, err := os.Stat(filepath.Join(shimDir, "nvx-fixture-cli")); err != nil {
		t.Errorf("pruning removed a current entry: %v", err)
	}
}

// TestCleanAndBuildPathUsesTheRelocatedDir pins the wiring. If PATH still pointed
// at the in-project directory, relocating would have achieved nothing.
func TestCleanAndBuildPathUsesTheRelocatedDir(t *testing.T) {
	project := t.TempDir()
	nvxHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "package.json"), []byte(`{"name":"p"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(projectBinDir(project, nvxHome), 0o700); err != nil {
		t.Fatal(err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Skipf("cannot chdir into the fixture project: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	got := CleanAndBuildPath("", nvxHome, "", "")
	for _, entry := range filepath.SplitList(got) {
		if strings.Contains(strings.ToLower(filepath.Clean(entry)),
			strings.ToLower(filepath.Join(".nvx", "project-bin"))) &&
			dirWithin(entry, project) {
			t.Errorf("PATH still contains the in-project shim dir %q", entry)
		}
	}
}
