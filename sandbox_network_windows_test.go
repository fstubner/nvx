//go:build windows

package main

import "testing"

// TestWindowsSandboxNetworkDefaultIsUnproxied pins what the documentation now
// says, so the two cannot drift apart again silently.
//
// F2: README.md and enforcement-matrix.md claimed Windows egress was restricted to
// the policy allowlist. It is not, unless an elevated `nvx setup` has added a
// loopback exemption -- an AppContainer cannot reach a loopback listener without
// one. By default nvx grants internetClient and (see the caller) strips the proxy
// variables, so the contained process connects directly.
//
// If this test starts failing because the default became proxied, that is good
// news -- update the three documents it references rather than the assertion.
func TestWindowsSandboxNetworkDefaultIsUnproxied(t *testing.T) {
	nvxHome := t.TempDir() // no setup marker, so no loopback exemption

	caps, useProxy := windowsSandboxNetwork(nvxHome, "proxy")
	if useProxy {
		t.Error("default is now proxied; update README.md, SECURITY.md and docs/enforcement-matrix.md, which currently document it as unproxied")
	}
	if len(caps) != 1 || caps[0] != capabilityInternetClientSID {
		t.Errorf("default capabilities = %v, want exactly [internetClient]; the docs describe egress as unrestricted on this basis", caps)
	}
}

// TestWindowsSandboxNetworkOfflineGrantsNothing covers the modes that ARE enforced
// without elevation: with no network capability the container has no network at
// all, which is a real OS-level guarantee rather than a cooperative one.
func TestWindowsSandboxNetworkOfflineGrantsNothing(t *testing.T) {
	for _, mode := range []string{"offline", "loopback", "OFFLINE", " loopback "} {
		caps, useProxy := windowsSandboxNetwork(t.TempDir(), mode)
		if len(caps) != 0 {
			t.Errorf("mode %q granted capabilities %v, want none", mode, caps)
		}
		if useProxy {
			t.Errorf("mode %q should not route through the proxy", mode)
		}
	}
}
