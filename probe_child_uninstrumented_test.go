package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestReportsItsOwnRaceBuildTag is the child half of the check below: run with
// NVX_REPORT_RACE=1 it prints how it was built and exits.
//
// Asking the binary rather than inspecting its bytes. The first version of this
// check searched the file for the string "ThreadSanitizer", which is present in
// every build of this package for the dullest possible reason -- this file
// mentions it -- so the check failed against a child that was in fact correct. A
// build tag the binary reports about itself cannot be confused with a build tag
// something merely talks about.
func TestReportsItsOwnRaceBuildTag(t *testing.T) {
	if os.Getenv("NVX_REPORT_RACE") != "1" {
		t.Skip("child-side helper for TestTheContainedProbeChildCarriesNoRaceInstrumentation")
	}
	os.Stdout.WriteString("nvx-race-build=" + boolWord(probeChildIsInstrumented) + "\n")
	os.Exit(0)
}

func boolWord(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// The child the probes actually launch must not carry race instrumentation.
//
// Checking the built artifact rather than the build command, because the failure
// this guards against is precisely one where the command looks right and the
// binary is instrumented anyway: a GOFLAGS carrying -race, a toolchain default, a
// future edit that stages os.Executable() again. Any of those would restore the
// flakiness silently, with the gate still green.
func TestTheContainedProbeChildCarriesNoRaceInstrumentation(t *testing.T) {
	if !probeChildIsInstrumented {
		t.Skip("not a -race build; the child is this binary and is not instrumented")
	}
	path, err := probeChildBinary()
	if err != nil {
		t.Fatalf("cannot produce the contained probe child: %v", err)
	}

	ask := func(exe string) string {
		t.Helper()
		cmd := exec.Command(exe, "-test.run=TestReportsItsOwnRaceBuildTag")
		cmd.Env = append(os.Environ(), "NVX_REPORT_RACE=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("asking %s how it was built: %v: %s", exe, err, out)
		}
		for _, line := range strings.Split(string(out), "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(line), "nvx-race-build="); ok {
				return v
			}
		}
		t.Fatalf("%s did not report its build tag; output was %q", exe, out)
		return ""
	}

	if got := ask(path); got != "false" {
		t.Fatalf("the contained probe child at %s reports race-build=%s; every contained launch would "+
			"start a race-instrumented process and the gate would be flaky under memory pressure again", path, got)
	}

	// Control: the same question put to THIS binary must answer "true". Without it,
	// a check that always reads "false" -- a broken helper, a swallowed env var --
	// would be indistinguishable from the check passing.
	self, err := os.Executable()
	if err != nil {
		t.Skipf("cannot locate this test binary to confirm the check discriminates: %v", err)
	}
	if got := ask(self); got != "true" {
		t.Fatalf("this binary is a -race build but reports race-build=%s; the check above proves nothing", got)
	}
}
