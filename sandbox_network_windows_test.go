//go:build windows

package main

import "testing"

// TestWindowsSandboxNetworkDefaultUsesTheRelay pins the property the egress
// allowlist depends on: in the default network mode the AppContainer is granted no
// network capability at all.
//
// This assertion is the inverse of the one it replaces. Until 0.5.0 the default
// granted internetClient and connected directly, so HTTP_PROXY was a request the
// target could decline and the documented allowlist was not enforced. The old test
// pinned that, and said in its own comment that a failure caused by the default
// becoming proxied was good news. This is that change.
//
// The capability is what matters here, not the flag: with internetClient granted,
// a package that calls connect() directly reaches any host regardless of what the
// relay does. Measured without it (see the egress primitives probe): direct TCP is
// refused by the OS and DNS does not resolve.
func TestWindowsSandboxNetworkDefaultUsesTheRelay(t *testing.T) {
	caps, useRelay := windowsSandboxNetwork("proxy")
	if !useRelay {
		t.Error("the default no longer routes through the egress relay; egress would be unrestricted")
	}
	if len(caps) != 0 {
		t.Errorf("default capabilities = %v, want none: any network capability lets the target bypass the relay entirely", caps)
	}
}

// TestWindowsSandboxNetworkOfflineGrantsNothing covers the modes that were already
// enforced without elevation: no network capability means no network at all.
func TestWindowsSandboxNetworkOfflineGrantsNothing(t *testing.T) {
	for _, mode := range []string{"offline", "loopback", "OFFLINE", " loopback "} {
		caps, useRelay := windowsSandboxNetwork(mode)
		if len(caps) != 0 {
			t.Errorf("mode %q granted capabilities %v, want none", mode, caps)
		}
		if useRelay {
			t.Errorf("mode %q should not start an egress relay; it has no egress", mode)
		}
	}
}

// TestWindowsSandboxNetworkOpenIsTheOnlyDirectMode records the one escape hatch.
// network.mode "open" is the documented way to opt out of the allowlist, and it is
// the only mode that may hand the container a network capability -- if any other
// mode starts doing so, the allowlist stops being enforced for it.
func TestWindowsSandboxNetworkOpenIsTheOnlyDirectMode(t *testing.T) {
	caps, useRelay := windowsSandboxNetwork("open")
	if useRelay {
		t.Error("network.mode \"open\" should connect directly, not through the relay")
	}
	if len(caps) != 1 || caps[0] != capabilityInternetClientSID {
		t.Errorf("open-mode capabilities = %v, want exactly [internetClient]", caps)
	}

	for _, mode := range []string{"proxy", "offline", "loopback", "", "  "} {
		if caps, _ := windowsSandboxNetwork(mode); len(caps) != 0 {
			t.Errorf("mode %q granted %v; only \"open\" may grant a network capability", mode, caps)
		}
	}
}
