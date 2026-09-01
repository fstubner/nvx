package main

import (
	"fmt"
	"net"
)

// The allowlist decides a NAME; the connection reaches an ADDRESS.
//
// allowed() matches on what the policy wrote and what the client asked for, both
// of which are text. net.Dial then resolves that text, and nothing looked at what
// it resolved to. An allowlisted hostname pointing at 169.254.169.254 is the
// cloud-metadata endpoint -- credentials, in one unauthenticated GET -- and a
// name whose DNS answer changes between the check and the dial is the same shape
// again.
//
// Deliberately narrow, and the narrowness is the design rather than an oversight:
//
//   - Only link-local is refused (169.254.0.0/16 and fe80::/10). That range is
//     never a service a developer means to allowlist by name, and it is where the
//     metadata endpoints live on every major cloud.
//   - RFC1918 and other private ranges are NOT refused. Reaching a LAN service is
//     a documented, supported thing to allowlist -- README's own example is
//     `localhost:5432`, and scripts/sandbox-enforcement-windows.ps1 proves the
//     egress path against a 192.168.x.x listener. Blocking those would break a
//     real use and the test that guards it.
//   - A policy naming a LITERAL link-local address is honoured. Someone who typed
//     169.254.169.254 into allow_hosts has decided; what this stops is reaching it
//     without having said so, through a name.
//
// So this closes "the address was never checked" without pretending to be a
// general SSRF filter, which nvx is not positioned to be: the allowlist is
// author-written, not attacker-supplied.

// isLinkLocal reports whether ip is in a link-local range.
func isLinkLocal(ip net.IP) bool {
	return ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}

// resolvedAddressAllowed reports whether connecting to host is acceptable once
// its address is known, given that the policy already permitted the name.
//
// An IP the client asked for directly is allowed: the allowlist matched that text,
// so a human wrote it somewhere and has decided. Only a NAME is resolved and
// judged, because a name is the thing whose address the policy author did not
// see.
func resolvedAddressAllowed(host string, lookup func(string) ([]net.IP, error)) error {
	if ip := net.ParseIP(host); ip != nil {
		return nil
	}
	ips, err := lookup(host)
	if err != nil {
		// Resolution failure is the dial's problem to report, not a policy verdict.
		// Refusing here would turn every transient DNS error into "blocked", which
		// reads as a containment decision it is not.
		return nil
	}
	for _, ip := range ips {
		if isLinkLocal(ip) {
			return fmt.Errorf("%s resolves to the link-local address %s, which nvx does not reach through a name; "+
				"allowlist that address literally if it is really what you want", host, ip)
		}
	}
	return nil
}

// checkResolvedAddress is resolvedAddressAllowed against the real resolver,
// replaceable so a test can supply an answer without needing DNS that returns
// one.
var checkResolvedAddress = func(host string) error {
	return resolvedAddressAllowed(host, net.LookupIP)
}
