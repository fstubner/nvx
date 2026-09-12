package nvx

import (
	"net"
	"testing"
)

// A name whose first lookup failed is not offered at the prompt.
//
// The loopback refusal in allowed() inspects the addresses from the FIRST
// resolution. When that lookup fails, there are none, so the refusal had nothing
// to inspect and the name reached the prompt. One "yes" -- or NVX_TRUST_YES,
// which needs no human -- and dialVetted resolved again, checking only for
// link-local. SERVFAIL first, 127.0.0.1 second, and a postinstall was spliced to
// the developer's local Postgres. The same shape closed for link-local the day
// before, found in the fix for it by an independent audit on 2026-09-06.
//
// A person cannot judge an address nobody has seen, so an unresolved name is
// refused before the prompt. Allowlisted names are untouched: they return before
// this, and a transient DNS failure there stays the dial's problem.
func TestAnUnresolvedNameIsRefusedNotPrompted(t *testing.T) {
	p := newTestProxy(t, "proxy", nil)
	p.policy.Isolation.Network.PromptUnknown = true

	if p.allowed(parseHostPortSpec("cache.attacker.example", 5432), nil) {
		t.Fatal("a name that did not resolve was permitted; the dial's second lookup would then be the only judge")
	}
	// As with the loopback test: `false` is also what a prompt nobody answers
	// returns, so the verdict alone cannot say which refusal fired. The audit
	// event can, and without this check the test passed with the fix reverted.
	entries, err := readAuditEntries(p.nvxHome)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	found := false
	for _, e := range entries {
		if e["event"] == "egress_deny_unresolved_prompt" {
			found = true
		}
	}
	if !found {
		t.Error("the unresolved name reached the prompt instead of being refused: a postinstall got to ask " +
			"the developer to approve an address that did not exist yet")
	}
}

// A grant given at the prompt does not carry a name to loopback later.
//
// Session grants are keyed on the NAME, and were consulted before the address
// was looked at. So a name approved while it resolved publicly -- an ordinary
// CDN, say -- matched the grant on every later request, including one where the
// record had changed to 127.0.0.1, and was dialled without the loopback refusal
// ever running. The address has to be judged on every request, not only the one
// that prompted; the refusal now sits before the session check.
func TestASessionGrantDoesNotCarryANameToLoopback(t *testing.T) {
	p := newTestProxy(t, "proxy", nil)
	p.policy.Isolation.Network.PromptUnknown = true

	hp := parseHostPortSpec("cdn.attacker.example", 443)
	// The grant a developer gave while the name pointed somewhere harmless.
	p.session["cdn.attacker.example:443"] = true

	// The record has since changed.
	if p.allowed(hp, []net.IP{net.ParseIP("127.0.0.1")}) {
		t.Fatal("a session grant carried a name to 127.0.0.1; a postinstall that got one yes for a public " +
			"host can now redirect it at the developer's local services")
	}
	if !auditHasLoopbackRefusal(t, p.nvxHome, "cdn.attacker.example") {
		t.Error("the loopback refusal did not fire; the grant was honoured before the address was judged")
	}

	// And the grant still works for what it was given for.
	if !p.allowed(hp, []net.IP{net.ParseIP("104.16.0.1")}) {
		t.Error("a session grant stopped working for the public address it was granted against")
	}
}
