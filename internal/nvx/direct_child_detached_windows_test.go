//go:build windows

package nvx

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// realNodeHome is an NVX_HOME whose default node is a copy of the real node.exe,
// with isolation off so `node` takes the uncontained path.
//
// A copy rather than whatever `node` PATH finds, because on a machine with nvx
// installed that is nvx's own shim, and the test would then run a second,
// installed nvx with its own job object around the command under test.
// process.execPath names the real executable through any shim.
func realNodeHome(t *testing.T) (home, nodeExe string) {
	t.Helper()
	out, err := exec.Command("node", "-p", "process.execPath + '|' + process.version").Output()
	if err != nil {
		t.Skipf("needs a real node on PATH: %v", err)
	}
	execPath, version, ok := strings.Cut(strings.TrimSpace(string(out)), "|")
	if !ok {
		t.Skipf("unexpected node output %q", out)
	}
	home = tempDir(t)
	dir := filepath.Join(home, "versions", "node", version)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatal(err)
	}
	nodeExe = filepath.Join(dir, "node.exe")
	if err := os.WriteFile(nodeExe, data, 0o700); err != nil { // #nosec G306 -- fixture
		t.Fatal(err)
	}
	if err := CreateLink(currentLinkPath(home), dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "policy.json"), []byte(`{"isolation":{"enabled":false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// Only the system directories, so no other node or nvx can be found, and
	// powershell still can, for counting processes.
	sys := os.Getenv("SystemRoot")
	t.Setenv("PATH", filepath.Join(sys, "System32")+";"+filepath.Join(sys, "System32", "WindowsPowerShell", "v1.0"))
	t.Setenv(nvxActiveEnvVar, "")
	return home, nodeExe
}

// detachedChildScript writes a node script that starts a detached, unref'd
// child carrying marker on its command line, the way a script starts a daemon
// or opens a browser. keepRunning makes the script itself wait for ever after.
func detachedChildScript(t *testing.T, dir, marker string, keepRunning bool) string {
	t.Helper()
	src := fmt.Sprintf("const {spawn} = require('child_process');\n"+
		"spawn(process.execPath, ['-e', 'setTimeout(function(){}, 120000)', %q], {detached: true, stdio: 'ignore'}).unref();\n",
		marker)
	if keepRunning {
		src += "setInterval(function(){}, 1000);\n"
	}
	script := filepath.Join(dir, "start-daemon.js")
	if err := os.WriteFile(script, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return script
}

// waitForCount polls until want processes carry needle, and returns the last
// count seen. The job reaps asynchronously once its last handle closes, so a
// single look straight after the command returns proves nothing either way.
func waitForCount(t *testing.T, needle string, want int, within time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(within)
	n := countProcessesWithArgument(t, needle)
	for n != want && time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
		n = countProcessesWithArgument(t, needle)
	}
	return n
}

// A process the command deliberately detaches outlives the command, as it does
// without nvx.
//
// On 2026-10-01 on the development machine, a node script that spawns ping.exe
// with detached:true and exits left 1 ping running when run with node directly,
// and 0 through the shim. The job that reaps an abandoned command
// killed it when nvx closed the job after an ordinary exit. That breaks every
// script that starts a daemon or opens a browser and returns.
func TestDetachedChildOutlivesTheCommandThroughTheShim(t *testing.T) {
	home, _ := realNodeHome(t)
	dir := tempDir(t)
	marker := "nvx-detached-" + filepath.Base(dir)
	t.Cleanup(func() { killProcessesByArgument(t, marker) })
	script := detachedChildScript(t, dir, marker, false)

	if code := runShim("node", []string{script}, home); code != 0 {
		t.Fatalf("the shim exited %d", code)
	}

	// The child exists by the time node exits, since spawn returns after
	// CreateProcess. So a count of 0 that stays 0 means it was killed.
	time.Sleep(2 * time.Second)
	if n := countProcessesWithArgument(t, marker); n != 1 {
		t.Errorf("%d detached children alive 2s after the command exited, want 1. "+
			"nvx killed a process the command deliberately left running.", n)
	}
}

// When nvx ends the command itself, because whatever started nvx has gone, the
// whole tree still goes with it, detached children included. The job exists for
// that orphan, a command nobody is waiting on any more and nothing would ever
// stop. Keeping detached children alive on an ordinary exit must not
// weaken it.
func TestEndingTheCommandStillReapsItsWholeTree(t *testing.T) {
	home, _ := realNodeHome(t)
	dir := tempDir(t)
	marker := "nvx-abandoned-" + filepath.Base(dir)
	t.Cleanup(func() { killProcessesByArgument(t, marker) })
	script := detachedChildScript(t, dir, marker, true)

	done := make(chan int, 1)
	go func() { done <- runShim("node", []string{script}, home) }()

	if n := waitForCount(t, marker, 1, 20*time.Second); n != 1 {
		t.Fatalf("the command's detached child never started (count %d)", n)
	}
	// What the hangup watchdog calls when the client has gone.
	if !endActiveChild() {
		t.Fatal("no active child to end; the command is not running through runDirectChild")
	}
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the shim did not return after its command was ended")
	}

	if n := waitForCount(t, marker, 0, 10*time.Second); n != 0 {
		t.Errorf("%d of the abandoned command's children still running after nvx ended it:\n%s",
			n, listProcessesWithArgument(t, marker))
	}
}
