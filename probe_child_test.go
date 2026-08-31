package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

// The AppContainer probes run this test binary as their own contained child:
// each one re-launches it with -test.run and an env var, and the test's body
// branches into the child half and exits. That is why there is no separate probe
// program to keep in step with them.
//
// Under -race it is also why the release gate was flaky. The child inherits the
// instrumentation, so every contained launch starts another ThreadSanitizer
// process, and a gate run does about forty of them. Measured on this machine:
// the test binary peaks at 531MB resident under -race against 72MB without, and
// the system's free commit charge fell to 1701MB during a -race run against a
// 65447MB limit. When a launch landed in one of those troughs the child could not
// start, and the probe reported it as a containment result.
//
// The failures said so outright once the whole run was kept rather than the
// summary: "The paging file is too small for this operation to complete" from
// CreateProcess, "ThreadSanitizer failed to allocate 79364096 bytes (error code:
// 1455)" -- 1455 is ERROR_COMMITMENT_LIMIT -- and icacls exiting 0xc0000142,
// STATUS_DLL_INIT_FAILED. Ten runs with -race failed three times; ten runs
// without it, back to back on the same machine, failed none.
//
// The parent keeps -race, which is the half that has caught real races. The child
// does not need it: it is the subject of the sandbox, not the code being checked
// for races, and nothing it does is asserted by the race detector. So under -race
// the child is built once per run as a separate, uninstrumented copy.
var (
	probeChildOnce sync.Once
	probeChildDir  string
	probeChildPath string
	probeChildErr  error
)

// probeChildBinary returns an executable that behaves exactly like this test
// binary when invoked with -test.run, but without race instrumentation.
//
// Without -race that IS this test binary, and nothing is built.
func probeChildBinary() (string, error) {
	if !probeChildIsInstrumented {
		return os.Executable()
	}
	probeChildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "nvx-probe-child-")
		if err != nil {
			probeChildErr = fmt.Errorf("temp dir for the uninstrumented probe child: %w", err)
			return
		}
		out := filepath.Join(dir, "nvxprobechild"+exeSuffix())
		cmd := exec.Command("go", "test", "-c", "-o", out, ".")
		// GOFLAGS is cleared deliberately. A GOFLAGS containing -race would put the
		// instrumentation straight back into the child, silently, and the build
		// would still succeed -- so the one thing this function exists to prevent
		// would be undone with nothing to show for it.
		cmd.Env = append(os.Environ(), "GOFLAGS=")
		if combined, err := cmd.CombinedOutput(); err != nil {
			_ = os.RemoveAll(dir)
			probeChildErr = fmt.Errorf("build an uninstrumented probe child: %w: %s", err, combined)
			return
		}
		probeChildDir, probeChildPath = dir, out
	})
	return probeChildPath, probeChildErr
}

func cleanupProbeChildBinary() {
	if probeChildDir != "" {
		_ = os.RemoveAll(probeChildDir)
	}
}

func exeSuffix() string {
	if os.Getenv("GOOS") == "windows" || filepath.Ext(os.Args[0]) == ".exe" {
		return ".exe"
	}
	return ""
}

// requireProbeChildBinary fails the test if the child could not be produced.
//
// A failure here is not a skip. Staging the child is setup, and a transient read
// error is forgiven by stageProbeChild -- but "the toolchain could not build the
// child at all" is a broken checkout or a missing Go, and skipping every
// containment probe on that would report a clean run for a gate that checked
// nothing.
func requireProbeChildBinary(t *testing.T) string {
	t.Helper()
	path, err := probeChildBinary()
	if err != nil {
		t.Fatalf("cannot produce the contained probe child: %v", err)
	}
	return path
}
