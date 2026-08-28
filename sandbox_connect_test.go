package main

import "testing"

func TestConnectSpecTakesTheHostPortFirst(t *testing.T) {
	// The mirror of --expose, and the order is deliberately the other way round:
	// what the developer knows is the port their service already runs on, so that
	// is the number they type first and the only one they have to type.
	m, err := parseConnectSpec("9222")
	if err != nil {
		t.Fatalf("parseConnectSpec(9222): %v", err)
	}
	if m.Host != 9222 {
		t.Fatalf("host port = %d, want 9222", m.Host)
	}
	if m.Inside != 0 {
		t.Fatalf("in-sandbox port = %d, want 0 (chosen at launch)", m.Inside)
	}

	m, err = parseConnectSpec("9222:19222")
	if err != nil {
		t.Fatalf("parseConnectSpec(9222:19222): %v", err)
	}
	if m.Host != 9222 || m.Inside != 19222 {
		t.Fatalf("got %d:%d, want 9222:19222", m.Host, m.Inside)
	}
}

func TestConnectSpecRefusesAPortMappedToItself(t *testing.T) {
	// An AppContainer shares the host's network stack, so a listener inside the
	// sandbox on 9222 collides with the very service on 9222 it exists to reach.
	// Left to bind, the failure surfaces as EADDRINUSE from a component the
	// developer never asked to run.
	if _, err := parseConnectSpec("9222:9222"); err == nil {
		t.Fatal("9222:9222 was accepted; it cannot work -- both ends want one port")
	}
}

func TestConnectSpecRefusesUnusablePorts(t *testing.T) {
	for _, spec := range []string{"", "http", "0", "-1", "65536", "9222:0", "9222:70000", "9222:x"} {
		if m, err := parseConnectSpec(spec); err == nil {
			t.Errorf("parseConnectSpec(%q) accepted it as %+v", spec, m)
		}
	}
}

func TestConnectPortsDropBadEntriesRatherThanTheRun(t *testing.T) {
	// One unusable entry in a policy file must not take the whole sandbox down:
	// the other ports still work, and the bad one is reported.
	got := normalizeConnectPorts([]string{"9222", "nonsense", "5432:15432", "9222"})
	if len(got) != 2 {
		t.Fatalf("got %d mappings, want 2: %+v", len(got), got)
	}
	if got[0].Host != 9222 || got[1].Host != 5432 || got[1].Inside != 15432 {
		t.Fatalf("unexpected mappings: %+v", got)
	}
}

func TestGrantingAHostPortCountsAsLooseningPolicy(t *testing.T) {
	// Reaching a service on the host is a hole in the containment boundary --
	// the same kind of hole the machine-wide loopback exemption was removed for
	// being. A project policy must not be able to open one without approval.
	before := DefaultPolicy()
	after := DefaultPolicy()
	after.Isolation.Network.ConnectPorts = []string{"9222"}

	if !policyLoosens(before, after) {
		t.Fatal("adding connect_ports did not register as loosening; a project could open a host port unapproved")
	}
	if policyLoosens(after, before) {
		t.Fatal("removing connect_ports registered as loosening")
	}
}

func TestConnectPortsMergeRatherThanReplace(t *testing.T) {
	// A project asking for its own port must not silently drop one the global
	// policy already granted -- and vice versa.
	global := DefaultPolicy()
	global.Isolation.Network.ConnectPorts = []string{"5432"}
	local := DefaultPolicy()
	local.Isolation.Network.ConnectPorts = []string{"9222"}

	merged := MergePolicies(global, local)
	if len(merged.Isolation.Network.ConnectPorts) != 2 {
		t.Fatalf("merged connect_ports = %v, want both entries", merged.Isolation.Network.ConnectPorts)
	}
}

func TestAnExplicitConnectFlagBeatsAPolicyEntryForTheSamePort(t *testing.T) {
	// The flag names an in-sandbox port because a command line usually has to
	// hardcode it. A policy entry for the same host port used to win the dedupe
	// and silently substitute a different port, so the hardcoded endpoint pointed
	// at nothing and nothing said why.
	flagFirst := []string{"9222:19222"}
	policy := []string{"9222"}

	got := normalizeConnectPorts(append(flagFirst, policy...))
	if len(got) != 1 {
		t.Fatalf("got %d mappings, want the two entries for port 9222 deduped: %+v", len(got), got)
	}
	if got[0].Inside != 19222 {
		t.Fatalf("in-sandbox port = %d, want the 19222 the flag asked for", got[0].Inside)
	}
}
