package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Shims embedded os.Executable() -- whichever binary ran `init-shims`. Generate
// them from a build tree and every shim depends on a file that gets rebuilt,
// moved or deleted, after which `npm` fails with a missing-file error naming a
// path in someone's source directory. That happened three times in one session on
// the machine this was written on: each smoke-test run repointed the real
// ~/.nvx/bin shims at a repo build, which was then deleted.
//
// Shims now point at <nvxHome>/bin/nvx, which is where the installer puts nvx and
// is a path nvx owns.
func TestShimsPointAtTheInstalledCopyNotTheRunningBinary(t *testing.T) {
	nvxHome := tempDir(t)

	target := stableShimTarget(nvxHome)

	wantDir := filepath.Join(nvxHome, "bin")
	if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(wantDir)) {
		t.Fatalf("shim target %q is outside %q; shims would depend on a path nvx does not control", target, wantDir)
	}

	// And the copy has to be there, or the shims point at nothing.
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("no nvx installed at the shim target: %v", err)
	}

	// A truncated binary is worse than a missing one: it fails at exec time, far
	// from the cause. The first version of this reused a size-capped log copier
	// and produced exactly that -- an 8 MB "nvx.exe" from a 10.7 MB source.
	self, err := os.Executable()
	if err != nil {
		t.Skipf("cannot locate the test binary: %v", err)
	}
	selfInfo, err := os.Stat(self)
	if err != nil {
		t.Skipf("cannot stat the test binary: %v", err)
	}
	if info.Size() != selfInfo.Size() {
		t.Errorf("installed copy is %d bytes but the source is %d; a truncated binary fails at exec time",
			info.Size(), selfInfo.Size())
	}
}

// Generating shims twice must be stable, and must not accumulate temp files.
func TestShimTargetIsStableAcrossRuns(t *testing.T) {
	nvxHome := tempDir(t)

	first := stableShimTarget(nvxHome)
	second := stableShimTarget(nvxHome)
	if first != second {
		t.Errorf("shim target changed between runs: %q then %q", first, second)
	}

	entries, err := os.ReadDir(filepath.Join(nvxHome, "bin"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("left a temporary file behind: %s", e.Name())
		}
	}
}

// The generated shims must run the stable target: a POSIX shim script names it,
// a Windows shim is a hard link to it.
func TestGeneratedShimsNameTheStableTarget(t *testing.T) {
	nvxHome := tempDir(t)
	if err := generateShims(nvxHome); err != nil {
		t.Fatalf("generateShims: %v", err)
	}

	shimDir := filepath.Join(nvxHome, "bin")
	target := filepath.Join(shimDir, nvxExecutableName())

	// One shim is enough to prove the target is threaded through; they are all
	// written from the same value.
	if runtime.GOOS == "windows" {
		if !sameExistingFile(filepath.Join(shimDir, "npm.exe"), target) {
			t.Errorf("npm.exe is not the installed nvx at %q", target)
		}
		return
	}
	body, err := os.ReadFile(filepath.Join(shimDir, "npm"))
	if err != nil {
		t.Fatalf("read shim: %v", err)
	}
	if !strings.Contains(string(body), target) {
		t.Errorf("shim npm does not invoke the installed nvx at %q; it contains:\n%s", target, body)
	}
}
