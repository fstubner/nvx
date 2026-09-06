package main

import (
	"os"
	"path/filepath"
	"testing"
)

// `lts/*` and `lts/<codename>` name what they name in .nvmrc.
//
// These are canonical .nvmrc contents in the wild -- nvm's own documentation
// uses them -- and nvx resolved neither: `lts/*` was read as a version
// expression and rejected, and `nvx install lts/hydrogen` reported "no release
// found matching query: lts/hydrogen". `lts` on its own worked. Measured by
// the audit against a checked-out project with `lts/*` in .nvmrc: nvx said the
// directory required "Node.js lts/*" and offered `nvx install node@lts/*`,
// which failed the same way.
func TestLTSAliasesResolveAgainstTheReleaseList(t *testing.T) {
	releases := []Release{
		{Version: "v24.1.0", Lts: false},
		{Version: "v22.11.0", Lts: "Jod"},
		{Version: "v22.9.0", Lts: false},
		{Version: "v20.18.0", Lts: "Iron"},
		{Version: "v18.20.4", Lts: "Hydrogen"},
	}
	for _, tc := range []struct{ query, want string }{
		{"lts", "v22.11.0"},
		{"lts/*", "v22.11.0"},
		{"lts/jod", "v22.11.0"},
		{"lts/Hydrogen", "v18.20.4"}, // codenames are case-insensitive, as in nvm
		{"lts/iron", "v20.18.0"},
	} {
		got, err := ResolveVersion(tc.query, releases)
		if err != nil {
			t.Errorf("%q: %v", tc.query, err)
			continue
		}
		if got.Version != tc.want {
			t.Errorf("%q resolved to %s, want %s", tc.query, got.Version, tc.want)
		}
	}
	if _, err := ResolveVersion("lts/argon", releases); err == nil {
		t.Error("an LTS codename with no release in the list resolved to something")
	}
}

// And against what is installed, from the LTS marker each install records.
func TestLTSAliasesResolveAgainstInstalledVersions(t *testing.T) {
	nvxHome := tempDir(t)
	for v, codename := range map[string]string{
		"v24.1.0":  "",
		"v22.11.0": "Jod",
		"v20.18.0": "Iron",
	} {
		dir := filepath.Join(nvxHome, "versions", "node", v)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if codename != "" {
			if err := os.WriteFile(filepath.Join(dir, ltsMarkerName), []byte(codename+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	node := Providers["node"]
	for _, tc := range []struct{ query, want string }{
		{"lts", "v22.11.0"},
		{"lts/*", "v22.11.0"},
		{"lts/iron", "v20.18.0"},
		{"lts/JOD", "v22.11.0"},
	} {
		got, err := resolveLocalVersion(node, tc.query, nvxHome)
		if err != nil {
			t.Errorf("%q: %v", tc.query, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%q resolved to %s, want %s", tc.query, got, tc.want)
		}
	}
	if _, err := resolveLocalVersion(node, "lts/hydrogen", nvxHome); err == nil {
		t.Error("an LTS codename with nothing installed for it resolved to something")
	}
}
