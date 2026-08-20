package main

import "testing"

// A typo in isolation.network.mode used to pass silently and bucket as proxy, so
// a policy asking for "offlin" got a live egress proxy while the author believed
// they had asked for no network at all. The neighbouring isolation.level already
// warned on an unrecognised value; this is the field where being wrong hands out
// MORE access than intended, so silence was the worse default.
func TestNetworkModeRejectsTyposAndKeepsValidValues(t *testing.T) {
	valid := map[string]string{
		"proxy":    "proxy",
		"offline":  "offline",
		"loopback": "loopback",
		"open":     "open",
		"OFFLINE":  "offline",
		" offline": "offline",
		"offline ": "offline",
		"":         "proxy",
	}
	for in, want := range valid {
		got, ok := parseNetworkMode(in)
		if !ok {
			t.Errorf("parseNetworkMode(%q) reported the value as unrecognised", in)
		}
		if got != want {
			t.Errorf("parseNetworkMode(%q) = %q, want %q", in, got, want)
		}
	}

	// Every one of these is a plausible typo for a mode that grants LESS than the
	// fallback, which is why they must not pass quietly.
	for _, bad := range []string{"offlin", "ofline", "opn", "loopbak", "none", "disabled", "off", "true", "proxy!"} {
		got, ok := parseNetworkMode(bad)
		if ok {
			t.Errorf("parseNetworkMode(%q) accepted a typo as valid", bad)
		}
		if got != "proxy" {
			t.Errorf("parseNetworkMode(%q) fell back to %q; the fallback must be the restrictive default", bad, got)
		}
	}
}

// normalizePolicy rewrites the value, so no downstream reader has to fall into
// its own default arm and quietly disagree with another platform's.
func TestNormalizePolicyRewritesAnUnknownNetworkMode(t *testing.T) {
	p := DefaultPolicy()
	p.Isolation.Network.Mode = "offlin"
	normalizePolicy(&p)
	if p.Isolation.Network.Mode != "proxy" {
		t.Errorf("an unrecognised mode survived normalization as %q", p.Isolation.Network.Mode)
	}

	p2 := DefaultPolicy()
	p2.Isolation.Network.Mode = "offline"
	normalizePolicy(&p2)
	if p2.Isolation.Network.Mode != "offline" {
		t.Errorf("a valid mode was rewritten to %q", p2.Isolation.Network.Mode)
	}
}
