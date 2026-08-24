//go:build windows

package main

import (
	"strings"
	"testing"
)

// The loopback-exemption warning, tested on a machine that does not have one.
//
// This warning is the only mitigation nvx offers for a hole its own documentation
// calls serious: with an exemption registered, contained code reaches every
// service on 127.0.0.1 and anything there that forwards traffic makes the egress
// allowlist meaningless. Until 2026-08-24 the only test for it,
// TestExemptMachineIsWarnedAbout, skipped unless the machine ALREADY carried an
// exemption -- so it skipped in CI and on every healthy developer machine, while
// docs/enforcement-matrix.md said the check was "pinned by" it.
//
// Registering a real exemption to test properly needs elevation and would leave
// the machine less safe than it was found. listLoopbackExemptSIDs is a variable
// instead, so the exempt branch can be reached without touching the machine --
// the same seam seatbeltExecPath provides for the macOS fail-closed test.
//
// The companion probe still exists and still asserts against the machine's real
// list. These cover the branch it cannot reach.

func withExemptSIDs(t *testing.T, sids []string) {
	t.Helper()
	orig := listLoopbackExemptSIDs
	listLoopbackExemptSIDs = func() ([]string, error) { return sids, nil }
	t.Cleanup(func() { listLoopbackExemptSIDs = orig })
}

func TestSandboxIsLoopbackExemptDetectsThisSandboxsSID(t *testing.T) {
	const sid = "S-1-15-2-1111111111-2222222222-3333333333-4444444444-5555555555-6666666666-7777777777"
	home := tempDir(t)

	withExemptSIDs(t, []string{"S-1-15-2-9999", sid, "S-1-15-2-8888"})
	exempt, err := sandboxIsLoopbackExempt(home, sid)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !exempt {
		t.Fatal("an exemption for this sandbox's own SID was not detected; the warning would never fire")
	}
}

// A machine with OTHER AppContainers exempted is not this sandbox's problem, and
// reporting it would be a false alarm that teaches people to ignore the warning.
// A real machine carried four unrelated exemptions -- a Windows WebView host and
// three orphans from uninstalled apps -- and an earlier version of a probe script
// matched the SID prefix rather than the SID and duly reported the problem as
// still present after it had been fixed.
func TestSandboxIsLoopbackExemptIgnoresOtherAppContainers(t *testing.T) {
	const ours = "S-1-15-2-1111111111-2222222222-3333333333-4444444444-5555555555-6666666666-7777777777"
	home := tempDir(t)

	withExemptSIDs(t, []string{
		"S-1-15-2-1310292540-1029022339-4008023048-2190398717-53961996-4257829345-603366646", // a Windows component
		"S-1-15-2-490905099-2794809881-2632752266-3514030558-4166392763-3416490339-321513134", // an orphan
	})
	exempt, err := sandboxIsLoopbackExempt(home, ours)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exempt {
		t.Error("another AppContainer's exemption was read as this sandbox's; that is a false alarm on a healthy machine")
	}
}

// The doctor line is what a user is told to run, and it had no test at all. It
// must report the finding AND name the exact removal command, because removing an
// exemption needs elevation and a SID nobody can be expected to retype.
func TestDoctorReportsAnExemptSandboxWithItsRemovalCommand(t *testing.T) {
	home := tempDir(t)
	sidStr, err := deriveAppContainerSIDString(stableSandboxProfile)
	if err != nil {
		t.Skipf("cannot derive the sandbox SID on this host: %v", err)
	}

	withExemptSIDs(t, []string{sidStr})
	out := captureStdout(t, func() {
		if !reportSandboxWeakeners(home) {
			t.Error("doctor did not report an exempt sandbox, so `nvx doctor` would exit 0 on a weakened machine")
		}
	})

	if !strings.Contains(out, "[FAIL]") {
		t.Errorf("the finding must be a FAIL, or it does not count against health:\n%s", out)
	}
	if !strings.Contains(out, "CheckNetIsolation LoopbackExempt -d") {
		t.Errorf("the removal command must be printed; it needs elevation and a SID:\n%s", out)
	}
	if !strings.Contains(out, sidStr) {
		t.Errorf("the printed command must carry THIS sandbox's SID, or it removes the wrong exemption:\n%s", out)
	}
}

// And the healthy machine stays quiet. A warning that fires when nothing is wrong
// is worse than none, because it is the one people learn to scroll past.
func TestDoctorSaysNothingWhenNoExemptionIsRegistered(t *testing.T) {
	home := tempDir(t)
	withExemptSIDs(t, []string{"S-1-15-2-9999"})
	out := captureStdout(t, func() {
		if reportSandboxWeakeners(home) {
			t.Error("doctor reported a weakener on a machine with no nvx exemption")
		}
	})
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected silence on a healthy machine, got:\n%s", out)
	}
}
