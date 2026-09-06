package main

import (
	"fmt"
	"net"
)

// refuseInvalidHost is the one refusal for a destination outside the hostname
// grammar: it says so with the bytes escaped, records it, and reports true
// when the caller should stop. Used at the top of allowed() and by both
// protocol handlers before they resolve anything, because the resolve-failure
// path also prints the name.
func (p *EgressProxy) refuseInvalidHost(hp hostPort) bool {
	if validEgressHost(hp.host) {
		return false
	}
	LogWarn("Blocked egress: the requested destination %q is not a valid hostname or address.", hp.host)
	auditLog(p.nvxHome, "egress_deny_invalid_host", map[string]string{"host": fmt.Sprintf("%q", hp.host)})
	return true
}

// validEgressHost reports whether host is something nvx should be judging at
// all: an IP address, or a hostname within the hostname grammar.
//
// The host is whatever bytes the sandboxed client put in its SOCKS or CONNECT
// request, and two things downstream take it at face value. The prompt prints
// it -- "Allow outbound connection to <host>:<port>?" -- to a terminal, where a
// carriage return or an escape sequence inside the name redraws the line the
// person is reading, and an embedded newline puts words in the prompt's
// mouth. And the audit log records it. Hostnames have a grammar; a name
// outside it is not a destination, it is input, and it is refused before it
// reaches either.
//
// The grammar is RFC 1123's, with underscores, which real names carry, and
// with the trailing dot of an absolute name. Everything is ASCII by the time
// it is a DNS name: an internationalised name arrives punycoded, so a byte
// outside ASCII is not a name.
func validEgressHost(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	labels := 0
	start := 0
	for i := 0; i <= len(host); i++ {
		if i < len(host) && host[i] != '.' {
			c := host[i]
			ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_'
			if !ok {
				return false
			}
			continue
		}
		// End of a label (at a dot or at the end of the string).
		n := i - start
		if n == 0 {
			// An empty label is only the one after a trailing dot.
			if i == len(host) && labels > 0 {
				break
			}
			return false
		}
		if n > 63 {
			return false
		}
		labels++
		start = i + 1
	}
	return labels > 0
}
