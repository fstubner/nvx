package main

import (
	"os"
	"path/filepath"
	"runtime"
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

// writeExec creates an executable file (0755) so Unix resolution accepts it.
func writeExec(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0755); err != nil { // #nosec G306 -- test fixture
		t.Fatal(err)
	}
}
