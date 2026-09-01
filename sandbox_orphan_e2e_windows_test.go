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

	// Reap the grandchild whatever happens below, including when an assertion
	// fails. Without this the test leaks one `node loop.js` per run for ever:
	// 92 of them were found on the development machine on 2026-09-01, holding 78
	// consoles open and 1.6 GB between them -- left by the very test that checks
	// nvx does not strand processes. Killing them released 76 of the 78 consoles,
	// which is what identified these as the source.
	t.Cleanup(func() { killProcessesByArgument(t, script) })

	deadline := time.Now().Add(stdinBrokenPipeInterval + 30*time.Second)
	for time.Now().Before(deadline) && countProcessesRunning(t, nvxExe) != 0 {
		time.Sleep(time.Second)
	}
	if countProcessesRunning(t, nvxExe) != 0 {
		t.Errorf("nvx was still running %v after its client exited. This is the leak that filled the "+
			"machine with 38 orphaned processes.", stdinBrokenPipeInterval+30*time.Second)
		killProcessesByImage(t, nvxExe)
		return
	}

	// nvx leaving is only half of the fix, and for a long time this test checked
	// only that half. It stopped at the line above with the comment "nvx left,
	// and the Job Object takes its child with it" -- which was false on the path
	// this test drives. superviseProcessTree, the only thing that creates a
	// reaping job, is called from the AppContainer launch alone; the client here
	// starts nvx with --no-sandbox, which never reaches it. So nvx exited, the
	// test passed, and the child ran on for ever. The leak had not been fixed,
	// only moved down one level where nothing was looking.
	//
	// A few seconds of slack because the job reaps asynchronously once nvx's last
	// handle closes.
	childDeadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(childDeadline) && countProcessesWithArgument(t, script) != 0 {
		time.Sleep(500 * time.Millisecond)
	}
	if n := countProcessesWithArgument(t, script); n != 0 {
		t.Errorf("nvx exited but left %d of its own children running. An MCP server started "+
			"through nvx therefore outlives the client that asked for it, which is the same "+
			"leak measured from the other end: the process nobody is waiting on and nobody kills.\n"+
			"Survivors:\n%s", n, listProcessesWithArgument(t, script))
	}
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

// countProcessesWithArgument counts live processes whose command line contains
// needle.
//
// Matched on the command line rather than the image name because the process
// being counted is nvx's child, not this test's: the test never holds a handle
// to it, and every one of them is the same node.exe as dozens of unrelated
// processes on a developer machine. The script path is unique per run, since
// tempDir gives each run its own directory, so it names exactly this run's child.
//
// String.Contains rather than -like: -like would read a [ or * in the temp path
// as a wildcard, and a needle that quietly matches nothing is how a check like
// this passes while measuring an empty set.
//
// The $PID exclusion is not incidental. The query passes the needle on its own
// command line, so it matches ITSELF: without that clause the count never
// reaches zero and this check fails no matter how correct nvx is -- a test that
// can only ever fail, which is as useless as one that can only ever pass. It was
// caught because the first failure said "2 children" where one was expected.
func countProcessesWithArgument(t *testing.T, needle string) int {
	t.Helper()
	out, err := exec.Command("powershell", "-NoProfile", "-Command",
		"@(Get-CimInstance Win32_Process | Where-Object { $_.ProcessId -ne $PID -and $_.CommandLine -and "+
			"$_.CommandLine.Contains("+powershellSingleQuote(needle)+") }).Count",
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

// listProcessesWithArgument describes the survivors, for a failure message that
// says which processes were left rather than only how many.
func listProcessesWithArgument(t *testing.T, needle string) string {
	t.Helper()
	out, err := exec.Command("powershell", "-NoProfile", "-Command",
		"Get-CimInstance Win32_Process | Where-Object { $_.ProcessId -ne $PID -and $_.CommandLine -and "+
			"$_.CommandLine.Contains("+powershellSingleQuote(needle)+") } | "+
			"ForEach-Object { \"  pid=$($_.ProcessId) ppid=$($_.ParentProcessId) $($_.Name): $($_.CommandLine)\" }",
	).Output()
	if err != nil {
		return "  (could not enumerate: " + err.Error() + ")"
	}
	return strings.TrimRight(string(out), "\r\n")
}

func killProcessesByArgument(t *testing.T, needle string) {
	t.Helper()
	_ = exec.Command("powershell", "-NoProfile", "-Command",
		"Get-CimInstance Win32_Process | Where-Object { $_.ProcessId -ne $PID -and $_.CommandLine -and "+
			"$_.CommandLine.Contains("+powershellSingleQuote(needle)+") } | "+
			"ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }",
	).Run()
}

// powershellSingleQuote renders s as a PowerShell single-quoted literal, in
// which the only character with meaning is the quote itself.
func powershellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
