package main

import (
	"net"
	"strings"
	"testing"
)

// A lookup that fails once must not become a way past the link-local guard.
//
// resolveEgressAddresses returns no addresses and no error when a name will not
// resolve, deliberately: a transient DNS failure is the dial's problem to
// report, not a containment verdict. dialVetted then had nothing to dial and
// fell back to net.Dial with the NAME -- which resolves a second time, and
// nothing judged that answer.
//
// So the guard that exists to stop a name reaching 169.254.169.254 could be
// stepped around by answering the first query with SERVFAIL and the second with
// the metadata address. The comment on resolveEgressAddresses asserted "there is
// nothing to rebind from when resolution never succeeded", and that is the
// error this pins: the second resolution is the thing to rebind from.
//
// Found by an independent security audit, 2026-09-05, in code added the day
// before to close the FIRST version of this same hole.
func TestASecondLookupCannotSlipPastTheLinkLocalGuard(t *testing.T) {
	orig := resolveEgressTarget
	t.Cleanup(func() { resolveEgressTarget = orig })

	// What the attacker's DNS answers once the first query has already failed.
	resolveEgressTarget = func(host string) ([]net.IP, error) {
		return resolveEgressAddresses(host, func(string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("169.254.169.254")}, nil
		})
	}

	conn, err := dialVetted(nil, "rebind.example", 80)
	if err == nil {
		if conn != nil {
			_ = conn.Close()
		}
		t.Fatal("dialVetted connected to a name whose only resolution is link-local. " +
			"An allowlisted host whose first lookup fails can reach the cloud metadata endpoint.")
	}
	if !strings.Contains(err.Error(), "link-local") {
		t.Errorf("the refusal does not name the reason, so nobody can act on it: %v", err)
	}
}

// A name that genuinely will not resolve fails as a dial, not as a policy
// denial, and still never reaches net.Dial by name.
//
// The distinction matters: turning every transient DNS error into "blocked"
// reads as a containment decision nvx did not make, which is why
// resolveEgressAddresses swallows the lookup error in the first place. What
// changed is only that the fallback no longer dials an unjudged name.
func TestAnUnresolvableNameFailsAsADialNotAsADenial(t *testing.T) {
	orig := resolveEgressTarget
	t.Cleanup(func() { resolveEgressTarget = orig })

	resolveEgressTarget = func(string) ([]net.IP, error) { return nil, nil }

	conn, err := dialVetted(nil, "does-not-resolve.invalid", 80)
	if err == nil {
		if conn != nil {
			_ = conn.Close()
		}
		t.Fatal("dialVetted reported success for a name that resolves to nothing")
	}
	if strings.Contains(strings.ToLower(err.Error()), "link-local") ||
		strings.Contains(strings.ToLower(err.Error()), "block") {
		t.Errorf("a resolution failure was reported as a containment decision: %v", err)
	}
}

// A literal address the policy already matched still dials, unchanged.
//
// The fix must not turn "the caller gave us addresses" into another lookup:
// those addresses were judged by the caller, and re-resolving would reintroduce
// the two-answers problem from the other end.
func TestAlreadyVettedAddressesAreNotResolvedAgain(t *testing.T) {
	orig := resolveEgressTarget
	t.Cleanup(func() { resolveEgressTarget = orig })

	called := false
	resolveEgressTarget = func(string) ([]net.IP, error) {
		called = true
		return nil, nil
	}

	// Port 1 on a loopback address: the dial fails fast without needing a
	// listener, and the assertion is about whether a lookup happened.
	conn, _ := dialVetted([]net.IP{net.ParseIP("127.0.0.1")}, "already.vetted", 1)
	if conn != nil {
		_ = conn.Close()
	}
	if called {
		t.Error("dialVetted re-resolved a host whose addresses it was already given")
	}
}
