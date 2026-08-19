package main

import (
	"os"
	"testing"
)

// withEnv sets an environment variable for one test and restores it after.
func withEnv(t *testing.T, key, value string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, old)
			return
		}
		_ = os.Unsetenv(key)
	})
}

// TestBlanketYesDoesNotWidenTheTrustBoundary is the fix for the worst finding an
// independent acceptance pass produced.
//
// Two prompts decide the security model rather than a step inside it: trusting a
// project's own .nvx-policy.json when it loosens settings, and adding a host to
// the egress allowlist. Both were covered by -y / NVX_YES, and --agent-mode sets
// that yes -- so the mode built for AI agents, which clone repositories nobody
// has read, auto-approved a repository's request to switch containment off.
// Measured against the shipped binary: a policy carrying
// {"isolation":{"enabled":false}} was refused without the flag and silently
// trusted with it, and arbitrary egress hosts were approved and persisted.
//
// `go test` runs with stdin not a terminal, so these calls exercise exactly the
// path a CI job or an agent harness takes.
func TestBlanketYesDoesNotWidenTheTrustBoundary(t *testing.T) {
	for _, key := range []string{"NVX_YES", "NVX_AGENT_MODE"} {
		t.Run(key, func(t *testing.T) {
			withEnv(t, "NVX_TRUST_YES", "")
			withEnv(t, key, "true")

			// yesFlag is what --agent-mode and -y both set at startup.
			old := yesFlag
			yesFlag = true
			t.Cleanup(func() { yesFlag = old })

			if PromptTrustBoundary("Trust a project policy that disables containment?") {
				t.Errorf("%s approved a request that widens the trust boundary; a repository could "+
					"switch its own sandbox off under --agent-mode", key)
			}
		})
	}
}

// TestOrdinaryPromptsStillHonourYes keeps the fix narrow. -y exists so installs
// do not stall on vulnerability and install-script confirmations, and breaking
// that would push people to --no-sandbox, which is worse than the prompt.
func TestOrdinaryPromptsStillHonourYes(t *testing.T) {
	withEnv(t, "NVX_YES", "true")
	if !PromptYesNo("Proceed with installation despite active vulnerabilities?") {
		t.Error("NVX_YES no longer approves an ordinary prompt; non-interactive installs would stall")
	}
}

// TestTrustBoundaryHasADeliberateOptIn covers the escape hatch. Someone pinning
// their own policy in CI has a way through -- it is just not a variable that gets
// set by habit, which is the property that made NVX_YES the wrong door.
func TestTrustBoundaryHasADeliberateOptIn(t *testing.T) {
	withEnv(t, "NVX_TRUST_YES", "true")
	if !PromptTrustBoundary("Trust this project policy?") {
		t.Error("NVX_TRUST_YES did not approve a trust-boundary prompt, so there is no way to " +
			"pin a policy non-interactively at all")
	}
}

// TestTrustBoundaryDeniesWhenNobodyIsThere pins the default. With no opt-in and
// no terminal, the answer is no.
func TestTrustBoundaryDeniesWhenNobodyIsThere(t *testing.T) {
	withEnv(t, "NVX_TRUST_YES", "")
	withEnv(t, "NVX_YES", "")
	old := yesFlag
	yesFlag = false
	t.Cleanup(func() { yesFlag = old })

	if PromptTrustBoundary("Trust this project policy?") {
		t.Error("a trust-boundary prompt was approved with no opt-in and no terminal")
	}
}
