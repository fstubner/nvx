package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolveCommandOnPath(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()

	// Command name differs by platform: on Windows shims are "<cmd>.cmd".
	shimName := "npm"
	if runtime.GOOS == "windows" {
		shimName = "npm.cmd"
	} else {
		shimName = "npm"
	}
	writeExec(t, filepath.Join(dirA, shimName))
	writeExec(t, filepath.Join(dirB, shimName))

	pathEnv := dirA + string(os.PathListSeparator) + dirB
	got := resolveCommandOnPath("npm", pathEnv)
	want := filepath.Join(dirA, shimName)
	if got != want {
		t.Fatalf("resolveCommandOnPath = %q, want %q (first dir wins)", got, want)
	}

	if resolveCommandOnPath("does-not-exist", pathEnv) != "" {
		t.Fatalf("expected empty for missing command")
	}
}

func TestDiagnosePath(t *testing.T) {
	nvxHome := t.TempDir()
	shimDir := filepath.Join(nvxHome, "bin")
	current := filepath.Join(nvxHome, "current")
	if err := os.MkdirAll(shimDir, 0755); err != nil { // #nosec G301 -- test fixture
		t.Fatal(err)
	}
	if err := os.MkdirAll(current, 0755); err != nil { // #nosec G301 -- test fixture
		t.Fatal(err)
	}

	// Healthy: shim dir first, current after.
	healthy := shimDir + string(os.PathListSeparator) + current
	rep := diagnosePath(healthy, nvxHome, nil)
	if !rep.shimDirOnPath || rep.shimDirIndex != 0 {
		t.Fatalf("healthy: shimDirOnPath=%v index=%d, want true/0", rep.shimDirOnPath, rep.shimDirIndex)
	}
	if len(rep.shadowedBy) != 0 {
		t.Fatalf("healthy: want no shadowing, got %+v", rep.shadowedBy)
	}

	// Broken: current before shim dir -> shadowing reported.
	broken := current + string(os.PathListSeparator) + shimDir
	rep = diagnosePath(broken, nvxHome, nil)
	if !rep.shimDirOnPath || rep.shimDirIndex != 1 {
		t.Fatalf("broken: index=%d, want 1", rep.shimDirIndex)
	}
	if len(rep.shadowedBy) != 1 || rep.shadowedBy[0].index != 0 {
		t.Fatalf("broken: want current shadowing at index 0, got %+v", rep.shadowedBy)
	}

	// Absent: shim dir not on PATH at all.
	rep = diagnosePath(current, nvxHome, nil)
	if rep.shimDirOnPath {
		t.Fatalf("absent: shimDirOnPath should be false")
	}
}

func TestShimPathPrependSnippet(t *testing.T) {
	// POSIX: must reference the bash-form dir and export PATH with it in front.
	bash := shimPathPrependSnippet("bash", "/home/u/.nvx/bin")
	if !strings.Contains(bash, "/home/u/.nvx/bin") {
		t.Fatalf("bash snippet missing shim dir: %s", bash)
	}
	if !strings.Contains(bash, "export PATH=") {
		t.Fatalf("bash snippet must export PATH: %s", bash)
	}

	// PowerShell: must filter the existing entry and reassign $env:PATH.
	ps := shimPathPrependSnippet("powershell", `C:\Users\u\.nvx\bin`)
	if !strings.Contains(ps, `.nvx\bin`) {
		t.Fatalf("powershell snippet missing shim dir: %s", ps)
	}
	if !strings.Contains(ps, "$env:PATH") {
		t.Fatalf("powershell snippet must set $env:PATH: %s", ps)
	}
}

// writeExec creates an executable file (0755) so Unix resolution accepts it.
func writeExec(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0755); err != nil { // #nosec G306 -- test fixture
		t.Fatal(err)
	}
}
