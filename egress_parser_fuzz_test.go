package main

import (
	"encoding/base64"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// The egress proxy parses bytes the sandboxed process chose.
//
// Everything else nvx parses comes from a person: a policy file, a command line,
// a registry answer. These two do not. A contained install script speaks SOCKS5
// and HTTP CONNECT to this proxy, and the proxy runs in the PARENT, outside the
// containment, with real network access -- so a panic here takes down the
// supervisor of a running sandbox, and a hang holds a developer's install open
// indefinitely.
//
// Unit tests covered the shapes someone thought to write down. These cover the
// ones nobody did.
//
// Two properties are asserted, and neither is "the parse is correct": a parser
// refusing malformed input is doing its job. What must hold is that no input
// makes the handler panic, and no input makes it fail to return.

// fuzzProxy builds a proxy that refuses every destination and can reach nothing.
//
// Three deliberate choices keep a fuzz run off the network entirely. The
// allowlist is empty and prompt_unknown is false, so allowed() refuses before
// dialVetted is ever called -- meaning no fuzz input can open a connection.
// resolveEgressTarget is stubbed, because both handlers resolve the host BEFORE
// consulting the allowlist, and without this every generated hostname would be a
// real DNS query. And nvxHome is empty, which makes auditLog a no-op rather than
// writing a file per iteration.
func fuzzProxy(f *testing.F) *EgressProxy {
	f.Helper()

	prevResolve := resolveEgressTarget
	resolveEgressTarget = func(host string) ([]net.IP, error) {
		// A fixed, ordinary address: not loopback and not link-local, so the
		// refusal comes from the allowlist rather than from an earlier guard, and
		// the allowlist path is what gets exercised.
		return []net.IP{net.IPv4(203, 0, 113, 1)}, nil
	}
	prevQuiet := quietFlag
	quietFlag = true
	f.Cleanup(func() {
		resolveEgressTarget = prevResolve
		quietFlag = prevQuiet
	})

	policy := DefaultPolicy()
	policy.Isolation.Network.Mode = "proxy"
	policy.Isolation.Network.PromptUnknown = false
	policy.Isolation.Network.AllowHosts = nil

	return &EgressProxy{
		token:    "0123456789abcdef0123456789abcdef",
		allow:    map[string]bool{},
		session:  map[string]bool{},
		prompted: map[string]bool{},
		policy:   policy,
		nvxHome:  "", // auditLog returns immediately on an empty home
	}
}

// driveHandler feeds input to a connection handler and reports whether it
// returned.
//
// net.Pipe is synchronous, so both directions need draining or the handler
// blocks writing its refusal and the test deadlocks on the thing it is meant to
// measure. The client closes after writing, which is what gives the handler its
// EOF.
func driveHandler(handler func(net.Conn), input []byte) bool {
	client, server := net.Pipe()

	go func() {
		_, _ = io.Copy(io.Discard, client) // the refusal nobody reads
	}()
	go func() {
		_, _ = client.Write(input)
		_ = client.Close()
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		handler(server)
	}()

	select {
	case <-done:
		_ = server.Close()
		return true
	case <-time.After(5 * time.Second):
		// Leaks the goroutine deliberately: the point is to report the hang, and
		// there is no way to interrupt a handler blocked on its own read.
		_ = server.Close()
		return false
	}
}

// FuzzSOCKSHandshake drives the SOCKS5 path.
//
// The parser reads a method count, a username length and a password length
// straight out of the input and uses each as a slice bound into one 262-byte
// buffer. Those bounds are correct by inspection -- 255 + 2 fits -- which is
// exactly the kind of reasoning worth checking with something that does not
// reason.
func FuzzSOCKSHandshake(f *testing.F) {
	p := fuzzProxy(f)

	// A well-formed negotiation, so the fuzzer starts from a shape that reaches
	// deep into the parser rather than being refused at byte two.
	f.Add([]byte{0x05, 0x01, 0x02, 0x01, 0x03, 'n', 'v', 'x', 0x04, 'a', 'b', 'c', 'd'})
	// Domain-name address type, the branch with the length-prefixed copy.
	f.Add([]byte{0x05, 0x01, 0x00, 0x05, 0x01, 0x00, 0x03, 0x0b, 'e', 'x', 'a', 'm', 'p', 'l', 'e', '.', 'c', 'o', 'm', 0x01, 0xbb})
	// The boundaries: a maximal method count and a maximal domain length.
	f.Add([]byte{0x05, 0xff})
	f.Add([]byte{0x05, 0x01, 0x02, 0x01, 0xff})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, input []byte) {
		if !driveHandler(p.handleSOCKSConn, input) {
			t.Fatalf("handleSOCKSConn did not return within 5s for %d bytes: %q", len(input), input)
		}
	})
}

// FuzzHTTPConnectRequest drives the CONNECT path.
//
// This one has more string handling than the SOCKS side -- a request line split
// on whitespace, a host:port split, a header loop reading until a blank line --
// and it is the path every proxy-aware tool actually takes.
func FuzzHTTPConnectRequest(f *testing.F) {
	p := fuzzProxy(f)

	f.Add([]byte("CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n"))
	f.Add([]byte("CONNECT example.com:443 HTTP/1.1\r\nProxy-Authorization: Basic bnZ4OnNlY3JldA==\r\n\r\n"))
	f.Add([]byte("GET http://example.com/ HTTP/1.1\r\n\r\n"))
	// A request line with no port, and one with nothing after the method.
	f.Add([]byte("CONNECT example.com HTTP/1.1\r\n\r\n"))
	f.Add([]byte("CONNECT\r\n\r\n"))
	f.Add([]byte(""))

	f.Fuzz(func(t *testing.T, input []byte) {
		if !driveHandler(p.handleHTTPConn, input) {
			t.Fatalf("handleHTTPConn did not return within 5s for %d bytes: %q", len(input), input)
		}
	})
}

// FuzzValidEgressHost covers the guard on its own.
//
// It decides what reaches a terminal prompt and the audit log, so it is the one
// function here whose ANSWER matters rather than only its liveness: anything it
// accepts is printed. The assertion is therefore stronger -- an accepted host
// must contain nothing that could rewrite the line a person is reading.
func FuzzValidEgressHost(f *testing.F) {
	f.Add("example.com")
	f.Add("sub.domain.example.com.")
	f.Add("127.0.0.1")
	f.Add("::1")
	f.Add("under_score.example")
	f.Add("bad host")
	f.Add("evil\r\nX-Injected: 1")
	f.Add("\x1b[2Jcleared")
	f.Add("")

	f.Fuzz(func(t *testing.T, host string) {
		if !validEgressHost(host) {
			return
		}
		for _, r := range host {
			if r < 0x20 || r == 0x7f {
				t.Fatalf("validEgressHost accepted %q, which carries a control character (%#U) and would be printed to a terminal", host, r)
			}
			if r > 0x7f {
				t.Fatalf("validEgressHost accepted %q, which is not ASCII; a DNS name reaching here is punycoded", host)
			}
		}
		if len(host) > 253 {
			t.Fatalf("validEgressHost accepted a %d-byte name, over the 253-byte limit", len(host))
		}
	})
}

// FuzzProxyAuthorization covers the credential check.
//
// Reachable by any local process on the TCP listener, so it is fed hostile input
// by construction: it must never panic on a malformed header -- base64 of
// arbitrary bytes included -- and must never accept one that does not carry this
// session's exact credential.
//
// The second half is the assertion that matters. A check refusing everything
// would be safe; one accepting anything else is the hole the token was added to
// close, where a sibling sandbox borrows this session's allowlist. So an
// acceptance is decoded here and compared against the credential, rather than
// merely counted.
func FuzzProxyAuthorization(f *testing.F) {
	const token = "0123456789abcdef0123456789abcdef"
	p := &EgressProxy{token: token}
	valid := base64.StdEncoding.EncodeToString([]byte(proxyAuthUser + ":" + token))

	f.Add("Basic " + valid)
	f.Add("basic " + valid) // the scheme is case-insensitive
	f.Add("Basic " + base64.StdEncoding.EncodeToString([]byte("nvx:wrong")))
	f.Add("Basic")
	f.Add("Basic !!!not base64!!!")
	f.Add("Bearer " + token)
	f.Add("")

	f.Fuzz(func(t *testing.T, header string) {
		if !p.authorized(header) {
			return
		}
		scheme, encoded, ok := strings.Cut(strings.TrimSpace(header), " ")
		if !ok || !strings.EqualFold(scheme, "Basic") {
			t.Fatalf("authorized(%q) accepted a header that is not Basic auth", header)
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
		if err != nil {
			t.Fatalf("authorized(%q) accepted a header whose payload is not base64", header)
		}
		user, pass, ok := strings.Cut(string(raw), ":")
		if !ok || user != proxyAuthUser || pass != token {
			t.Fatalf("authorized(%q) accepted %q:%q, which is not this session's credential", header, user, pass)
		}
	})
}
