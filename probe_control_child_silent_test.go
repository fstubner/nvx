package main

import (
	"os"
	"os/exec"
	"testing"
)

// The host-capability control child must write nothing.
//
// It runs with this process's own stdout and stderr, and `go test` parses those
// to decide what to say about the package. A child that reached the testing
// package emitted "testing: warning: no tests to run" and a bare "PASS", and
// cmd/go stamped the package summary "[no tests to run]" -- so the gate's
// headline read exactly like a run in which no test executed, on a project whose
// two preceding commits were both about the gate reporting something it had not
// checked.
//
// Asserting on the bytes rather than on the argument list, because the argument
// list looked correct while it was broken: the failure was that the child got far
// enough to speak at all, which only the output shows. Run as an ordinary
// subprocess -- no AppContainer needed, since what is being pinned is the child's
// silence, not its containment.
func TestTheHostControlChildWritesNothing(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Skipf("cannot locate this test binary: %v", err)
	}
	cmd := exec.Command(self)
	cmd.Env = append(os.Environ(), "NVX_HOST_CONTROL_CHILD=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the control child exited non-zero (%v), so a healthy host would read as incapable: %s", err, out)
	}
	if len(out) != 0 {
		t.Fatalf("the control child wrote %d bytes, which go test will attribute to this package "+
			"and can put on the gate's summary line: %q", len(out), out)
	}

	// Control: the invocation this replaced must still be noisy.
	//
	// Without it, a binary that had become silent for some unrelated reason -- a
	// changed testing package, a build that exits early -- would pass the check
	// above while proving nothing. This is also the exact command that caused the
	// regression, so it doubles as a record of what the bug was.
	//
	// A pattern matching no test runs no test, so there is no recursion here. That
	// is worth stating: disabling the guard above instead makes the child run this
	// very test, which spawns another child, and the first attempt at a sabotage
	// check forked 78 processes before it was killed.
	noisy := exec.Command(self, "-test.run=NvxHostControlMatchesNoTest")
	noisy.Env = append(os.Environ(), "NVX_HOST_CONTROL_CHILD=")
	if prev, err := noisy.CombinedOutput(); err == nil && len(prev) == 0 {
		t.Fatal("the old -test.run invocation is silent too, so the check above does not " +
			"discriminate and would pass however the child were launched")
	}
}
