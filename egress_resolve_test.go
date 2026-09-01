package main

import (
	"errors"
	"net"
	"strings"
	"testing"
)

// The allowlist decides a name; the connection reaches an address.
//
// allowed() matches text -- what the policy wrote against what the client asked
// for -- and net.Dial then resolved that text with nothing looking at the result.
// An allowlisted hostname pointing at 169.254.169.254 is the cloud metadata
// endpoint, which hands out credentials to any unauthenticated GET.
func TestANameResolvingToLinkLocalIsRefused(t *testing.T) {
	metadata := func(string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("169.254.169.254")}, nil
	}
	err := resolvedAddressAllowed("harmless.example.com", metadata)
	if err == nil {
		t.Fatal("a name resolving to the cloud metadata endpoint was allowed; " +
			"the allowlist checked the name and nothing checked the address")
	}
	if !strings.Contains(err.Error(), "169.254.169.254") {
		t.Fatalf("the refusal does not name the address it refused: %v", err)
	}

	// IPv6 link-local too, or the check only covers half the address space.
	v6 := func(string) ([]net.IP, error) { return []net.IP{net.ParseIP("fe80::1")}, nil }
	if err := resolvedAddressAllowed("harmless.example.com", v6); err == nil {
		t.Fatal("an IPv6 link-local answer was allowed")
	}

	// Any link-local answer is enough, even alongside ordinary ones: a resolver
	// returning several addresses must not be judged by the first.
	mixed := func(string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("93.184.216.34"), net.ParseIP("169.254.169.254")}, nil
	}
	if err := resolvedAddressAllowed("harmless.example.com", mixed); err == nil {
		t.Fatal("a link-local address after a public one was allowed; nvx would dial whichever the OS picked")
	}
}

// The narrowness is deliberate, and this pins it so a later "harden the SSRF
// check" does not quietly break supported uses.
func TestTheResolvedAddressCheckStaysNarrow(t *testing.T) {
	cases := []struct {
		name string
		host string
		ips  []net.IP
		want bool // true = must be allowed
	}{
		// Reaching a LAN service is documented and supported: README's own example
		// is localhost:5432, and the Windows enforcement script proves the egress
		// path against a 192.168.x.x listener. Refusing RFC1918 would break both.
		{"private range via a name", "nas.local", []net.IP{net.ParseIP("192.168.1.5")}, true},
		{"carrier-grade NAT (Tailscale)", "peer.ts.net", []net.IP{net.ParseIP("100.106.71.95")}, true},
		{"ordinary public address", "registry.npmjs.org", []net.IP{net.ParseIP("104.16.0.1")}, true},
		// A literal the policy named is a decision already taken, including a
		// link-local one: this stops reaching it WITHOUT having said so.
		{"literal link-local", "169.254.169.254", nil, true},
		{"literal loopback", "127.0.0.1", nil, true},
		// ...and the case the check exists for.
		{"link-local via a name", "sneaky.example.com", []net.IP{net.ParseIP("169.254.169.254")}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lookup := func(string) ([]net.IP, error) { return tc.ips, nil }
			err := resolvedAddressAllowed(tc.host, lookup)
			if tc.want && err != nil {
				t.Fatalf("%s was refused: %v", tc.host, err)
			}
			if !tc.want && err == nil {
				t.Fatalf("%s was allowed", tc.host)
			}
		})
	}
}

// A resolver that fails is not a policy verdict.
//
// Returning an error here would turn every transient DNS failure into "blocked",
// which reads as a containment decision it is not -- and the dial that follows
// reports the real failure anyway.
func TestAResolverFailureIsNotTreatedAsARefusal(t *testing.T) {
	broken := func(string) ([]net.IP, error) { return nil, errors.New("no such host") }
	if err := resolvedAddressAllowed("example.com", broken); err != nil {
		t.Fatalf("a DNS failure was reported as a containment refusal: %v", err)
	}
}
