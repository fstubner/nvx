//go:build windows

package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// probeSocketHeadroomProblem reports why an NVX_HOME cannot host this suite's
// probes, or "" when it can.
//
// Several probes run the real nvx, which builds a guest home under NVX_HOME and
// binds an AF_UNIX socket inside it. sun_path is 108 bytes on Windows too, so a
// long NVX_HOME puts that socket out of reach and every one of those probes
// fails -- measured 2026-09-03 with an 87-character NVX_HOME: four failures,
// TestExposedPortIsReachableFromTheHost, TestContainedMcpServerCompletesHandshake
// and both streaming-stdio probes, none of which is about sockets or paths.
//
// nvx's own message is precise ("the egress socket path is 129 bytes, over the
// 107-byte AF_UNIX limit") and it does reach the test output, buried in each
// failure. Four late failures carrying it are still four things to read before
// the one sentence that matters.
//
// Checked rather than skipped. A hosted runner refusing to create AppContainers
// cannot be fixed by the person running the gate; this can, by exporting a
// shorter NVX_HOME, and skipping four containment probes for it would be the
// silent hole this suite has spent a lot of effort closing.
func probeSocketHeadroomProblem(nvxHome string) string {
	// The longest path a probe will bind: the guest home is the sandbox home plus
	// a session id, and session ids are a fixed 16 hex characters.
	guest := filepath.Join(getSandboxHomeDir(nvxHome), strings.Repeat("0", 16))
	sock := windowsEgressSocketPath(guest)
	if egressSocketPathFits(sock) {
		return ""
	}
	return fmt.Sprintf(
		"NVX_HOME is too long for this suite's probes.\n"+
			"  NVX_HOME:      %s (%d characters)\n"+
			"  needs a socket at: %s (%d bytes)\n"+
			"  AF_UNIX allows:    %d bytes\n"+
			"Every probe that runs the real nvx will fail on this, and none of them is about\n"+
			"paths. Re-run with a shorter home, for example:\n"+
			"  $env:NVX_HOME='C:\\nvxgate'; $env:NVX_PROBE=1; go test -race -timeout 40m .",
		nvxHome, len(nvxHome), sock, len(sock), unixSocketPathMax-1)
}
