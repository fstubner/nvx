package main

import "testing"

func TestParseLandlockExecArgs(t *testing.T) {
	guest, work, nvx, mode, port, cmd, args, ok := parseLandlockExecArgs([]string{
		"--guest-home=/guest",
		"--work-dir=/work",
		"--nvx-home=/nvx",
		"--network-mode=proxy",
		"--proxy-port=8080",
		"--",
		"/bin/node",
		"-v",
	})
	if !ok {
		t.Fatal("expected ok")
	}
	if guest != "/guest" || work != "/work" || nvx != "/nvx" || mode != "proxy" || port != 8080 || cmd != "/bin/node" {
		t.Fatalf("unexpected parse: %q %q %q %q %d %q", guest, work, nvx, mode, port, cmd)
	}
	if len(args) != 1 || args[0] != "-v" {
		t.Fatalf("unexpected args: %v", args)
	}
}

func TestNormalizeAllowEntry(t *testing.T) {
	if got := normalizeAllowEntry("Registry.npmjs.org:443"); got != "registry.npmjs.org:443" {
		t.Fatalf("got %q", got)
	}
	if got := normalizeAllowEntry("localhost"); got != "localhost:*" {
		t.Fatalf("got %q", got)
	}
}

func TestShouldSandbox(t *testing.T) {
	policy := DefaultPolicy()
	if !shouldSandbox("node", policy, shimOptions{}) {
		t.Fatal("expected node to sandbox by default")
	}
	if shouldSandbox("node", policy, shimOptions{noSandbox: true}) {
		t.Fatal("expected --no-sandbox to skip")
	}
	policy.Isolation.Enabled = false
	if shouldSandbox("npm", policy, shimOptions{}) {
		t.Fatal("expected disabled isolation to skip")
	}
}

func TestNetworkAllowlist(t *testing.T) {
	p := DefaultPolicy()
	p.Isolation.Network.AllowHosts = []string{"localhost:5432"}
	list := p.NetworkAllowlist(Providers["node"])
	found := false
	for _, e := range list {
		if e == "localhost:5432" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected localhost:5432 in allowlist: %v", list)
	}
}
