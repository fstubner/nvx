//go:build !windows

package nvx

import (
	"os"
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
// neither. Measured on Linux, where no other node was reachable: the install
// died with "Could not find real executable for node". Measured on macOS, where
// one was: the contained process ran the pinned v22.23.2 while a nested lookup
// silently got the machine's v24.20.0.
//
// Not built on Windows: it puts the launched runtime's directory on the
// contained PATH already, through containedEnv, and a test that only ever skips
// there trips the probe gate that treats an unexplained skip as a failure.
func TestTheContainedPathCarriesTheRuntimeBinDir(t *testing.T) {
	nvxHome := tempDir(t)
	binDir := filepath.Join(nvxHome, "versions", "node", "v22.0.0", "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cmdPath := filepath.Join(binDir, "npm")

	got := withRuntimeBinOnPath([]string{"PATH=/usr/bin:/bin", "HOME=/guest"}, cmdPath, nvxHome)

	parts := filepath.SplitList(pathOf(t, got))
	if len(parts) == 0 || parts[0] != binDir {
		t.Fatalf("the runtime's bin directory does not lead the contained PATH: %v", parts)
	}
	if !strings.Contains(pathOf(t, got), "/usr/bin") {
		t.Fatalf("the rest of the PATH was dropped: %q", pathOf(t, got))
	}
}

// A command that is not an nvx-managed runtime does not get its directory
// added: the point is to make nvx's own runtimes resolvable, not to widen what
// a contained process can reach by name.
func TestAnUnmanagedCommandDirIsNotAddedToTheContainedPath(t *testing.T) {
	nvxHome := tempDir(t)
	before := []string{"PATH=/usr/bin:/bin"}

	got := withRuntimeBinOnPath(before, "/opt/some-tool/bin/tool", nvxHome)

	if got[0] != before[0] {
		t.Fatalf("PATH changed for a command outside nvx's runtimes: %q", got[0])
	}
}

// Both runtimes, not only the one being launched.
//
// Measured on Windows, which already had the launched runtime's directory on
// the contained PATH: inside a contained bun, a script calling `node` fell
// through to the machine's own node and the container denied it; inside a
// contained node, `bun` could not be resolved at all. A postinstall that calls
// the other runtime is an ordinary thing to write.
func TestTheContainedPathCarriesTheOtherRuntimeToo(t *testing.T) {
	nvxHome := tempDir(t)
	nodeBin := filepath.Join(nvxHome, "versions", "node", "v22.0.0", "bin")
	bunBin := filepath.Join(nvxHome, "versions", "bun", "v1.4.2", "bin")
	for _, d := range []string{nodeBin, bunBin} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// A global default for bun, the way `nvx default bun` records one, so the
	// other-runtime lookup has something to resolve.
	for _, f := range []string{filepath.Join(bunBin, "bun"), filepath.Join(bunBin, "bunx")} {
		if err := os.WriteFile(f, []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Dir(bunBin), runtimeCurrentLinkPath(nvxHome, "bun")); err != nil {
		t.Skipf("cannot record a global default without symlinks here: %v", err)
	}

	got := withRuntimeBinOnPath([]string{"PATH=/usr/bin"}, filepath.Join(nodeBin, "npm"), nvxHome)

	parts := filepath.SplitList(pathOf(t, got))
	if len(parts) < 2 || parts[0] != nodeBin {
		t.Fatalf("the launched runtime does not lead the contained PATH: %v", parts)
	}
	if parts[1] != bunBin {
		t.Fatalf("the other runtime is not on the contained PATH: %v", parts)
	}
}

func pathOf(t *testing.T, env []string) string {
	t.Helper()
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			return strings.TrimPrefix(e, "PATH=")
		}
	}
	t.Fatal("no PATH in the environment")
	return ""
}
