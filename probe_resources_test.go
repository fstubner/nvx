package main

import (
	"fmt"
	"strings"
)

// Telling "the machine ran out of memory" apart from "the code under test is
// wrong".
//
// A release-gate run that exhausts the system commit charge does not report
// itself as such. It reports whatever happened to be allocating at that instant,
// which over three failing runs was five different things: CreateProcess and
// fork/exec saying "The paging file is too small for this operation to
// complete", ThreadSanitizer saying "failed to allocate ... (error code: 1455)",
// icacls exiting 0xc0000142, a fresh net.Listen failing, and interface
// enumeration coming back empty. Only the first two name memory at all.
//
// The cost of not naming it was not that the runs failed. It was that two of the
// five faces are SKIPS -- an empty interface list skipped both egress-allowlist
// tests, and a failed launch skipped the containment marker probe -- so the run
// went green with security probes that had not executed, and the failing faces
// were read as regressions in whichever unrelated test was unlucky.
//
// So the signatures that DO name memory are matched, and every report carries the
// machine's commit headroom at the moment it was taken. The second half matters
// more than the first: it means a future failure that wears a sixth face still
// arrives with the number that explains it, instead of costing another
// investigation to discover the machine was full.

// exhaustionSignatures are Win32 failures that say outright that the host is out
// of memory. Matched on text because they arrive as exit codes and wrapped
// errors from several layers -- a spawned icacls, a CreateProcess call, the Go
// toolchain -- with no common error type to test for.
var exhaustionSignatures = []struct{ substr, meaning string }{
	{"The paging file is too small for this operation to complete", "ERROR_COMMITMENT_LIMIT: the system commit charge is exhausted"},
	{"Not enough memory resources are available", "ERROR_NOT_ENOUGH_MEMORY"},
	{"Not enough storage is available to process this command", "ERROR_OUTOFMEMORY"},
	{"0xc0000142", "STATUS_DLL_INIT_FAILED: a child process could not initialise, which on this host means it could not get memory"},
	{"error code: 1455", "ERROR_COMMITMENT_LIMIT reported by the race detector's allocator"},
	{"failed to allocate", "an allocator refused a reservation"},
}

// resourceExhaustionHint returns a non-empty explanation when err is the host
// running out of memory rather than a defect in what is being tested. The empty
// string means "this is not one of the known exhaustion signatures", which is
// not the same as "the host is fine" -- see hostMemoryNote.
func resourceExhaustionHint(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	for _, sig := range exhaustionSignatures {
		if strings.Contains(msg, sig.substr) {
			return fmt.Sprintf("%s (%s).%s", sig.substr, sig.meaning, hostMemoryNote())
		}
	}
	return ""
}

// failIfHostIsOutOfMemory turns a recognised exhaustion failure into a clear
// verdict about the machine, and reports nothing otherwise.
//
// A failure rather than a skip, deliberately. The probe genuinely did not run, so
// a skip is defensible in the abstract -- but these arrive in bursts across
// consecutive tests, and a gate that quietly skips a run's worth of containment
// checks because the machine was full is exactly the outcome this file exists to
// stop. Loud and red; the message says plainly that it is not a containment
// regression.
func failIfHostIsOutOfMemory(t testingT, what string, err error) {
	if hint := resourceExhaustionHint(err); hint != "" {
		t.Helper()
		t.Fatalf("%s failed because this machine is out of memory, NOT because containment regressed: %s\n"+
			"Re-run with less running, or without -race, before reading this as a defect.", what, hint)
	}
}

// testingT is the part of *testing.T these helpers need, so they can be unit
// tested for the decision they make rather than only through a real failure.
type testingT interface {
	Helper()
	Fatalf(format string, args ...any)
}

// launchT adds the other half of the decision: these helpers choose between
// failing and skipping, and a test that cannot observe the skip can only check
// half of what they do.
type launchT interface {
	testingT
	Skipf(format string, args ...any)
}
