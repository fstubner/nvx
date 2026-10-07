package nvx

import (
	"context"
	"net"
	"strings"
	"testing"
)

// A literal link-local address is never offered at the prompt.
//
// The prompt is raised by whatever the sandbox is running, which is the untrusted
// code, and the refusal for a literal loopback address says why that matters:
// nobody reviewed an answer given to a question the code chose to ask. The
// address that matters most in 169.254.0.0/16 is 169.254.169.254, the cloud
// metadata endpoint, where one unauthenticated GET returns credentials.
//
// resolveEgressAddresses refused a NAME that resolves there, but returns a
// literal address as itself, so the literal walked through to a yes.
//
// The resolver the test passes returns the literal as itself, as
// resolveEgressTarget does. A nil answer is the "did not resolve" case, which is
// refused after the prompt for another reason and would hide this one.

func TestALiteralLinkLocalAddressIsNeverOfferedAtThePrompt(t *testing.T) {
	// Approves any prompt that is reached, so a refusal here is the link-local
	// rule and not an unanswerable prompt.
	t.Setenv("NVX_TRUST_YES", "1")

	for _, host := range []string{
		"169.254.169.254",        // cloud metadata
		"169.254.0.1",            // the rest of 169.254.0.0/16
		"169.254.255.255",        // its last address
		"::ffff:169.254.169.254", // the same address, IPv4-mapped
		"fe80::1",                // IPv6 link-local
		"FE80::1",                // letter case does not change the address
		"febf::1",                // the top of fe80::/10
	} {
		t.Run(host, func(t *testing.T) {
			p := newPromptingProxy(t, nil)
			literal := []net.IP{net.ParseIP(host)}
			if p.allowed(parseHostPortSpec(host, 80), literal) {
				t.Fatalf("%s:80 was granted through the prompt; untrusted code can ask for the metadata endpoint", host)
			}
			if !auditContains(t, p.nvxHome, "egress_deny_link_local_prompt") {
				t.Errorf("%s:80 was denied, but not by the link-local rule: the prompt path was still entered", host)
			}
			if len(p.prompted) != 0 {
				t.Errorf("%s:80 recorded a prompt; the refusal must come before asking", host)
			}
		})
	}
}

// The rule is about link-local addresses and nothing near them. Each neighbour
// here is one a careless prefix test would catch.
func TestTheLinkLocalRefusalDoesNotReachNeighbouringAddresses(t *testing.T) {
	t.Setenv("NVX_TRUST_YES", "1")

	for _, host := range []string{
		"169.253.255.255", // just below 169.254.0.0/16
		"169.255.0.0",     // just above it
		"fec0::1",         // site-local, outside fe80::/10
		"fe7f::1",         // just below fe80::/10
		"104.16.0.1",      // an ordinary public address
	} {
		t.Run(host, func(t *testing.T) {
			p := newPromptingProxy(t, nil)
			if !p.allowed(parseHostPortSpec(host, 443), []net.IP{net.ParseIP(host)}) {
				t.Fatalf("%s:443 was refused; the link-local rule is too broad", host)
			}
			if auditContains(t, p.nvxHome, "egress_deny_link_local_prompt") {
				t.Errorf("%s:443 was recorded as link-local", host)
			}
		})
	}
}

// A policy entry that names the address is a decision someone wrote down, and it
// still works. It is the same line the loopback refusal draws.
func TestAPolicyEntryStillAllowsALiteralLinkLocalAddress(t *testing.T) {
	t.Setenv("NVX_TRUST_YES", "1")
	p := newPromptingProxy(t, []string{"169.254.169.254:80"})
	if !p.allowed(parseHostPortSpec("169.254.169.254", 80), []net.IP{net.ParseIP("169.254.169.254")}) {
		t.Fatal("an allow_hosts entry naming 169.254.169.254:80 no longer allows it")
	}
	// Only that port. The entry is matched, not the address waved through.
	if p.allowed(parseHostPortSpec("169.254.169.254", 8080), []net.IP{net.ParseIP("169.254.169.254")}) {
		t.Error("an entry for port 80 also allowed port 8080")
	}
}

// The same refusal on the wire, through both protocol handlers, for the spellings
// a client can send.
func TestTheProxyRefusesALiteralLinkLocalAddressOnBothProtocols(t *testing.T) {
	t.Setenv("NVX_TRUST_YES", "1")
	policy := DefaultPolicy()
	policy.Isolation.Network.PromptUnknown = true
	p, err := startEgressProxy(context.Background(), policy, Providers["node"], tempDir(t))
	if err != nil {
		t.Fatalf("startEgressProxy: %v", err)
	}
	t.Cleanup(p.Close)

	for _, target := range []string{"169.254.169.254:80", "[::ffff:169.254.169.254]:80", "[fe80::1]:80"} {
		status, _ := connectThrough(t, p, target)
		if !strings.Contains(status, " 403 ") {
			t.Errorf("CONNECT %s got %q, want 403", target, status)
		}
	}
	// 0x02 is "not allowed by ruleset". A granted request that then failed to dial
	// answers 0x05, which is how this looked before the refusal.
	if code := socksConnectName(t, p, "169.254.169.254", 80); code != 0x02 {
		t.Errorf("SOCKS answered %#x for 169.254.169.254:80, want 0x02 (refused)", code)
	}
	if !auditContains(t, p.nvxHome, "egress_deny_link_local_prompt") {
		t.Error("the refusals were not recorded as egress_deny_link_local_prompt")
	}
	if auditContains(t, p.nvxHome, "egress_allow_prompted") {
		t.Error("a link-local address reached the prompt and was approved")
	}
}
