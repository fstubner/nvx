//go:build windows

package main

// Opt-in measurement probe (NVX_PROBE=1) for F1 / "Fix A": where does AppContainer
// setup latency actually go?
//
// The 2026-07-20 design measured a ~70s per-launch stall and attributed it to
// grantWorkdirAncestors re-granting every ancestor of the workdir and guest home
// on every launch, with 4 of 5 ancestor grants hanging to their full icacls
// timeout. That measurement predates several rounds of changes, so this re-times
// each phase against today's code rather than trusting it.

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func timePhase(t *testing.T, name string, fn func() error) time.Duration {
	t.Helper()
	start := time.Now()
	err := fn()
	d := time.Since(start)
	status := "ok"
	if err != nil {
		status = "ERR: " + err.Error()
	}
	t.Logf("%-34s %8.2fs   %s", name, d.Seconds(), status)
	return d
}

func TestMeasureAppContainerSetupPhases(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (creates a throwaway AppContainer profile, mutates ACLs)")
	}

	const probeProfile = "nvx.sandbox.timingprobe"
	sid, err := ensureAppContainerSID(probeProfile)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	defer deleteAppContainerProfile(probeProfile)

	// Mirror the real layout: the guest home sits deep under the user profile, so
	// the ancestor walk has several levels to climb.
	nvxHome := filepath.Join(os.Getenv("USERPROFILE"), ".nvx-timingprobe")
	guestHome := filepath.Join(nvxHome, "sandbox_home", "probesession")
	if err := os.MkdirAll(guestHome, 0o700); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(nvxHome)

	workDir := t.TempDir()

	var total time.Duration
	total += timePhase(t, "grant guest home (M)", func() error {
		return grantAppContainerPath(sid, guestHome)
	})
	total += timePhase(t, "labelLowIntegrity /t (no timeout)", func() error {
		return labelLowIntegrity(guestHome)
	})
	total += timePhase(t, "grant workdir (M)", func() error {
		return grantAppContainerPath(sid, workDir)
	})
	total += timePhase(t, "ancestors of workdir", func() error {
		grantWorkdirAncestors(sid, "", workDir)
		return nil
	})
	total += timePhase(t, "ancestors of guest home", func() error {
		grantWorkdirAncestors(sid, "", guestHome)
		return nil
	})

	// Second pass: appContainerHasGrant should make repeats near-free. If it does
	// not, the presence check is not working and every launch pays full price.
	t.Log("--- second pass (should be near-free if the presence check works) ---")
	var repeat time.Duration
	repeat += timePhase(t, "grant guest home (repeat)", func() error {
		return grantAppContainerPath(sid, guestHome)
	})
	repeat += timePhase(t, "ancestors of guest home (repeat)", func() error {
		grantWorkdirAncestors(sid, "", guestHome)
		return nil
	})

	t.Logf("TOTAL first pass: %.2fs", total.Seconds())
	t.Logf("TOTAL repeat:     %.2fs", repeat.Seconds())
}
