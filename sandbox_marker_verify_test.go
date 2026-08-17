package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestContainmentDisprovedOutsideSandbox is the forged-marker case: an ordinary
// process, with a writable home, must be provable as uncontained so an ambient
// NVX_SANDBOX=1 cannot disable containment.
func TestContainmentDisprovedOutsideSandbox(t *testing.T) {
	home := realHomeDir()
	if home == "" {
		t.Skip("no resolvable home directory on this host")
	}
	probe, err := os.CreateTemp(home, ".nvx-writable-check-*")
	if err != nil {
		t.Skipf("home %q is not writable here, so the probe cannot be exercised: %v", home, err)
	}
	_ = probe.Close()
	_ = os.Remove(probe.Name())

	if !containmentDisproved() {
		t.Error("a normal process with a writable home should be provably uncontained; a forged NVX_SANDBOX would be honoured")
	}
}

// TestRealHomeDirIgnoresEnvironmentOverrides is the property the whole check rests
// on. The sandbox redirects HOME and USERPROFILE to the guest home, which IS
// writable -- so a probe that trusted those variables would report "not contained"
// from inside the sandbox and cause every nested nvx to re-sandbox.
func TestRealHomeDirIgnoresEnvironmentOverrides(t *testing.T) {
	before := realHomeDir()
	if before == "" {
		t.Skip("no resolvable home directory on this host")
	}

	fake := t.TempDir()
	t.Setenv("HOME", fake)
	t.Setenv("USERPROFILE", fake)

	after := realHomeDir()
	if after != before {
		t.Errorf("realHomeDir changed with HOME/USERPROFILE overridden: %q -> %q; the probe would follow the guest home and misreport containment", before, after)
	}
	if filepath.Clean(after) == filepath.Clean(fake) {
		t.Errorf("realHomeDir returned the overridden path %q", fake)
	}
}

// TestContainmentDisprovedIsInconclusiveWithoutAHome pins the fail-safe direction:
// when the home cannot be resolved the answer must be "cannot tell", which leaves
// the marker trusted rather than forcing a sandbox.
func TestContainmentDisprovedIsInconclusiveWithoutAHome(t *testing.T) {
	if realHomeDir() == "" && containmentDisproved() {
		t.Error("with no resolvable home the check must be inconclusive, not a positive disproof")
	}
}
