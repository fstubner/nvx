package nvx

import (
	"net"
	"testing"
)

// A destination name that is not a valid hostname or address is refused before
// it can reach the prompt.
//
// The prompt asks "Allow outbound connection to <host:port> for the rest of
// this run?", and <host> is whatever bytes the sandboxed client put in its
// SOCKS or CONNECT request, trimmed and lowercased. The thing asking is the
// untrusted code. A name carrying a carriage return or a terminal escape can
// redraw the line the person is reading -- "evil.example" rendered as
// "github.com" -- and a name with an embedded newline puts words in the
// prompt's mouth. Hostnames have a grammar; anything outside it is not a
// destination nvx should be asking about.
func TestAMalformedHostNeverReachesThePrompt(t *testing.T) {
	// If the prompt IS reached, this approves it, so a refusal that happens
	// after the prompt would show up as a wrongly allowed connection rather
	// than a quiet pass.
	t.Setenv("NVX_TRUST_YES", "1")
	resolved := []net.IP{net.ParseIP("104.16.0.1")} // a resolved address, so the unresolved-name rule does not mask the result

	// A trailing "\r" alone is not here: parseHostPortSpec trims surrounding
	// whitespace, so what is judged and shown is the clean name, which cannot
	// mislead. Control characters INSIDE the name survive the trim, and those
	// are the cases.
	for _, host := range []string{
		"evil.example\x1b[2K\rallow github.com",
		"evil.example\nAllow outbound connection to github.com:443? yes",
		"evil example",
		"evil.example\x00",
		"eviĺ.example", // not ASCII; an IDN must arrive punycoded
	} {
		p := newPromptingProxy(t, nil)
		if p.allowed(parseHostPortSpec(host, 443), resolved) {
			t.Errorf("%q was granted; a name outside the hostname grammar reached the prompt", host)
		}
		if !auditContains(t, p.nvxHome, "egress_deny_invalid_host") {
			t.Errorf("%q was refused, but not as an invalid host; the prompt path was still entered", host)
		}
		if len(p.prompted) != 0 {
			t.Errorf("%q recorded a prompt; the refusal must come before asking", host)
		}
	}
}

// And the names that are valid still get through to the ordinary decision,
// so this is a grammar check and not a new denial.
func TestValidHostsStillReachTheOrdinaryDecision(t *testing.T) {
	t.Setenv("NVX_TRUST_YES", "1")
	resolved := []net.IP{net.ParseIP("104.16.0.1")}
	for _, host := range []string{"github.com", "registry.npmjs.org", "xn--80ak6aa92e.com", "my_host.internal", "104.16.0.1", "2606:4700::6810:1", "a.b-c.d."} {
		p := newPromptingProxy(t, nil)
		if !p.allowed(parseHostPortSpec(host, 443), resolved) {
			t.Errorf("%q is a valid destination name and was refused", host)
		}
		if auditContains(t, p.nvxHome, "egress_deny_invalid_host") {
			t.Errorf("%q was flagged as an invalid host", host)
		}
	}
}
