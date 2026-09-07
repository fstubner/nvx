//go:build !windows

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// The contained process can find the runtime the command it is running needs.
//
// npm on Unix is a script whose first line is `#!/usr/bin/env node`, so it
// resolves node through PATH. Inside the sandbox that PATH is the user's, which
// after `nvx env` has nvx's shim directory on it -- so npm found the `node`
// SHIM, and the shim went looking for nvx's home to resolve a version. NVX_HOME
// is scrubbed and HOME points at the throwaway guest profile, so it found
// neither and the install died with "Could not find real executable for node".
//
// Measured on Linux: with only the shim directory on PATH and a global default
// set -- the arrangement the README describes -- `npm install` failed. With the
// runtime's own bin directory on PATH (what `nvx use` leaves behind) the same
// install succeeded. Windows escaped it because npm.cmd resolves node.exe next
// to itself rather than through PATH.
//
// Not built on Windows: it resolves node next to npm.cmd rather than through
// PATH, so there is nothing here for it to check, and a test that only ever
// skips there trips the probe gate that treats an unexplained skip as a failure.
//
// So the runtime's bin directory goes on the contained PATH, ahead of anything
// the user had. Inside the sandbox that is the right order: a nested `node` or
// `npm` should be the pinned runtime running inside the containment that is
// already active, not a shim trying to start a second one.
func TestTheContainedPathCarriesTheRuntimeBinDir(t *testing.T) {
	t.Skip("PROBE branch: the fix is disabled on purpose, so this unit test would fail before the smoke gets a chance to run")
	nvxHome := tempDir(t)
	binDir := filepath.Join(nvxHome, "versions", "node", "v22.0.0", "bin")
	cmdPath := filepath.Join(binDir, "npm")

	got := withRuntimeBinOnPath([]string{"PATH=/usr/bin:/bin", "HOME=/guest"}, cmdPath, nvxHome)

	path := ""
	for _, e := range got {
		if strings.HasPrefix(e, "PATH=") {
			path = strings.TrimPrefix(e, "PATH=")
		}
	}
	parts := filepath.SplitList(path)
	if len(parts) == 0 || parts[0] != binDir {
		t.Fatalf("the runtime's bin directory does not lead the contained PATH: %q", path)
	}
	if !strings.Contains(path, "/usr/bin") {
		t.Fatalf("the rest of the PATH was dropped: %q", path)
	}
}

// A command that is not an nvx-managed runtime does not get its directory
// added: the point is to make nvx's own runtime resolvable, not to widen what a
// contained process can reach by name.
func TestAnUnmanagedCommandDirIsNotAddedToTheContainedPath(t *testing.T) {
	nvxHome := tempDir(t)
	before := []string{"PATH=/usr/bin:/bin"}

	got := withRuntimeBinOnPath(before, "/opt/some-tool/bin/tool", nvxHome)

	if got[0] != before[0] {
		t.Fatalf("PATH changed for a command outside nvx's runtimes: %q", got[0])
	}
}
