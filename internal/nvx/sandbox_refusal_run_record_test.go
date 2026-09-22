package nvx

import (
	"testing"
)

// A refusal is not recorded as a contained run.
//
// The run record's mode is set to "sandboxed" before the sandbox is asked to
// start, and nothing corrected it afterwards. Measured 2026-09-20 against the
// installed shim on a host that could not launch an AppContainer, `npm install`
// wrote both of these:
//
//	{"event":"sandbox_not_started","reason":"the appcontainer launch failed",...}
//	{"event":"run","command":"npm","mode":"sandboxed","exit":"1",...}
//
// One log, one command, two contradictory answers to "was this contained?".
// Reading the run line alone -- which is what `nvx audit` shows and what any
// tool counting contained runs would total -- says a command ran inside the
// sandbox when it never started.
//
// Pinned at the seam rather than by launching a sandbox: every refusal in
// runNativeSandbox and runSandbox funnels through sandboxDidNotStart, and what
// broke was that it told nobody. A real launch needs a host that can refuse one
// on demand, which is the thing that cannot be arranged in a test.
func TestARefusalIsNotRecordedAsAContainedRun(t *testing.T) {
	home := t.TempDir()
	trace := &runTrace{nvxHome: home, command: "npm", record: true, top: true}
	trace.note(runModeSandboxed, "")

	config := SandboxConfig{
		NvxHome:   home,
		Command:   "npm",
		OnRefusal: func(reason string) { trace.note(runModeRefused, reason) },
	}
	sandboxDidNotStart(config, "the appcontainer launch failed", 1)

	if trace.mode == runModeSandboxed {
		t.Fatal("a command that never started is still recorded as having run contained; " +
			"the audit log would report containment nvx did not deliver")
	}
	if trace.mode != runModeRefused {
		t.Fatalf("the run was recorded as %q, want %q", trace.mode, runModeRefused)
	}
	if trace.reason != "the appcontainer launch failed" {
		t.Fatalf("the run record does not say why containment failed: %q", trace.reason)
	}
}

// Every refusal inside runSandbox reaches the run record.
//
// The callback is only useful if the paths that refuse actually call it, and
// runSandbox's own early returns did not: they pre-date sandboxDidNotStart and
// returned a bare 1, so an unknown provider or a failed egress proxy left the
// record saying "sandboxed" with no refusal logged anywhere. This drives the
// real dispatcher through one of them.
func TestRunSandboxReportsItsOwnRefusalsToTheRunRecord(t *testing.T) {
	home := t.TempDir()
	var gotReason string

	code := captureStderrDiscarded(t, func() int {
		return runSandbox(SandboxConfig{
			NvxHome:            home,
			Command:            "does-not-matter",
			FilesystemProvider: "definitely-not-a-provider",
			OnRefusal:          func(reason string) { gotReason = reason },
		})
	})

	if code == 0 {
		t.Fatal("an unknown containment provider was accepted")
	}
	if gotReason == "" {
		t.Fatal("runSandbox refused the run and told the run record nothing; the record " +
			"keeps the \"sandboxed\" it was given before the sandbox was asked to start")
	}
}

// captureStderrDiscarded runs fn with stderr silenced and returns its result.
func captureStderrDiscarded(t *testing.T, fn func() int) int {
	t.Helper()
	var code int
	captureStderrHere(t, func() { code = fn() })
	return code
}
