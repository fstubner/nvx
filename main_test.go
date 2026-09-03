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
	// Safe to run before m.Run() despite this function's standing warning about
	// per-probe setup, because it is gated on NVX_PROBE -- which is not in
	// windowsAllowedEnvKeys, so a contained child never has it and never reaches
	// this branch. NVX_HOME is not propagated either, so a child could not fail
	// the check even if it did.
	if os.Getenv("NVX_PROBE") == "1" {
		if problem := probeSocketHeadroomProblem(GetHomeDir()); problem != "" {
			fmt.Fprintln(os.Stderr, problem)
			os.Exit(1)
		}
	}
	code := m.Run()
	cleanupProbeChildBinary()
	os.Exit(code)
}
