//go:build windows

package main

import (
	"errors"
	"strings"
	"testing"
)

// requireContainedRunLaunched is requireAppContainerLaunch for a probe that runs
// the nvx BINARY rather than launching an AppContainer itself.
//
// Those probes never see the CreateProcess error: nvx catches it, prints
// "AppContainer launch failed: ... Access is denied" and exits non-zero, so the
// refusal arrives as text on stdout. Three probes written on 2026-09-04 and
// 2026-09-05 asserted straight through that and called t.Fatalf, which passed
// locally -- this machine creates AppContainers -- and turned Windows CI red,
// because GitHub-hosted runners cannot create them at all.
//
// Routing the text back through the same decision keeps the property that
// decision exists for: a host that CAN create AppContainers and still refused is
// a finding, not a skip, and the skip reason is one the CI allowlist recognises
// rather than a fourth spelling of "cannot run here".
func requireContainedRunLaunched(t *testing.T, out string) {
	t.Helper()
	requireContainedRunLaunchedT(t, out)
}

// requireContainedRunLaunchedT is the body, over the same narrow interface
// requireAppContainerLaunch uses, so the decision is testable without a host
// that refuses launches.
func requireContainedRunLaunchedT(t launchT, out string) {
	t.Helper()
	const marker = "AppContainer launch failed"
	if !strings.Contains(out, marker) {
		return
	}
	// The one line, not the whole transcript: this becomes the skip reason, and
	// the CI gate reads skip reasons.
	line := out
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, marker) {
			line = strings.TrimSpace(l)
			break
		}
	}
	requireAppContainerLaunch(t, errors.New(line))
}

// The binary-running probes route a refusal the same way a direct launch does.
//
// Verified here rather than only on CI, because the skip path cannot fire on a
// machine that CAN create AppContainers -- which is precisely why three probes
// went green locally and red on GitHub's runners. Without this, the fix for that
// is itself only testable by pushing.
func TestAContainedRunRefusalIsRoutedLikeALaunchRefusal(t *testing.T) {
	const refusal = `nvx: AppContainer launch failed: CreateProcess(AppContainer) ` +
		`exe="C:\probe.exe" cwd="C:\work": Access is denied.`

	t.Run("incapable host skips, with a reason CI recognises", func(t *testing.T) {
		withCapability(t, false, "the control launch was refused")
		v := decideContainedRun(refusal)
		if !v.skipped {
			t.Fatalf("a refusal on a host that cannot create AppContainers did not skip (failed=%v msg=%q)",
				v.failed, v.msg)
		}
		if !strings.Contains(v.msg, "this host cannot create AppContainer children") {
			t.Errorf("the skip reason is not the one the CI allowlist matches: %q", v.msg)
		}
	})

	t.Run("capable host fails, so a real regression is not swallowed", func(t *testing.T) {
		withCapability(t, true, "")
		v := decideContainedRun(refusal)
		if !v.failed {
			t.Fatalf("a refusal on a host that DOES create AppContainers was not a failure "+
				"(skipped=%v msg=%q); a launcher regression would vanish into the skip count", v.skipped, v.msg)
		}
	})

	t.Run("ordinary output is left alone", func(t *testing.T) {
		withCapability(t, false, "irrelevant")
		v := decideContainedRun("RESULT round-trip-ok\n")
		if v.failed || v.skipped {
			t.Errorf("output with no launch failure was diverted (failed=%v skipped=%v msg=%q)",
				v.failed, v.skipped, v.msg)
		}
	})
}

// decideContainedRun runs requireContainedRunLaunched against the recorder.
func decideContainedRun(out string) verdictT {
	var v verdictT
	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ours := r.(stopVerdict); !ours {
					panic(r)
				}
			}
		}()
		requireContainedRunLaunchedT(&v, out)
	}()
	return v
}
