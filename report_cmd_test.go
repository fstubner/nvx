package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `nvx report` collects the whole picture, checked through the built binary.
//
// Two things this catches that a direct call cannot. The command has to be
// reachable -- a case in the dispatch that was never added would leave `nvx
// report` printing the help, and every unit test would still pass. And doctor's
// output has to be captured rather than printed: it writes its heading and its
// [OK]/[FAIL] rows to stdout while the Log* helpers write to stderr, so the
// first version of this captured stderr alone, put the advice in the file, left
// the verdicts on the terminal, and lost the half a reader needs.
func TestTheReportCollectsWhatSomeoneElseWouldNeed(t *testing.T) {
	exe := filepath.Join(tempDir(t), "nvx"+exeSuffixForTest())
	if out, err := runGoBuild(exe); err != nil {
		t.Skipf("cannot build nvx here: %v\n%s", err, out)
	}

	home := tempDir(t)
	work := tempDir(t)
	out := filepath.Join(work, "report.txt")

	cmd := execCommandForTest(exe, "report", "--out="+out)
	cmd.Dir = work
	cmd.Env = append(os.Environ(), "NVX_HOME="+home, "NVX_TRACE=", "NVX_DEBUG=")
	terminal, _ := cmd.CombinedOutput()

	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("nvx report wrote no file (is the command wired into the dispatch?): %v\nnvx said:\n%s",
			err, terminal)
	}
	report := string(body)

	for _, section := range []string{
		"interception (nvx doctor)", "policy in effect", "audit.log", "debug.log", "nvx version:",
	} {
		if !strings.Contains(report, section) {
			t.Errorf("the report has no %q section:\n%s", section, report)
		}
	}

	// The verdict rows are the point of including doctor at all, and they are the
	// half that went to stdout.
	if !strings.Contains(report, "[OK]") && !strings.Contains(report, "[FAIL]") {
		t.Errorf("the report contains no doctor verdict rows, so doctor's stdout was not captured. "+
			"Report:\n%s", report)
	}
	// ...and having been captured, they must not ALSO have hit the terminal.
	if strings.Contains(string(terminal), "shim interception") {
		t.Errorf("doctor's output printed to the terminal as well as going into the report:\n%s", terminal)
	}

	// Someone about to send this is told what is in it.
	if !strings.Contains(string(terminal), "before sending it") {
		t.Errorf("nothing warned that the report carries paths and package names:\n%s", terminal)
	}

	// With no capture on disk, the report says how to get one rather than leaving
	// an empty section that looks like nothing happened.
	if !strings.Contains(report, "NVX_DEBUG=1") {
		t.Errorf("the report does not say how to capture more detail:\n%s", report)
	}
}

// The capture is written when asked for, and not otherwise, through the real
// binary rather than the function.
func TestTheDebugCaptureRecordsARunOnlyWhenAskedTo(t *testing.T) {
	exe := filepath.Join(tempDir(t), "nvx"+exeSuffixForTest())
	if out, err := runGoBuild(exe); err != nil {
		t.Skipf("cannot build nvx here: %v\n%s", err, out)
	}

	home := tempDir(t)
	logPath := filepath.Join(home, "debug.log")

	run := func(debug string) {
		cmd := execCommandForTest(exe, "doctor")
		cmd.Dir = tempDir(t)
		cmd.Env = append(os.Environ(), "NVX_HOME="+home, "NVX_TRACE=", "NVX_DEBUG="+debug)
		_, _ = cmd.CombinedOutput()
	}

	run("")
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("a capture file exists after a run with NVX_DEBUG unset; the default is not off")
	}

	run("1")
	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("NVX_DEBUG=1 recorded nothing: %v", err)
	}
	// doctor on an empty home always has something to say; the value of the
	// capture is that the rendered text is there, not just an event name.
	if !strings.Contains(string(body), "shim dir") && !strings.Contains(string(body), "PATH") {
		t.Errorf("the capture holds no rendered output from the run:\n%s", body)
	}
	before := len(body)

	run("")
	after, _ := os.ReadFile(logPath)
	if len(after) != before {
		t.Errorf("a run with NVX_DEBUG unset still wrote to the capture (%d -> %d bytes)", before, len(after))
	}
}
