package main

import (
	"fmt"
	"os"
	"testing"
)

// TestMain exists to drop the uninstrumented probe child built for a -race run.
//
// It is deliberately the only thing here. Tests in this package re-run the test
// binary as a contained child, so anything done before m.Run() runs again inside
// every AppContainer probe -- which is how a global setup step becomes a
// per-probe one nobody meant to write.
func TestMain(m *testing.M) {
	// The host-capability control starts this binary inside an AppContainer to
	// learn whether CreateProcess can start a process there at all. It has to do
	// that silently.
	//
	// The child's stdout and stderr are this test process's own, and `go test`
	// reads them: a child that reached the testing package printed "testing:
	// warning: no tests to run" and a bare "PASS", and cmd/go tagged the whole
	// package "[no tests to run]" on the gate's summary line -- the one line a
	// maintainer reads, made indistinguishable from a run in which nothing
	// executed. Exiting here produces no output at all, and still proves the only
	// thing the control asks: that the process started and ran our code.
	if os.Getenv("NVX_HOST_CONTROL_CHILD") == "1" {
		os.Exit(0)
	}
	// One early, legible failure instead of four late confusing ones when
	// NVX_HOME cannot hold an AF_UNIX socket. See probeSocketHeadroomProblem.
	//
	// Gated on NVX_HOME being set EXPLICITLY, which is the whole point of this
	// function's standing warning about doing work before m.Run(). A first
	// version keyed on NVX_PROBE alone, reasoning that a contained child never
	// has it. It does: several probes re-run this binary inside the sandbox and
	// must pass NVX_PROBE=1 through, or the child's own gate skips instead of
	// asserting. Inside that child HOME points at the guest home, so GetHomeDir()
	// returns <guestHome>/.nvx at 82 characters, the check fired, and TestMain
	// exited 1 -- killing TestOneSandboxSessionCannotReadAnother and the relay
	// allowlist probe, two containment tests, on a machine with nothing wrong
	// with it.
	//
	// NVX_HOME is not in windowsAllowedEnvKeys, so a contained child never has
	// one. Asking whether it is set is therefore both the correct discriminator
	// and the honest condition: the message tells the reader to shorten a
	// variable, which only means anything if they set it.
	if os.Getenv("NVX_HOME") != "" && os.Getenv("NVX_PROBE") == "1" {
		if problem := probeSocketHeadroomProblem(GetHomeDir()); problem != "" {
			fmt.Fprintln(os.Stderr, problem)
			os.Exit(1)
		}
	}
	code := m.Run()
	cleanupProbeChildBinary()
	os.Exit(code)
}
