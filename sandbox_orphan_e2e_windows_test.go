//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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
// a LIVE child, after the client that started it has exited.
//
// Every shortcut here has already produced a false pass, so none are taken. An
// early manual check looked fine while the child had failed to start, so nvx was
// about to exit anyway. A later version closed nvx's stdin from the test process
// itself, which left the parent alive and so described a finished PIPELINE --
// something nvx must deliberately not kill, and the mirror of this test in
// sandbox_parent_watch_windows_test.go now covers it.
//
// So this drives the real binary through a real intermediate client process that
// really exits, and requires nvx to end on its own.
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

	// A stand-in MCP client: it starts nvx over a pipe, the way a real client
	// does, and then dies.
	//
	// The client has to be a separate process that actually exits. An earlier
	// version of this test just closed nvx's stdin from the test process and
	// called that the orphan case -- but the test process stayed alive, so it
	// was really testing a finished pipeline, which nvx must NOT kill. It passed
	// against a watchdog that killed healthy pipelines, and would have kept
	// passing after that bug was fixed only by accident.
	client := filepath.Join(dir, "client.js")
	clientSrc := "const {spawn} = require('child_process');\n" +
		// argv[0] is node and argv[1] is this script, so the arguments start at 2.
		"spawn(process.argv[2], ['--no-sandbox', 'node', process.argv[3]], {stdio: ['pipe', 'inherit', 'inherit']});\n" +
		"setTimeout(function(){ process.exit(0); }, 4000);\n"
	if err := os.WriteFile(client, []byte(clientSrc), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("node", client, nvxExe, script)
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot run node to stand in for an MCP client: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("the stand-in client failed: %v", err)
	}
	// The client is now gone, leaving nvx with a broken stdin and a dead parent
	// while its own child runs on -- the exact state that stranded 38 processes.

	deadline := time.Now().Add(stdinBrokenPipeInterval + 30*time.Second)
	for time.Now().Before(deadline) {
		if countProcessesRunning(t, nvxExe) == 0 {
			return // nvx left, and the Job Object takes its child with it
		}
		time.Sleep(time.Second)
	}

	t.Errorf("nvx was still running %v after its client exited. This is the leak that filled the "+
		"machine with 38 orphaned processes.", stdinBrokenPipeInterval+30*time.Second)
	killProcessesByImage(t, nvxExe)
}

// countProcessesRunning counts live processes started from exePath.
//
// Matched on the executable path rather than the name: the machine this runs on
// has a real nvx installation whose processes must not be confused with the
// throwaway binary this test built.
func countProcessesRunning(t *testing.T, exePath string) int {
	t.Helper()
	out, err := exec.Command("powershell", "-NoProfile", "-Command",
		"@(Get-Process -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq '"+exePath+"' }).Count",
	).Output()
	if err != nil {
		t.Skipf("cannot enumerate processes on this host: %v", err)
	}
	n, convErr := strconv.Atoi(strings.TrimSpace(string(out)))
	if convErr != nil {
		t.Skipf("unexpected process count output %q: %v", string(out), convErr)
	}
	return n
}

func killProcessesByImage(t *testing.T, exePath string) {
	t.Helper()
	_ = exec.Command("powershell", "-NoProfile", "-Command",
		"Get-Process -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq '"+exePath+"' } | Stop-Process -Force",
	).Run()
}
