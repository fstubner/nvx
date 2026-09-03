package main

import (
	"net"
	"strconv"
	"strings"
	"testing"
)

// Two ways an allowlisted NAME reached an address the policy never approved,
// both found by an independent acceptance pass on 2026-09-03.

// The address that was judged is the address that is dialled.
//
// The proxy resolved a name to vet it and then handed the NAME to net.Dial,
// which resolved it a second time. A record with a short TTL answers benign to
// the check and 169.254.169.254 -- the cloud metadata endpoint, credentials in
// one unauthenticated GET -- to the dial. egress_resolve.go's own header named
// that shape as one it closed while the code kept doing it.
//
// Asserted as the property that makes rebinding impossible rather than by
// standing up a lying DNS server: given addresses to dial, the name must never
// be consulted again. The name here cannot resolve at all, so a connection
// proves the dial used the vetted address.
func TestTheDialUsesTheVettedAddressAndNotTheName(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)

	const unresolvable = "nvx-this-name-must-not-resolve.invalid"
	if ips, err := net.LookupIP(unresolvable); err == nil && len(ips) > 0 {
		t.Skipf("%s unexpectedly resolves here (%v), so it cannot show the name was ignored", unresolvable, ips)
	}

	conn, err := dialVetted([]net.IP{net.ParseIP("127.0.0.1")}, unresolvable, uint16(port))
	if err != nil {
		t.Fatalf("dialVetted refused the vetted address and appears to have used the name instead: %v", err)
	}
	_ = conn.Close()
}

// A name that resolves to link-local is refused, and the refusal happens at the
// one resolution the dial then uses.
func TestResolvingOnceStillRefusesLinkLocal(t *testing.T) {
	metadata := func(string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("169.254.169.254")}, nil
	}
	if _, err := resolveEgressAddresses("harmless.example.com", metadata); err == nil {
		t.Fatal("a name resolving to the cloud metadata endpoint was approved")
	}
	// A literal address the policy author typed is theirs to decide.
	ips, err := resolveEgressAddresses("169.254.169.254", metadata)
	if err != nil {
		t.Fatalf("a literally allowlisted link-local address was refused: %v", err)
	}
	if len(ips) != 1 || !ips[0].Equal(net.ParseIP("169.254.169.254")) {
		t.Fatalf("a literal address should be returned as itself, got %v", ips)
	}
	// A name that will not resolve is the dial's problem to report, not a
	// containment verdict; nothing can rebind an answer never obtained.
	broken := func(string) ([]net.IP, error) { return nil, net.UnknownNetworkError("no dns") }
	if ips, err := resolveEgressAddresses("example.com", broken); err != nil || len(ips) != 0 {
		t.Fatalf("a DNS failure became a policy verdict: ips=%v err=%v", ips, err)
	}
}

// A hostname pointing at 127.0.0.1 does not get to ask the developer for their
// local Postgres.
//
// The refusal that stops untrusted code prompting for a loopback service matched
// only the literal spellings of loopback, so `cache.attacker.example` with an A
// record of 127.0.0.1 walked past it and reached the prompt. The prompt is
// raised BY the contained process, at a moment nobody expects a security
// question, and one "yes" hands over a service that takes no credentials.
func TestANameResolvingToLoopbackIsRefusedNotPrompted(t *testing.T) {
	p := newTestProxy(t, "proxy", nil)
	p.policy.Isolation.Network.PromptUnknown = true // the prompt path is the one under test

	loopback := []net.IP{net.ParseIP("127.0.0.1")}
	if p.allowed(parseHostPortSpec("cache.attacker.example", 5432), loopback) {
		t.Error("a name resolving to 127.0.0.1 was permitted; a postinstall could reach the developer's local database")
	}
	// The verdict alone proves nothing: a test cannot answer a prompt, so the
	// prompt path denies too and `false` means either. Which refusal fired is the
	// whole question, and only the audit event says. Without this the test passed
	// with the fix reverted.
	if !auditHasLoopbackRefusal(t, p.nvxHome, "cache.attacker.example") {
		t.Error("the name reached the prompt instead of being refused: a postinstall got to ask the " +
			"developer for their local database, which is exactly what this refusal exists to stop")
	}

	// The literal spellings must still be refused, and a genuinely remote name
	// must still be reachable through the ordinary prompt path rather than being
	// swept up by this.
	if p.allowed(parseHostPortSpec("127.0.0.1", 5432), loopback) {
		t.Error("the literal loopback refusal regressed")
	}
	// A remote name must not be caught by THIS rule. It is still denied here --
	// nothing allowlists it and a prompt cannot be answered in a test -- so the
	// verdict alone cannot tell the two refusals apart. The audit event can.
	remoteProxy := newTestProxy(t, "proxy", nil)
	remoteProxy.policy.Isolation.Network.PromptUnknown = true
	remoteProxy.allowed(parseHostPortSpec("registry.npmjs.org", 443), []net.IP{net.ParseIP("104.16.0.1")})
	entries, err := readAuditEntries(remoteProxy.nvxHome)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	for _, e := range entries {
		if e["event"] == "egress_deny_loopback_prompt" {
			t.Errorf("a remote name was refused by the loopback rule; the check is too broad: %v", e)
		}
	}
}

// The refusal is audited under its own event, so a reader can tell it from an
// ordinary allowlist denial.
func TestTheLoopbackRefusalIsAudited(t *testing.T) {
	p := newTestProxy(t, "proxy", nil)
	p.policy.Isolation.Network.PromptUnknown = true

	p.allowed(parseHostPortSpec("cache.attacker.example", 5432), []net.IP{net.ParseIP("127.0.0.1")})

	if !auditHasLoopbackRefusal(t, p.nvxHome, "cache.attacker.example") {
		t.Fatal("the refusal was not recorded as a loopback denial, so `nvx audit` cannot tell it from an ordinary allowlist miss")
	}
}

// auditHasLoopbackRefusal reports whether the loopback refusal fired for host.
//
// The discriminator this file's tests rest on. `allowed()` returning false says
// only that the destination was not permitted; it is the same answer whether the
// loopback rule refused it or an unanswerable prompt did. Asserting the verdict
// alone let the loopback test pass with the fix reverted.
func auditHasLoopbackRefusal(t *testing.T, nvxHome, host string) bool {
	t.Helper()
	entries, err := readAuditEntries(nvxHome)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	for _, e := range entries {
		if e["event"] == "egress_deny_loopback_prompt" && strings.Contains(e["host"], host) {
			return true
		}
	}
	return false
}
