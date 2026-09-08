package main

import "testing"

// Switching a project policy from proxy to loopback is a loosening, and needs
// approval like any other.
//
// It read as a tightening until 2026-09-08, because loopback was ranked with
// offline, below proxy. The two modes differ in exactly the direction that
// matters: proxy reaches a loopback service only when allow_hosts names it,
// loopback reaches every service on 127.0.0.1 without naming any. A
// .nvx-policy.json is a file in a repository, so this is one line in a pull
// request handing a contained install the developer's database, their other
// projects' dev servers, and any local agent -- and past the gate as if it had
// asked for less.
//
// macOS is where that was live: its Seatbelt profile grants all of localhost in
// this mode. Windows and Linux happened to treat loopback as offline, so nothing
// was reachable there to hand over.
func TestSwitchingToLoopbackModeAsksForApproval(t *testing.T) {
	policyWithMode := func(mode string) Policy {
		var p Policy
		p.Isolation.Network.Mode = mode
		return p
	}

	loosening := []struct{ from, to string }{
		{"proxy", "loopback"},
		{"offline", "loopback"},
		{"loopback", "open"},
		{"proxy", "open"},
		{"offline", "proxy"},
	}
	for _, c := range loosening {
		if !policyLoosens(policyWithMode(c.from), policyWithMode(c.to)) {
			t.Errorf("%s -> %s widens what the sandbox can reach, so it must need approval", c.from, c.to)
		}
	}

	tightening := []struct{ from, to string }{
		{"loopback", "proxy"},
		{"open", "loopback"},
		{"loopback", "offline"},
		{"proxy", "offline"},
		{"open", "proxy"},
	}
	for _, c := range tightening {
		if policyLoosens(policyWithMode(c.from), policyWithMode(c.to)) {
			t.Errorf("%s -> %s narrows what the sandbox can reach, so it must not need approval", c.from, c.to)
		}
	}
}

// The ordering holds for a value that was not normalised on the way in.
//
// parseNetworkMode trims and lowercases, but a policy that reached this gate
// without passing through it -- and one has, which is why the write-back in
// normalizePolicy sits on both branches -- would otherwise fall to the default
// arm and rank as proxy. "LOOPBACK " ranking as proxy is the same hole this test
// exists to close, reached by a different route.
func TestNetworkModeRankIgnoresCaseAndPadding(t *testing.T) {
	if networkModeRank(" LOOPBACK ") != networkModeRank("loopback") {
		t.Error("a padded, upper-case loopback does not rank as loopback")
	}
	if networkModeRank("loopback") <= networkModeRank("proxy") {
		t.Error("loopback must rank above proxy: it reaches every loopback service, proxy reaches the named ones")
	}
	if networkModeRank("loopback") >= networkModeRank("open") {
		t.Error("loopback must rank below open: open reaches the whole internet")
	}
	if networkModeRank("offline") >= networkModeRank("proxy") {
		t.Error("offline must rank below proxy: it reaches nothing")
	}
}
