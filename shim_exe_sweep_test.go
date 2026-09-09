package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// What asideName writes is what sweepStaleShimExes removes.
//
// Two halves of one decision, written in two places: a file is renamed aside
// under one name and deleted later by a glob that has to match it. Nothing fails
// loudly if they stop agreeing -- upgrades keep working, and the bin directory
// quietly grows a copy of nvx every time, which is exactly the state this was
// written to clear.
func TestAsideFilesAreTheOnesTheSweepRemoves(t *testing.T) {
	dir := tempDir(t)

	aside := asideName(filepath.Join(dir, "nvx.exe"))
	if err := os.WriteFile(aside, []byte("old binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	shimAside := asideName(filepath.Join(dir, "npm.exe"))
	if err := os.WriteFile(shimAside, []byte("old shim"), 0o600); err != nil {
		t.Fatal(err)
	}

	sweepStaleShimExes(dir)

	for _, p := range []string{aside, shimAside} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s survived the sweep, so it would accumulate on every upgrade", filepath.Base(p))
		}
	}
}

// The sweep takes only what it put there.
//
// It globs a directory that holds every shim and nvx itself, so a pattern that
// reached further would delete the working binary rather than the spare copy.
func TestTheSweepLeavesEverythingElseAlone(t *testing.T) {
	dir := tempDir(t)
	keep := []string{"nvx.exe", "npm.exe", "node.exe", "nvx.exe.sha256", "policy.json"}
	for _, name := range keep {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("live"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	sweepStaleShimExes(dir)

	for _, name := range keep {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("the sweep removed %s, which nothing renamed aside", name)
		}
	}
}

// A file that cannot be replaced is renamed aside, and the new one takes its
// name.
//
// The rename path is what Windows needs and what this test can reach on any
// platform: replaceFile is called with a destination it cannot rename over only
// on Windows, so what is checked here is the outcome both platforms must share --
// dst holds the new content afterwards, and nothing was lost.
func TestReplaceFilePutsTheNewContentInPlace(t *testing.T) {
	dir := tempDir(t)
	dst := filepath.Join(dir, "nvx.exe")
	if err := os.WriteFile(dst, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(dir, "nvx.exe.tmp")
	if err := os.WriteFile(tmp, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := replaceFile(tmp, dst); err != nil {
		t.Fatalf("replaceFile: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("the destination still holds %q; the upgrade installed nothing", got)
	}
}

// An upgrade never leaves the destination missing.
//
// replaceFile moves the old file aside before putting the new one in place, so
// there is a moment with nothing at dst. If the second rename fails there, the
// original goes back: for a shim the alternative is a command gone from PATH, and
// for nvx.exe it is every shim in the directory pointing at nothing.
//
// The failure is forced by handing it a source that does not exist, which is the
// one way to make the second rename fail without needing a running binary.
func TestAFailedReplaceRestoresTheOriginal(t *testing.T) {
	dir := tempDir(t)
	dst := filepath.Join(dir, "nvx.exe")
	if err := os.WriteFile(dst, []byte("the working binary"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := replaceFile(filepath.Join(dir, "does-not-exist"), dst)
	if err == nil {
		t.Fatal("replacing from a missing source reported success")
	}

	got, rerr := os.ReadFile(dst)
	if rerr != nil {
		t.Fatalf("the destination is gone after a failed replace: %v", rerr)
	}
	if string(got) != "the working binary" {
		t.Fatalf("the destination holds %q; the original was not restored", got)
	}
	// And nothing was left behind under the aside name.
	matches, _ := filepath.Glob(filepath.Join(dir, "*"+asideSuffix+"*"))
	if len(matches) != 0 {
		t.Errorf("a failed replace left %v behind", matches)
	}
}

// The suffix is distinctive enough not to collide with a real command name.
func TestTheAsideSuffixCannotBeARealShimName(t *testing.T) {
	for _, cmd := range allShimCommands() {
		if strings.Contains(cmd, asideSuffix) {
			t.Errorf("the shim %q contains the aside marker, so the sweep would delete it", cmd)
		}
	}
}
