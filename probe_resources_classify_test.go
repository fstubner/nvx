package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// recordingT captures what failIfHostIsOutOfMemory decided, so the decision can
// be asserted without failing the real test.
type recordingT struct {
	failed bool
	msg    string
}

func (r *recordingT) Helper() {}
func (r *recordingT) Fatalf(format string, args ...any) {
	r.failed = true
	r.msg = fmt.Sprintf(format, args...)
}

// The strings below are verbatim from a release-gate run that failed while the
// machine was out of commit charge. They are the evidence this file exists for:
// each one was reported as a test result rather than as a full machine, and two
// of them as a SKIP, so the run went green with containment probes that had not
// run.
func TestHostExhaustionIsRecognisedInTheErrorsItActuallyProduced(t *testing.T) {
	measured := []struct {
		name string
		err  error
	}{
		{
			"CreateProcess for a contained child",
			errors.New(`CreateProcess(AppContainer) exe="C:\\Users\\Felix\\AppData\\Local\\Temp\\nvx207680945\\markerprobe.exe": The paging file is too small for this operation to complete.`),
		},
		{
			"the Go toolchain being spawned to build nvx",
			errors.New(`build nvx: fork/exec C:\Program Files\Go\bin\go.exe: The paging file is too small for this operation to complete.`),
		},
		{
			"a spawned icacls failing to initialise",
			errors.New(`integrity label for "C:\\Users\\Felix\\AppData\\Local\\Temp\\nvx482175809": icacls failed: exit status 0xc0000142 ()`),
		},
		{
			"the race detector's own allocator",
			errors.New("ThreadSanitizer failed to allocate 0x000004bb0000 (79364096) bytes at 0x05c6b7a10000 (error code: 1455)"),
		},
	}

	for _, m := range measured {
		t.Run(m.name, func(t *testing.T) {
			hint := resourceExhaustionHint(m.err)
			if hint == "" {
				t.Fatalf("not recognised as the host running out of memory: %v", m.err)
			}
			var rec recordingT
			failIfHostIsOutOfMemory(&rec, "the probe", m.err)
			if !rec.failed {
				t.Fatal("recognised the exhaustion but did not fail; a skip here is how a green run hides a probe that never ran")
			}
			if !strings.Contains(rec.msg, "NOT because containment regressed") {
				t.Fatalf("the failure does not tell the reader this is the machine, not a defect: %q", rec.msg)
			}
		})
	}
}

// The complement, and the reason this is not just a substring match on "memory":
// a real containment failure must still be reported as one.
func TestOrdinaryFailuresAreNotBlamedOnTheHost(t *testing.T) {
	ordinary := []error{
		nil,
		errors.New("CreateProcess(AppContainer): Access is denied."),
		errors.New("CreateProcess(AppContainer): The system cannot find the file specified."),
		errors.New("child reported: SSHKEY=READ, expected DENIED"),
		errors.New("listen tcp 127.0.0.1:0: bind: address already in use"),
	}
	for _, err := range ordinary {
		if hint := resourceExhaustionHint(err); hint != "" {
			t.Fatalf("blamed the host for an ordinary failure %v: %s", err, hint)
		}
		var rec recordingT
		failIfHostIsOutOfMemory(&rec, "the probe", err)
		if rec.failed {
			t.Fatalf("failed the run over %v, which is not an exhaustion signature", err)
		}
	}
}

// The Go runtime's own wording, added after a full gate run died with it and
// zero tests executed while the host had 1,902MB of free commit out of 65,447.
// A run that dies before any test runs is the case with the least evidence in the
// log, so the one line it does print has to be recognised.
func TestTheGoRuntimesOwnAllocationFailureIsRecognised(t *testing.T) {
	err := errors.New("fatal error: runtime: cannot allocate memory")
	if hint := resourceExhaustionHint(err); hint == "" {
		t.Fatal("the Go runtime's allocation failure was not recognised as the host running out of memory")
	}
	// ...and the near-miss stays a near-miss: this must not swallow ordinary
	// failures that merely mention memory.
	for _, ordinary := range []string{
		"cannot allocate a new session id",
		"the sandbox could not allocate a port",
	} {
		if hint := resourceExhaustionHint(errors.New(ordinary)); hint != "" {
			t.Errorf("%q was blamed on the host: %s", ordinary, hint)
		}
	}
}
