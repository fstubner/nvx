package main

import (
	"fmt"
	"net"
	"strconv"
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

// resolveEgressAddresses resolves host ONCE and returns the addresses nvx will
// dial, or an error naming why it will not.
//
// The point is the "once". checkResolvedAddress resolved a name to vet it and
// then handed the NAME to net.Dial, which resolved it again -- so the address
// that was judged and the address that was reached were two separate DNS
// answers. A record with a short TTL answers benign to the first and
// 169.254.169.254 to the second, and the file this sits in already named that
// shape ("a name whose DNS answer changes between the check and the dial is the
// same shape again") while the code kept doing it. Found by an independent
// acceptance pass on 2026-09-03.
//
// A literal IP from the client is returned as itself: the allowlist matched that
// text, so a human wrote it and has decided.
//
// A lookup that FAILS returns no addresses and no error, and the caller dials by
// name so the dial reports it. Refusing here would turn a transient DNS error
// into a containment verdict it is not -- and there is nothing to rebind from
// when resolution never succeeded.
func resolveEgressAddresses(host string, lookup func(string) ([]net.IP, error)) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	ips, err := lookup(host)
	if err != nil || len(ips) == 0 {
		return nil, nil
	}
	for _, ip := range ips {
		if isLinkLocal(ip) {
			return nil, fmt.Errorf("%s resolves to the link-local address %s, which nvx does not reach through a name; "+
				"allowlist that address literally if it is really what you want", host, ip)
		}
	}
	return ips, nil
}

// resolveEgressTarget is resolveEgressAddresses against the real resolver,
// replaceable so a test can supply an answer without needing DNS that returns
// one.
var resolveEgressTarget = func(host string) ([]net.IP, error) {
	return resolveEgressAddresses(host, net.LookupIP)
}

// anyLoopback reports whether any resolved address is a loopback address.
//
// isLoopback answers about the TEXT the client asked for, which is the right
// question for allowlist matching and the wrong one for refusing a prompt: a
// name is exactly how you would reach 127.0.0.1 without typing it. See the
// refusal in allowed().
func anyLoopback(ips []net.IP) bool {
	for _, ip := range ips {
		if ip.IsLoopback() {
			return true
		}
	}
	return false
}

// dialVetted connects to the addresses resolveEgressTarget approved, trying each
// in turn, and never re-resolves the name.
//
// net.Dial("tcp", "name:port") walks a fresh DNS answer internally; that second
// lookup is the whole bug. Dialing the vetted IPs keeps the address that was
// judged and the address that is reached the same one.
//
// Each in turn rather than just the first, because net.Dial tried them all and
// dropping that would break a host whose AAAA record is unreachable while its A
// record works -- a real configuration, and not the kind of regression to
// introduce while closing a hole.
//
// An empty list means the name did not resolve; the name is dialled so the dial
// reports the DNS error in its own words. Nothing can rebind an answer that was
// never obtained.
func dialVetted(ips []net.IP, host string, port uint16) (net.Conn, error) {
	if len(ips) == 0 {
		return net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(int(port))))
	}
	var lastErr error
	for _, ip := range ips {
		conn, err := net.Dial("tcp", net.JoinHostPort(ip.String(), strconv.Itoa(int(port))))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}
