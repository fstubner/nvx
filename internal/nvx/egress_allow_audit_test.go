package nvx

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
)

// The audit log is documented as the record of what nvx allowed, and it only held
// what was refused and what a person approved at the prompt. A contained install
// reached registry.npmjs.org with nothing written down. A host the policy allows
// is now recorded the first time a run reaches it.
//
// One record per host and port per run, because a package manager makes many
// connections to one registry and a record for each would bury the denials this
// log is read for.

// allowEvents returns the egress_allow records in nvxHome's audit log, as
// "host rule" strings in the order written.
func allowEvents(t *testing.T, nvxHome string) []string {
	t.Helper()
	entries, err := readAuditEntries(nvxHome)
	if err != nil {
		// No file at all is no records, which is a result several tests expect.
		return nil
	}
	var out []string
	for _, e := range entries {
		if e["event"] == "egress_allow" {
			out = append(out, e["host"]+" "+e["rule"])
		}
	}
	return out
}

// proxyWithPolicy starts the real constructor on policy, so the allowlist is built
// the way a run builds it.
func proxyWithPolicy(t *testing.T, policy Policy) *EgressProxy {
	t.Helper()
	p, err := startEgressProxy(context.Background(), policy, Providers["node"], tempDir(t))
	if err != nil {
		t.Fatalf("startEgressProxy: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

var publicAddr = []net.IP{net.ParseIP("104.16.0.1")}

func TestAnAllowedHostIsRecordedOncePerRun(t *testing.T) {
	policy := DefaultPolicy()
	policy.Isolation.Network.PromptUnknown = false
	p := proxyWithPolicy(t, policy)

	for i := 0; i < 5; i++ {
		if !p.allowed(parseHostPortSpec("registry.npmjs.org", 443), publicAddr) {
			t.Fatal("the shipped default registry was refused; the test cannot show what is recorded")
		}
	}
	got := allowEvents(t, p.nvxHome)
	want := []string{"registry.npmjs.org:443 default_allow"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("five connections to one allowed host wrote %q, want %q", got, want)
	}
}

// Each host and port is its own destination, so each is recorded once.
func TestEachAllowedHostAndPortIsRecordedOnce(t *testing.T) {
	policy := DefaultPolicy()
	policy.Isolation.Network.PromptUnknown = false
	// With no port, any port on the host is allowed.
	policy.Isolation.Network.AllowHosts = []string{"mirror.example.test"}
	p := proxyWithPolicy(t, policy)

	for _, hp := range []hostPort{
		parseHostPortSpec("mirror.example.test", 443),
		parseHostPortSpec("mirror.example.test", 8443),
		parseHostPortSpec("mirror.example.test", 443),
		parseHostPortSpec("registry.npmjs.org", 443),
	} {
		if !p.allowed(hp, publicAddr) {
			t.Fatalf("%s:%d was refused", hp.host, hp.port)
		}
	}
	got := strings.Join(allowEvents(t, p.nvxHome), "|")
	want := "mirror.example.test:443 allow_hosts|mirror.example.test:8443 allow_hosts|registry.npmjs.org:443 default_allow"
	if got != want {
		t.Fatalf("records were %q, want %q", got, want)
	}
}

// The record names the setting a person can open and change. An entry in
// allow_hosts is named as that even when default_allow lists the host as well,
// because it is the one somebody wrote.
func TestTheRecordNamesTheRuleThatAllowedTheHost(t *testing.T) {
	policy := DefaultPolicy()
	policy.Isolation.Network.PromptUnknown = false
	policy.Isolation.Network.AllowHosts = []string{"localhost:5432", "REGISTRY.npmjs.org:443"}
	p := proxyWithPolicy(t, policy)

	p.allowed(parseHostPortSpec("registry.npmjs.org", 443), publicAddr)
	p.allowed(parseHostPortSpec("api.osv.dev", 443), publicAddr)
	// Reached as 127.0.0.1 and listed as localhost. The entry that matched is the
	// one named, whatever the request spelled.
	p.allowed(parseHostPortSpec("127.0.0.1", 5432), []net.IP{net.ParseIP("127.0.0.1")})

	got := strings.Join(allowEvents(t, p.nvxHome), "|")
	want := "registry.npmjs.org:443 allow_hosts|api.osv.dev:443 default_allow|127.0.0.1:5432 allow_hosts"
	if got != want {
		t.Fatalf("records were %q, want %q", got, want)
	}
}

// Only an allowed destination is an allow record. A refusal has its own events,
// and an approval at the prompt has its own, written when it was given.
func TestOnlyAnAllowedHostIsRecordedAsAllowed(t *testing.T) {
	t.Run("a refused host", func(t *testing.T) {
		policy := DefaultPolicy()
		policy.Isolation.Network.PromptUnknown = false
		p := proxyWithPolicy(t, policy)
		if p.allowed(parseHostPortSpec("evil.example.test", 443), publicAddr) {
			t.Fatal("a host off the allowlist was allowed")
		}
		if got := allowEvents(t, p.nvxHome); len(got) != 0 {
			t.Errorf("a refused host wrote allow records: %q", got)
		}
	})

	t.Run("an allowlisted name that resolves to a link-local address", func(t *testing.T) {
		policy := DefaultPolicy()
		policy.Isolation.Network.PromptUnknown = false
		policy.Isolation.Network.AllowHosts = []string{"cache.example.test:80"}
		p := proxyWithPolicy(t, policy)
		refuse := func(host string) ([]net.IP, error) {
			return resolveEgressAddresses(host, func(string) ([]net.IP, error) {
				return []net.IP{net.ParseIP("169.254.169.254")}, nil
			})
		}
		if _, ok := p.admit(parseHostPortSpec("cache.example.test", 80), refuse); ok {
			t.Fatal("a name resolving to the metadata address was admitted")
		}
		if got := allowEvents(t, p.nvxHome); len(got) != 0 {
			t.Errorf("a connection refused after the lookup wrote allow records: %q", got)
		}
	})

	t.Run("a host approved at the prompt", func(t *testing.T) {
		t.Setenv("NVX_TRUST_YES", "1")
		p := newPromptingProxy(t, nil)
		for i := 0; i < 3; i++ {
			if !p.allowed(parseHostPortSpec("example.com", 443), publicAddr) {
				t.Fatal("NVX_TRUST_YES did not approve the prompt")
			}
		}
		if got := allowEvents(t, p.nvxHome); len(got) != 0 {
			t.Errorf("a prompted approval also wrote allow records: %q", got)
		}
		if !auditContains(t, p.nvxHome, "egress_allow_prompted") {
			t.Error("the prompted approval itself was not recorded")
		}
	})
}

// network.mode loopback allows loopback by the mode's definition, and is named
// as the reason.
func TestLoopbackModeIsNamedAsTheRule(t *testing.T) {
	p := newTestProxy(t, "loopback", nil)
	if !p.allowed(parseHostPortSpec("127.0.0.1", 8080), []net.IP{net.ParseIP("127.0.0.1")}) {
		t.Fatal("loopback mode refused loopback")
	}
	if got := strings.Join(allowEvents(t, p.nvxHome), "|"); got != "127.0.0.1:8080 mode_loopback" {
		t.Fatalf("records were %q", got)
	}
}

// Connections arrive on one goroutine each, so the first one to a host is a race
// between all of them.
func TestConcurrentFirstConnectionsWriteOneRecord(t *testing.T) {
	policy := DefaultPolicy()
	policy.Isolation.Network.PromptUnknown = false
	p := proxyWithPolicy(t, policy)

	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.allowed(parseHostPortSpec("registry.npmjs.org", 443), publicAddr)
		}()
	}
	wg.Wait()
	if got := allowEvents(t, p.nvxHome); len(got) != 1 {
		t.Fatalf("40 simultaneous first connections wrote %d records: %q", len(got), got)
	}
}

// Through the real listener, with a tunnel that opens, the record is written.
func TestAConnectionThroughTheProxyIsRecorded(t *testing.T) {
	_, port, _ := net.SplitHostPort(echoTarget(t))
	p := proxyAllowing(t, "127.0.0.1:"+port)

	if status, echo := connectThrough(t, p, "127.0.0.1:"+port); !strings.Contains(status, " 200 ") || echo != "ping" {
		t.Fatalf("CONNECT did not tunnel: status %q, echo %q", status, echo)
	}
	if got := allowEvents(t, p.nvxHome); len(got) != 1 || got[0] != "127.0.0.1:"+port+" allow_hosts" {
		t.Fatalf("records after one CONNECT were %q", got)
	}
	if status, _ := connectThrough(t, p, "127.0.0.1:"+port); !strings.Contains(status, " 200 ") {
		t.Fatalf("second CONNECT got %q", status)
	}
	if got := allowEvents(t, p.nvxHome); len(got) != 1 {
		t.Fatalf("a second CONNECT to the same host wrote another record: %q", got)
	}
}

// `nvx audit` has to show which rule allowed the host, not only that something
// was allowed.
func TestAuditShowsTheRuleThatAllowedAHost(t *testing.T) {
	out := formatAuditEntry(map[string]string{
		"time":  "2026-10-07T10:00:00Z",
		"event": "egress_allow",
		"host":  "registry.npmjs.org:443",
		"rule":  "default_allow",
	})
	for _, want := range []string{"egress_allow", "host=registry.npmjs.org:443", "rule=default_allow"} {
		if !strings.Contains(out, want) {
			t.Errorf("the audit line %q does not show %q", out, want)
		}
	}
}
