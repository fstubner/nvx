package main

import (
	"strings"
	"testing"
)

// A misspelt key silently removes the protection it was meant to configure.
//
// `"blocked_packges"` parses, exits 0, and blocks nothing: encoding/json ignores
// fields it has no home for, so the file reads as valid and the blocklist is
// empty. Every protection here is opt-in through a key name, so every one of them
// can be switched off by one wrong letter that looks like it worked.
func TestAMisspeltPolicyKeyIsReported(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantKey     string
		wantNearest string
	}{
		{
			"the blocklist",
			`{"blocked_packges":["evil"]}`,
			"blocked_packges", "blocked_packages",
		},
		{
			"a nested setting",
			`{"isolation":{"network":{"mdoe":"open"}}}`,
			"isolation.network.mdoe", "isolation.network.mode",
		},
		{
			"typosquatting toggle",
			`{"typosquatting":{"enabld":false}}`,
			"typosquatting.enabld", "typosquatting.enabled",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			unknown := unknownPolicyKeys([]byte(tc.body))
			found := false
			for _, k := range unknown {
				if k == tc.wantKey {
					found = true
				}
			}
			if !found {
				t.Fatalf("%q was not reported as unknown; got %v", tc.wantKey, unknown)
			}
			if near := nearestPolicyKey(tc.wantKey, policyKeyPaths()); near != tc.wantNearest {
				t.Fatalf("suggested %q for %q, want %q", near, tc.wantKey, tc.wantNearest)
			}
		})
	}
}

// Real keys must never be reported, or the warning becomes noise and gets
// ignored — which is the same outcome as not having it.
func TestRealPolicyKeysAreNotReportedAsUnknown(t *testing.T) {
	body := `{
	  "blocked_packages": ["evil"],
	  "enforce_ignore_scripts": true,
	  "typosquatting": {"enabled": true, "max_distance": 2, "trusted_packages": ["react"]},
	  "release_age": {"enabled": true, "min_age_hours": 24},
	  "isolation": {
	    "enabled": true,
	    "level": "strict",
	    "network": {"mode": "proxy", "allow_hosts": ["example.com:443"], "prompt_unknown": false},
	    "filesystem": {"provider": "native", "allow_read_exec": ["/opt/tools"]}
	  }
	}`
	if unknown := unknownPolicyKeys([]byte(body)); len(unknown) != 0 {
		t.Fatalf("a policy using only real keys reported %v as unknown", unknown)
	}
}

// Not every unknown key is a typo, and one that is not must still be reported —
// without a misleading suggestion attached.
func TestAnUnrecognisableKeyIsReportedWithoutASuggestion(t *testing.T) {
	unknown := unknownPolicyKeys([]byte(`{"totally_made_up_setting": 1}`))
	if len(unknown) != 1 || unknown[0] != "totally_made_up_setting" {
		t.Fatalf("got %v, want the one unknown key", unknown)
	}
	if near := nearestPolicyKey("totally_made_up_setting", policyKeyPaths()); near != "" {
		t.Fatalf("suggested %q for a key that resembles nothing; a wrong suggestion is worse than none", near)
	}
}

// The key list is derived from the struct tags, so it cannot drift from the type
// the way a hand-maintained list would.
func TestKnownPolicyKeysComeFromTheStruct(t *testing.T) {
	known := policyKeyPaths()
	for _, want := range []string{
		"blocked_packages",
		"typosquatting.max_distance",
		"release_age.min_age_hours",
		"isolation.network.mode",
		"isolation.filesystem.allow_read_exec",
	} {
		if !known[want] {
			t.Errorf("%q is a real policy key but was not derived from the struct", want)
		}
	}
	if known["-"] || known[""] {
		t.Error("a json:\"-\" field leaked into the known-key set")
	}
	if !strings.Contains(strings.Join(keysOf(known), " "), "isolation") {
		t.Error("nested keys are missing entirely")
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
