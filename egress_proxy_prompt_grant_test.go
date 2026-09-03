package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newPromptingProxy is newTestProxy with the unknown-host prompt turned on, which
// is the path both tests below are about.
func newPromptingProxy(t *testing.T, allowHosts []string) *EgressProxy {
	t.Helper()
	p := newTestProxy(t, "proxy", allowHosts)
	p.policy.Isolation.Network.PromptUnknown = true
	return p
}

func auditContains(t *testing.T, nvxHome, event string) bool {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(nvxHome, "audit.log"))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), event)
}

// A loopback destination is never offered at the prompt.
//
// The prompt is raised by whatever the sandbox is running, so the thing asking is
// the untrusted code. Localhost is where services that take no credentials listen
// — Postgres, Redis, dev servers, other agents' MCP servers — so a postinstall
// being able to raise "allow outbound connection to 127.0.0.1:5432?" is a
// postinstall being able to ask for the developer's database.
//
// The audit event is what distinguishes this from an ordinary denial: a test
// process is non-interactive, so PromptTrustBoundary would refuse anyway and a
// bare false would prove nothing about whether the prompt was reached.
func TestLoopbackIsNeverOfferedAtThePrompt(t *testing.T) {
	// NVX_TRUST_YES would approve any prompt that IS reached, so if the loopback
	// branch ever stops short-circuiting, this test fails loudly rather than
	// passing on the non-interactive denial.
	t.Setenv("NVX_TRUST_YES", "1")

	for _, host := range []string{"127.0.0.1", "localhost", "::1"} {
		p := newPromptingProxy(t, nil)
		if p.allowed(parseHostPortSpec(host, 5432), nil) {
			t.Errorf("%s:5432 was granted through the prompt; untrusted code can ask for a local service", host)
		}
		if !auditContains(t, p.nvxHome, "egress_deny_loopback_prompt") {
			t.Errorf("%s:5432 was denied, but not by the loopback rule -- the prompt path was still entered", host)
		}
		if len(p.prompted) != 0 {
			t.Errorf("%s:5432 recorded a prompt; the refusal must come before asking", host)
		}
	}
}

// ...and allowlisting it in the policy still works, so this is a restriction on
// how loopback is granted rather than on whether it can be.
func TestAllowlistedLoopbackStillWorksWithPromptingOn(t *testing.T) {
	t.Setenv("NVX_TRUST_YES", "1")
	p := newPromptingProxy(t, []string{"127.0.0.1:5432"})
	if !p.allowed(parseHostPortSpec("127.0.0.1", 5432), nil) {
		t.Error("an allowlisted local service must stay reachable; refusing it pushes people to --no-sandbox")
	}
}

// Approving a host at the prompt lasts for this run and is not written down.
//
// It used to call persistNetworkAllowHost, so one yes granted the host for ever:
// an acceptance pass approved a destination, then reached it in a later run with
// no prompt and no trust environment at all. The prompt said "allow outbound
// connection to X?", mentioned no persistence, and the field it set is named
// `session`.
func TestAPromptedApprovalDoesNotOutliveTheRun(t *testing.T) {
	t.Setenv("NVX_TRUST_YES", "1")
	p := newPromptingProxy(t, nil)

	if !p.allowed(parseHostPortSpec("example.com", 443), nil) {
		t.Fatal("NVX_TRUST_YES did not approve the prompt; the rest of this test would prove nothing")
	}
	if !p.session["example.com:443"] {
		t.Error("an approved host must be permitted for the remainder of the run")
	}

	// Nothing may have been written to the grants store.
	grants := loadProjectGrants(p.nvxHome, projectScopeDir())
	for _, h := range grants.AllowHosts {
		if strings.EqualFold(strings.TrimSpace(h), "example.com:443") {
			t.Fatal("a prompted approval was persisted; one yes becomes a permanent grant again")
		}
	}
	if auditContains(t, p.nvxHome, "grant_added") {
		t.Error("a prompted approval recorded a durable grant in the audit log")
	}
}
