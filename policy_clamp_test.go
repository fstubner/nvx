package main

import "testing"

// TestClampToTrusted verifies that an untrusted project policy cannot weaken
// isolation below the trusted (defaults + global) baseline — the fix for the
// project-policy isolation-downgrade finding.
func TestClampToTrusted(t *testing.T) {
	trusted := DefaultPolicy() // native / proxy / strict / enabled

	// A hostile project policy tries to disable everything.
	weak := DefaultPolicy()
	weak.Isolation.Enabled = false
	weak.Isolation.Provider = "wsl"
	weak.Isolation.Filesystem.Provider = "wsl"
	weak.Isolation.Network.Mode = "open"
	weak.Isolation.Filesystem.Mode = "permissive"

	clampToTrusted(&weak, trusted)

	if !weak.Isolation.Enabled {
		t.Error("isolation.enabled was weakened to false")
	}
	if weak.IsolationProviderName() != "native" {
		t.Errorf("provider downgraded to %q, want native", weak.IsolationProviderName())
	}
	if netModeRank(weak.Isolation.Network.Mode) < netModeRank("proxy") {
		t.Errorf("network.mode downgraded to %q", weak.Isolation.Network.Mode)
	}
	if fsModeRank(weak.Isolation.Filesystem.Mode) < fsModeRank("strict") {
		t.Errorf("filesystem.mode downgraded to %q", weak.Isolation.Filesystem.Mode)
	}
}

// TestClampAllowsTightening verifies a project policy may still make things
// STRICTER than the trusted baseline.
func TestClampAllowsTightening(t *testing.T) {
	trusted := DefaultPolicy() // proxy
	strict := DefaultPolicy()
	strict.Isolation.Network.Mode = "offline" // stricter than proxy

	clampToTrusted(&strict, trusted)

	if strict.Isolation.Network.Mode != "offline" {
		t.Errorf("legitimate tightening to offline was reverted to %q", strict.Isolation.Network.Mode)
	}
}
