package main

import (
	"strings"
	"testing"
)

func envValue(env []string, key string) (string, bool) {
	for _, kv := range env {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], key) {
			return parts[1], true
		}
	}
	return "", false
}

// A variable the project asked for survives containment; one it did not still
// does not.
//
// Without the allow list the only way to get a variable into a contained run was
// --no-sandbox, which answers "my build needs NODE_ENV" by switching the sandbox
// off. Both halves are asserted together: a pass-through that quietly passed
// everything would satisfy the first half alone.
func TestAVariableTheProjectNamesSurvivesContainment(t *testing.T) {
	t.Setenv("NVX_TEST_PASSED", "kept")
	t.Setenv("NVX_TEST_UNNAMED", "dropped")

	res := scrubEnvironmentAllowing("", []string{"NVX_TEST_PASSED"})

	if got, ok := envValue(res.Env, "NVX_TEST_PASSED"); !ok || got != "kept" {
		t.Errorf("a variable named in isolation.environment.allow did not reach the sandbox (got %q, present=%v)", got, ok)
	}
	if _, ok := envValue(res.Env, "NVX_TEST_UNNAMED"); ok {
		t.Error("a variable the policy never named was passed in; the scrub is not filtering")
	}
	if !containsString(res.Dropped, "NVX_TEST_UNNAMED") {
		t.Errorf("the dropped list does not mention NVX_TEST_UNNAMED, so nothing could report it: %v", res.Dropped)
	}
	if containsString(res.Dropped, "NVX_TEST_PASSED") {
		t.Error("a variable that WAS passed through is listed as dropped; the report would be a lie")
	}
}

// A credential cannot be smuggled in by naming it in the policy.
//
// This is the security boundary of the whole feature. .nvx-policy.json is a file
// in the repository, so if allow could name AWS_SECRET_ACCESS_KEY, a pull request
// could add one line and hand a cloud credential to whatever a package's install
// script runs -- the exact transfer the scrub exists to prevent. The sensitive
// prefixes outrank the allow list, and saying so out loud is part of it: silently
// ignoring the entry would leave someone believing it worked.
func TestNamingACredentialInThePolicyIsRefusedNotHonoured(t *testing.T) {
	t.Setenv("AWS_SECRET_ACCESS_KEY", "shh")
	t.Setenv("GITHUB_TOKEN", "shh")

	res := scrubEnvironmentAllowing("", []string{"AWS_SECRET_ACCESS_KEY", "GITHUB_TOKEN", "CI"})

	for _, secret := range []string{"AWS_SECRET_ACCESS_KEY", "GITHUB_TOKEN"} {
		if v, ok := envValue(res.Env, secret); ok {
			t.Errorf("%s reached the contained environment (value length %d). A policy file must not be able "+
				"to hand a credential to package code.", secret, len(v))
		}
		if !containsString(res.Refused, secret) {
			t.Errorf("%s was blocked but not reported as refused, so the policy author is never told "+
				"their entry did nothing: refused=%v", secret, res.Refused)
		}
	}
	// The non-sensitive entry in the same list is unaffected.
	if containsString(res.Refused, "CI") {
		t.Error("CI was reported as refused; only sensitive prefixes should be")
	}
}

// Only variables that change behaviour reach the screen.
//
// Every contained run on Windows drops ~96 variables, nearly all of it OS
// furniture (COMPUTERNAME, PROCESSOR_LEVEL). Naming them all on every run would
// be noise nobody reads, which is its own way of saying nothing.
func TestOnlyBehaviourChangingVariablesAreWorthPrinting(t *testing.T) {
	dropped := []string{"COMPUTERNAME", "PROCESSOR_LEVEL", "CI", "ONEDRIVE", "NODE_ENV", "npm_config_yes"}
	notable := notableDropped(dropped)

	for _, want := range []string{"CI", "NODE_ENV"} {
		if !containsString(notable, want) {
			t.Errorf("%q changes how tools behave and would not be reported: %v", want, notable)
		}
	}
	// npm_config_* is npm's own plumbing, set for every child npm spawns, and nvx
	// shims npx. Treating it as notable fired the warning on 143 of 193 contained
	// runs on a machine running npx-based MCP servers -- a warning on essentially
	// every server start, which is noise, not a warning.
	for _, noise := range []string{"COMPUTERNAME", "PROCESSOR_LEVEL", "ONEDRIVE", "npm_config_yes"} {
		if containsString(notable, noise) {
			t.Errorf("%q is OS furniture and would print on every single contained run: %v", noise, notable)
		}
	}

	// Proxy variables are deliberately absent: nvx replaces them itself, so
	// reporting them would announce a breakage that did not happen.
	if got := notableDropped([]string{"HTTP_PROXY", "HTTPS_PROXY"}); len(got) != 0 {
		t.Errorf("proxy variables were reported as casualties, but nvx sets its own: %v", got)
	}
}

// Adding an entry needs the same approval an egress host does.
//
// A project-local file that widens what leaves the machine is not something a
// checkout should be able to do silently. allow_read_exec already works this way;
// an allow list that skipped the gate would be a hole beside a door.
func TestAddingAPassThroughCountsAsLoosening(t *testing.T) {
	before := Policy{}
	after := Policy{}
	after.Isolation.Environment.Allow = []string{"NODE_ENV"}

	if !policyLoosens(before, after) {
		t.Error("adding isolation.environment.allow did not count as loosening, so a project file could " +
			"add one with no approval")
	}
	if policyLoosens(after, after) {
		t.Error("an unchanged policy was reported as loosening")
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
