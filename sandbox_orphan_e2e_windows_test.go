//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The regression: nvx blocked on a child that never exits, after the client
// that started it has gone.
//
// Measured 2026-08-21 on the development machine: 48 live nvx processes, 38
// orphaned, each holding a supervisor inside an AppContainer, accumulating about
// one per 60-80 seconds until Windows ran out of commit charge. Nearly all were
// `nvx shim npx <an MCP server>`.
//
// The unit tests either side of this cover whether a broken pipe is detected and
// whether the watchdog arms on the right handle shapes. Neither covers the case
// that actually stranded those processes: nvx sitting in WaitForSingleObject on
// a LIVE child while its own stdin hangs up. An earlier manual check looked like
// it passed, but the child had failed to start, so nvx was about to exit anyway
// and the observation proved nothing.
//
// So this drives the real binary: give it a long-lived child, close its stdin,
// and require the process to end on its own.
func TestNvxExitsWhenItsClientDisappears(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the nvx binary; skipped under -short")
	}

	dir := tempDir(t)
	nvxExe := filepath.Join(dir, "nvx.exe")
	build := exec.Command("go", "build", "-o", nvxExe, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("cannot build nvx for this test: %v\n%s", err, out)
	}

	// A child that will not stop on its own, standing in for an MCP server that
	// ignores stdin EOF -- which is why nvx was left waiting in the first place.
	script := filepath.Join(dir, "loop.js")
	if err := os.WriteFile(script, []byte("setInterval(function(){}, 1000);\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(nvxExe, "--no-sandbox", "node", script)
	cmd.Dir = dir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("could not start nvx: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	// Let the child get going, so the wait being interrupted is the thing under
	// test rather than a race with startup.
	select {
	case err := <-done:
		t.Fatalf("nvx exited before the client disappeared, so this proves nothing about "+
			"the orphan case: %v", err)
	case <-time.After(6 * time.Second):
	}

	// The client going away.
	_ = stdin.Close()

	select {
	case <-done:
		// nvx noticed and left. Its children go with it: the Job Object it
		// assigns them to is set to kill on last-handle-close, which process
		// exit does.
	case <-time.After(stdinBrokenPipeInterval + 30*time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("nvx was still running %v after its stdin hung up. This is the leak that filled "+
			"the machine with 38 orphaned processes.", stdinBrokenPipeInterval+30*time.Second)
	}
}
