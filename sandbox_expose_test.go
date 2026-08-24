package main

import "testing"

// TestParseExposeSpecRejectsAPortMappedToItself is the rule that is easy to get
// wrong and expensive to discover at runtime.
//
// An AppContainer shares the host's network stack, unlike a Linux network
// namespace, so a port bound inside the container is occupied for the host too.
// The parent binds first, so a same-number mapping does not fail as a mapping --
// it fails later, as the contained server dying with EADDRINUSE, which reads as
// the user's own port conflict. Measured on Windows 11 with 51733 on both sides
// before the rule existed.
func TestParseExposeSpecRejectsAPortMappedToItself(t *testing.T) {
	if _, err := parseExposeSpec("5173:5173"); err == nil {
		t.Fatal("a port mapped to itself must be refused: the container shares the host's network stack")
	}
}

func TestParseExposeSpec(t *testing.T) {
	for _, tc := range []struct {
		in        string
		container int
		host      int
	}{
		{"5173", 5173, 0}, // host 0 means "pick a free one"
		{"5173:8080", 5173, 8080},
		{" 5173 : 8080 ", 5173, 8080}, // a policy file written by hand
	} {
		got, err := parseExposeSpec(tc.in)
		if err != nil {
			t.Errorf("%q: unexpected error %v", tc.in, err)
			continue
		}
		if got.Container != tc.container || got.Host != tc.host {
			t.Errorf("%q: got %+v, want container=%d host=%d", tc.in, got, tc.container, tc.host)
		}
	}

	for _, bad := range []string{"", "0", "70000", "notaport", "5173:0", "5173:70000", "5173:x", ":8080"} {
		if _, err := parseExposeSpec(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
}

// TestNormalizeExposePortsSkipsBadEntriesWithoutDroppingGoodOnes: a typo in one
// entry of a policy file must not silently cost the others, and must not take
// the launch down either.
func TestNormalizeExposePortsSkipsBadEntriesWithoutDroppingGoodOnes(t *testing.T) {
	got := normalizeExposePorts([]string{"notaport", "5173", "3000:3001", "5173:9999"})
	if len(got) != 2 {
		t.Fatalf("expected the two usable entries, got %+v", got)
	}
	if got[0].Container != 5173 || got[0].Host != 0 {
		t.Errorf("first entry: got %+v", got[0])
	}
	if got[1].Container != 3000 || got[1].Host != 3001 {
		t.Errorf("second entry: got %+v", got[1])
	}
}

// TestExposePortsInAProjectPolicyCountAsLoosening pins that a project file
// cannot publish a port without the developer approving it.
//
// Publishing grants the sandbox no new access, so it is tempting to treat it as
// neutral. It is not: it puts whatever the contained process serves onto the
// host's loopback, where a browser extends it the trust localhost carries.
func TestExposePortsInAProjectPolicyCountAsLoosening(t *testing.T) {
	before := DefaultPolicy()
	after := DefaultPolicy()
	after.Isolation.Network.ExposePorts = []string{"5173"}

	if !policyLoosens(before, after) {
		t.Fatal("adding expose_ports must count as loosening, so the project policy needs trust")
	}
	if policyLoosens(after, after) {
		t.Error("an unchanged expose_ports list must not read as loosening")
	}
}
