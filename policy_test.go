package main

import (
	"testing"
	"time"
)

func TestParseLandlockExecArgs(t *testing.T) {
	guest, work, nvx, mode, shimCmd, port, cmd, args, ok := parseLandlockExecArgs([]string{
		"--guest-home=/guest",
		"--work-dir=/work",
		"--nvx-home=/nvx",
		"--network-mode=proxy",
		"--command=node",
		"--proxy-port=8080",
		"--",
		"/bin/node",
		"-v",
	})
	if !ok {
		t.Fatal("expected ok")
	}
	if guest != "/guest" || work != "/work" || nvx != "/nvx" || mode != "proxy" || shimCmd != "node" || port != 8080 || cmd != "/bin/node" {
		t.Fatalf("unexpected parse: %q %q %q %q %q %d %q", guest, work, nvx, mode, shimCmd, port, cmd)
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
	defer func() { noSandboxFlag = false }()

	if !shouldSandbox("node", policy, shimOptions{}) {
		t.Fatal("expected node to sandbox by default")
	}
	// A --no-sandbox smuggled through the wrapped command must NOT bypass.
	if !shouldSandbox("node", policy, shimOptions{payloadNoSandbox: true}) {
		t.Fatal("payload --no-sandbox must not disable the sandbox")
	}
	// Only a leading `nvx --no-sandbox` (noSandboxFlag) bypasses.
	noSandboxFlag = true
	if shouldSandbox("node", policy, shimOptions{}) {
		t.Fatal("expected leading nvx --no-sandbox to skip")
	}
	noSandboxFlag = false

	policy.Isolation.Enabled = false
	if shouldSandbox("npm", policy, shimOptions{}) {
		t.Fatal("expected disabled isolation to skip")
	}
}

func TestReleaseAgePolicyDefaults(t *testing.T) {
	p := DefaultPolicy()
	if !p.ReleaseAgeEnabled() {
		t.Fatal("expected release age enabled by default")
	}
	if p.ReleaseAgeMinHours() != 24 {
		t.Fatalf("expected 24h default, got %d", p.ReleaseAgeMinHours())
	}

	var legacy Policy
	normalizePolicy(&legacy)
	if !legacy.ReleaseAgeEnabled() {
		t.Fatal("expected legacy policy to enable release age")
	}
	if legacy.ReleaseAgeMinHours() != 24 {
		t.Fatalf("expected legacy 24h default, got %d", legacy.ReleaseAgeMinHours())
	}

	disabled := false
	p2 := Policy{ReleaseAge: ReleaseAgePolicy{Enabled: &disabled}}
	normalizePolicy(&p2)
	if p2.ReleaseAgeEnabled() {
		t.Fatal("expected explicit disable")
	}
}

func TestIsTrustedPackage(t *testing.T) {
	p := Policy{Typosquatting: TyposquattingPolicy{TrustedPackages: []string{"Lodash", "@types/node"}}}
	if !p.IsTrustedPackage("lodash") {
		t.Fatal("expected lodash trusted")
	}
	if !p.IsTrustedPackage("@types/node") {
		t.Fatal("expected scoped package trusted")
	}
	if p.IsTrustedPackage("react") {
		t.Fatal("react should not be trusted")
	}
}

func TestPublishAgeShouldWarn(t *testing.T) {
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	pub := now.Add(-2 * time.Hour)
	if !publishAgeShouldWarn(pub, 24, now) {
		t.Fatal("expected warn for 2h old publish within 24h window")
	}
	if publishAgeShouldWarn(pub, 1, now) {
		t.Fatal("expected no warn outside 1h window")
	}
	if publishAgeShouldWarn(time.Time{}, 24, now) {
		t.Fatal("expected no warn for zero publish time")
	}
}

func TestIsolationLevelDefaultsToStandard(t *testing.T) {
	p := DefaultPolicy()
	if p.IsolationLevel() != levelStandard {
		t.Errorf("DefaultPolicy().IsolationLevel() = %v, want standard", p.IsolationLevel())
	}
}

func TestIsolationLevelFromJSON(t *testing.T) {
	p := DefaultPolicy()
	local := Policy{Isolation: IsolationPolicy{Level: "strict"}}
	merged := MergePolicies(p, local)
	if merged.IsolationLevel() != levelStrict {
		t.Errorf("merged.IsolationLevel() = %v, want strict", merged.IsolationLevel())
	}
}

func TestPolicyLoosensOnStrictToStandard(t *testing.T) {
	before := DefaultPolicy()
	before.Isolation.Level = "strict"
	after := before
	after.Isolation.Level = "standard"
	if !policyLoosens(before, after) {
		t.Error("dropping isolation.level from strict to standard should count as loosening")
	}
}

func TestPolicyTightensOnStandardToStrict(t *testing.T) {
	before := DefaultPolicy() // level defaults to standard
	after := before
	after.Isolation.Level = "strict"
	if policyLoosens(before, after) {
		t.Error("raising isolation.level from standard to strict should not count as loosening")
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
