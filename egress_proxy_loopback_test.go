package main

import (
	"context"
	"testing"
)

// newTestProxy builds an EgressProxy without listeners, so allowed() can be
// exercised directly.
func newTestProxy(t *testing.T, mode string, allowHosts []string) *EgressProxy {
	t.Helper()
	policy := DefaultPolicy()
	policy.Isolation.Network.Mode = mode
	policy.Isolation.Network.PromptUnknown = false // deny rather than block on a prompt
	policy.Isolation.Network.AllowHosts = allowHosts
	policy.Isolation.Network.DefaultAllow = nil
	policy.Isolation.Network.DefaultAllowSet = true

	allow := map[string]bool{}
	for _, entry := range policy.NetworkAllowlist(Providers["node"]) {
		allow[normalizeAllowEntry(entry)] = true
	}
	return &EgressProxy{
		allow:    allow,
		session:  map[string]bool{},
		prompted: map[string]bool{},
		policy:   policy,
		nvxHome:  t.TempDir(),
		ctx:      context.Background(),
	}
}

// TestLoopbackIsNotAutomaticallyAllowed is the fix for a regression the Windows
// egress relay introduced.
//
// EgressProxy.allowed permitted every loopback destination unconditionally (F38).
// That was survivable while no contained process could reach this proxy — on
// Windows an AppContainer with no network capability reached nothing, and on
// Linux a loopback-only netns has its own 127.0.0.1 rather than the host's. The
// relay put the proxy in the parent, outside the containment, dialling on the
// contained process's behalf. "Permit all loopback" then meant "permit every
// service on the developer's machine".
//
// Measured before the fix: a contained process read a host loopback service's
// response with an empty allowlist.
func TestLoopbackIsNotAutomaticallyAllowed(t *testing.T) {
	p := newTestProxy(t, "proxy", nil)

	for _, host := range []string{"127.0.0.1", "localhost", "::1"} {
		if p.allowed(parseHostPortSpec(host, 5432)) {
			t.Errorf("%s:5432 was permitted with an empty allowlist; a contained install could reach every local service", host)
		}
	}
}

// TestLoopbackIsReachableWhenAllowlisted keeps the fix from becoming "deny
// everything local", which would break the documented ability to reach a dev
// server from inside the sandbox. Both spellings must work: parseHostPortSpec
// rewrites localhost to 127.0.0.1 before the allowlist is consulted, so a policy
// written either way has to match.
func TestLoopbackIsReachableWhenAllowlisted(t *testing.T) {
	cases := []struct {
		name  string
		entry string
	}{
		{"numeric", "127.0.0.1:3000"},
		{"by name", "localhost:3000"},
		{"wildcard port", "127.0.0.1:*"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newTestProxy(t, "proxy", []string{tc.entry})
			if !p.allowed(parseHostPortSpec("127.0.0.1", 3000)) {
				t.Errorf("allow_hosts %q did not permit 127.0.0.1:3000", tc.entry)
			}
			if !p.allowed(parseHostPortSpec("localhost", 3000)) {
				t.Errorf("allow_hosts %q did not permit localhost:3000", tc.entry)
			}
			// A different port on the same host stays blocked, so the entry is
			// being matched rather than the host being waved through again.
			if tc.entry != "127.0.0.1:*" && p.allowed(parseHostPortSpec("127.0.0.1", 9999)) {
				t.Errorf("allow_hosts %q also permitted port 9999", tc.entry)
			}
		})
	}
}

// TestLoopbackModeStillPermitsLoopback pins the one mode whose entire definition
// is "loopback and nothing else". The fix must not collapse it into offline.
func TestLoopbackModeStillPermitsLoopback(t *testing.T) {
	p := newTestProxy(t, "loopback", nil)

	if !p.allowed(parseHostPortSpec("127.0.0.1", 8080)) {
		t.Error("network.mode=loopback must permit loopback destinations; that is what the mode is for")
	}
	if p.allowed(parseHostPortSpec("registry.npmjs.org", 443)) {
		t.Error("network.mode=loopback must still block non-loopback destinations")
	}
}

// TestOfflineModeBlocksLoopbackToo records a deliberate change. Offline used to
// permit loopback, because the unconditional short-circuit ran ahead of the mode
// check. A mode named offline granting a route to the host's services is not
// defensible once that route actually exists.
func TestOfflineModeBlocksLoopbackToo(t *testing.T) {
	p := newTestProxy(t, "offline", nil)

	if p.allowed(parseHostPortSpec("127.0.0.1", 8080)) {
		t.Error("network.mode=offline permitted a loopback destination")
	}
	if p.allowed(parseHostPortSpec("registry.npmjs.org", 443)) {
		t.Error("network.mode=offline permitted a remote destination")
	}
}
