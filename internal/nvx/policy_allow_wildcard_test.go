package nvx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An allow_hosts entry such as "*.example.com:443" read like a subdomain wildcard
// and matched nothing, neither the subdomains nor the apex. The allowlist is
// compared against the exact host name a client asks for, so no client ever sends
// a name with a * in it. Everything to that domain was refused with nothing to
// say why, and the person who wrote the entry believed they had allowed it.
//
// No document promises wildcard hosts, so nvx says that the entry matches
// nothing, and the policy page says the same.

// The warning is about what matching really does, so this pins it. If a wildcard
// host is ever matched, the warning below becomes false and this fails first.
func TestAWildcardHostEntryMatchesNoHost(t *testing.T) {
	p := newTestProxy(t, "proxy", []string{"*.wild.example.test:443"})
	for _, host := range []string{"sub.wild.example.test", "wild.example.test", "evil-wild.example.test", "a.b.wild.example.test"} {
		if p.allowed(parseHostPortSpec(host, 443), publicAddr) {
			t.Errorf("%s:443 was allowed by %q; if wildcard hosts are now matched, the load-time warning and the policy page are wrong",
				host, "*.wild.example.test:443")
		}
	}
}

func TestPolicyLoadSaysAWildcardHostMatchesNothing(t *testing.T) {
	p := DefaultPolicy()
	p.Isolation.Network.AllowHosts = []string{
		"*.warn-allow.example.test:443", // the case in question
		"*",                             // every host, which no entry can say
		"api.*.warn-allow.example.test", // a * in the middle
		"exact.warn-allow.example.test:443",
		"port-wildcard.warn-allow.example.test:*", // * for the PORT is the one wildcard there is
		"no-port.warn-allow.example.test",
	}
	p.Isolation.Network.DefaultAllow = []string{"*.warn-default.example.test:443", "registry.npmjs.org:443"}
	p.Isolation.Network.DefaultAllowSet = true

	out := captureStderrHere(t, func() { normalizePolicy(&p) })

	for _, want := range []string{
		`isolation.network.allow_hosts lists "*.warn-allow.example.test:443"`,
		`isolation.network.allow_hosts lists "*"`,
		`isolation.network.allow_hosts lists "api.*.warn-allow.example.test"`,
		`isolation.network.default_allow lists "*.warn-default.example.test:443"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("no warning containing %q in:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "matches no host") || !strings.Contains(out, "list each host") {
		t.Errorf("the warning does not say the entry matches nothing and that each host must be listed:\n%s", out)
	}
	for _, quiet := range []string{"exact.warn-allow", "port-wildcard.warn-allow", "no-port.warn-allow", "registry.npmjs.org"} {
		if strings.Contains(out, quiet) {
			t.Errorf("an entry that matches fine was reported: %q appears in:\n%s", quiet, out)
		}
	}
}

// Said once per entry. A command loads the policy more than once, and the same
// line twice reads as two problems.
func TestAWildcardHostIsReportedOncePerRun(t *testing.T) {
	p := DefaultPolicy()
	p.Isolation.Network.AllowHosts = []string{"*.once.example.test:443"}
	out := captureStderrHere(t, func() {
		normalizePolicy(&p)
		normalizePolicy(&p)
		normalizePolicy(&p)
	})
	if n := strings.Count(out, `"*.once.example.test:443"`); n != 1 {
		t.Fatalf("the entry was reported %d times, want once:\n%s", n, out)
	}
}

// Through the real entry point and a real file, because that is where a person's
// entry comes from.
func TestLoadPolicyWarnsAboutAWildcardInTheGlobalPolicyFile(t *testing.T) {
	nvxHome := tempDir(t)
	body := `{"isolation":{"network":{"allow_hosts":["*.file.example.test:443","ok.file.example.test:443"]}}}`
	if err := os.WriteFile(filepath.Join(nvxHome, "policy.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// Somewhere with no project policy above it.
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tempDir(t)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	var loaded Policy
	out := captureStderrHere(t, func() {
		var err error
		loaded, err = LoadPolicy(nvxHome)
		if err != nil {
			t.Fatalf("LoadPolicy: %v", err)
		}
	})
	if !strings.Contains(out, `"*.file.example.test:443"`) {
		t.Errorf("loading a policy file with a wildcard host printed no warning for it:\n%s", out)
	}
	if strings.Contains(out, "ok.file.example.test") {
		t.Errorf("the exact entry was reported:\n%s", out)
	}
	// The entry stays in the policy as written. It matches nothing, and removing it
	// would make `nvx policy explain` show something other than the file.
	found := false
	for _, h := range loaded.Isolation.Network.AllowHosts {
		if h == "*.file.example.test:443" {
			found = true
		}
	}
	if !found {
		t.Errorf("the entry was dropped from the loaded policy: %q", loaded.Isolation.Network.AllowHosts)
	}
}
