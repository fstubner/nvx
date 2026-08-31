//go:build linux

package main

import "testing"

// The two readers that decide whether Linux containment happens at all.
//
// normalizePolicy now hands them a canonical value, so in principle neither has
// to trim. They trim anyway, and this asserts it, because both fail OPEN on an
// unrecognised string: networkModeRequiresNamespace returns false, so no network
// namespace is created, and seccompFilterForMode returns wanted=false, so
// applyLinuxNetworkSeccomp returns nil having installed nothing — reporting
// success to a caller that has no other way to tell.
//
// A single defence against a fail-open is not enough when the input arrives from
// a file a project ships. This is the second one.
//
// These live in a _linux.go file, so this test only builds and runs on Linux —
// which is the point: the defect was found by reading the code on Windows, where
// none of it compiles, and CI is what actually executes it.
func TestLinuxNetworkReadersTolerateUntrimmedModes(t *testing.T) {
	needsNamespace := []string{
		"proxy", "offline", "loopback",
		"proxy ", " offline", "\tloopback\n", "OFFLINE ", " Proxy ",
	}
	for _, mode := range needsNamespace {
		if !networkModeRequiresNamespace(mode) {
			t.Errorf("networkModeRequiresNamespace(%q) = false; the sandbox would run with the host's network", mode)
		}
	}

	// open is the only mode that legitimately gets no namespace.
	for _, mode := range []string{"open", "open ", " OPEN "} {
		if networkModeRequiresNamespace(mode) {
			t.Errorf("networkModeRequiresNamespace(%q) = true; open must not be given a namespace", mode)
		}
	}

	// An unrecognised mode still gets none, which is why normalizePolicy has to
	// guarantee this is never reached with a typo — recorded so the coupling is
	// visible rather than discovered again.
	if networkModeRequiresNamespace("offlin") {
		t.Error("an unrecognised mode was treated as needing a namespace; that is not what this arm does")
	}
}

func TestSeccompFilterIsChosenForUntrimmedModes(t *testing.T) {
	restricted := []string{
		"offline", "loopback", "proxy",
		"offline ", " loopback", "\tproxy\n", "OFFLINE ",
	}
	for _, mode := range restricted {
		filter, wanted := seccompFilterForMode(mode)
		if !wanted {
			t.Errorf("seccompFilterForMode(%q) wanted=false; applyLinuxNetworkSeccomp would return nil having installed nothing", mode)
			continue
		}
		if len(filter) == 0 {
			t.Errorf("seccompFilterForMode(%q) wanted a filter but returned an empty one", mode)
		}
	}

	for _, mode := range []string{"open", "open ", ""} {
		if _, wanted := seccompFilterForMode(mode); wanted {
			t.Errorf("seccompFilterForMode(%q) wanted a filter; that mode asks for none", mode)
		}
	}
}
