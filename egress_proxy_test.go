package main

import "testing"

func TestEgressAllowed(t *testing.T) {
	newProxy := func(mode string, promptUnknown bool) *EgressProxy {
		p := &EgressProxy{
			// Include an allowlisted LOOPBACK entry (a local registry) to prove
			// loopback is reachable when explicitly permitted.
			allow:     map[string]bool{"registry.npmjs.org:443": true, "wild.example.com:*": true, "127.0.0.1:4873": true},
			session:   map[string]bool{},
			prompted:  map[string]bool{},
			httpAddr:  "127.0.0.1:59991", // proxy's own listener ports
			socksAddr: "127.0.0.1:59992",
		}
		p.policy.Isolation.Network.Mode = mode
		p.policy.Isolation.Network.PromptUnknown = promptUnknown
		return p
	}

	p := newProxy("proxy", false)
	checks := []struct {
		host string
		port uint16
		want bool
		msg  string
	}{
		{"127.0.0.1", 59991, true, "loopback to the proxy's own port is allowed"},
		{"127.0.0.1", 4873, true, "explicitly allowlisted loopback (local registry)"},
		{"127.0.0.1", 5432, false, "arbitrary loopback service (DB) is NOT auto-allowed"},
		{"registry.npmjs.org", 443, true, "exact allowlist entry"},
		{"wild.example.com", 8080, true, "wildcard host:* entry"},
		{"evil.example.com", 443, false, "unknown host blocked when prompt_unknown=false"},
	}
	for _, c := range checks {
		if got := p.allowed(hostPort{host: c.host, port: c.port}); got != c.want {
			t.Errorf("%s: allowed(%s:%d)=%v, want %v", c.msg, c.host, c.port, got, c.want)
		}
	}

	// Offline mode blocks a non-allowlisted host — including arbitrary loopback.
	off := newProxy("offline", true)
	if off.allowed(hostPort{host: "example.com", port: 443}) {
		t.Error("offline mode should block non-loopback, non-allowlisted egress")
	}
	if off.allowed(hostPort{host: "127.0.0.1", port: 9229}) {
		t.Error("offline mode should block arbitrary loopback (pivot channel closed)")
	}
	// The proxy's own port stays reachable even offline (the child needs it).
	if !off.allowed(hostPort{host: "127.0.0.1", port: 59991}) {
		t.Error("proxy's own port must remain reachable")
	}

	// A session-approved host is allowed.
	s := newProxy("proxy", false)
	s.session["approved.example.com:443"] = true
	if !s.allowed(hostPort{host: "approved.example.com", port: 443}) {
		t.Error("session-approved host should be allowed")
	}
}

func TestIsLoopback(t *testing.T) {
	cases := map[string]bool{
		"localhost":   true,
		"127.0.0.1":   true,
		"::1":         true,
		"127.0.0.5":   true, // 127.0.0.0/8
		"8.8.8.8":     false,
		"example.com": false,
		"":            false,
	}
	for host, want := range cases {
		if got := isLoopback(host); got != want {
			t.Errorf("isLoopback(%q)=%v, want %v", host, got, want)
		}
	}
}
