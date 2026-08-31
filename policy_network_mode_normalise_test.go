package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// isolation.network.mode leaves normalizePolicy canonical, whatever was written.
//
// parseNetworkMode trims and lowercases BEFORE validating, so "offline " is a
// valid mode and returns ok -- and the write-back used to sit only on the invalid
// branch, so the padded string survived into the policy that everything else
// reads. Readers disagreed about it: networkModeRank trims, so the policy ranked
// as stricter than the default and merged with no trust prompt, while the Linux
// namespace and seccomp readers lowercased without trimming and fell through to
// their "do nothing" arms.
//
// A project could therefore ask for something apparently STRICTER than the
// default and get unrestricted host network on Linux, with no prompt, because by
// the ranking's measure nothing had widened.
//
// This runs on every platform on purpose. The damage lands on Linux, the cause is
// in platform-independent policy code, and the machine this was written on cannot
// execute the Linux half.
func TestNetworkModeIsCanonicalAfterNormalisation(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"offline ", "offline"},
		{" offline", "offline"},
		{"\toffline\n", "offline"},
		{"OFFLINE", "offline"},
		{" Proxy ", "proxy"},
		{"LOOPBACK ", "loopback"},
		{" open", "open"},
		{"proxy", "proxy"},
		{"", "proxy"},        // unset defaults to proxy
		{"offlin", "proxy"},  // a typo narrows to the restrictive default
		{"banana", "proxy"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			p := DefaultPolicy()
			p.Isolation.Network.Mode = tc.in
			normalizePolicy(&p)
			if got := p.Isolation.Network.Mode; got != tc.want {
				t.Fatalf("mode %q normalised to %q, want %q; a reader that does not trim "+
					"will fall through to its default arm and contain nothing", tc.in, got, tc.want)
			}
		})
	}
}

// The same thing through the real entry point, because normalizePolicy is not
// what a user's file goes through -- LoadPolicy is, and a project policy takes a
// merge path of its own.
func TestAPaddedModeInAProjectPolicyLoadsCanonical(t *testing.T) {
	tmp := tempDir(t)
	projectDir := filepath.Join(tmp, "project")
	nvxHome := filepath.Join(tmp, ".nvx")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nvxHome, 0o755); err != nil {
		t.Fatal(err)
	}

	body, err := json.Marshal(map[string]any{
		"isolation": map[string]any{
			"network": map[string]any{"mode": "offline "},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, ".nvx-policy.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWd) }()
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadPolicy(nvxHome)
	if err != nil {
		t.Fatalf("LoadPolicy: %v", err)
	}
	if got := loaded.Isolation.Network.Mode; got != "offline" {
		t.Fatalf("project policy mode loaded as %q, want %q -- the padded value reached "+
			"the readers that decide whether to build a network namespace", got, "offline")
	}
}

// Asking for something stricter than the default is still not a loosening, so it
// still must not prompt. The bug was never that offline prompted; it was that
// offline was announced and open was delivered.
func TestAskingForAStricterModeIsNotALoosening(t *testing.T) {
	before := DefaultPolicy()
	normalizePolicy(&before)

	for _, mode := range []string{"offline ", "offline", " loopback"} {
		after := DefaultPolicy()
		after.Isolation.Network.Mode = mode
		normalizePolicy(&after)
		if policyLoosens(before, after) {
			t.Errorf("mode %q counted as loosening; it is stricter than the default", mode)
		}
	}

	// ...and open still is one, padded or not, or the prompt stops protecting the
	// case it exists for.
	for _, mode := range []string{"open", "open ", " OPEN "} {
		after := DefaultPolicy()
		after.Isolation.Network.Mode = mode
		normalizePolicy(&after)
		if !policyLoosens(before, after) {
			t.Errorf("mode %q did not count as loosening; a project could switch the network wide open with no prompt", mode)
		}
	}
}
