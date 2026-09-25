package nvx

import (
	"strings"
	"testing"
)

// Bun resolves the same version expressions Node does.
//
// Bun's install path understood an exact version and a string prefix, and
// nothing else, so `nvx install bun@^1.1` and a package.json with
// "engines": {"bun": ">=1.1.0"} both failed with "no Bun release matches"
// against a release list that had plenty. Node's path falls through to the
// shared range matcher after its exact and prefix checks; Bun now does too.
func TestBunInstallResolvesRanges(t *testing.T) {
	nvxHome := tempDir(t)
	// Fresh, so fetchBunReleases serves it without a network call; offline in
	// case it ever does not.
	writeBunCache(t, nvxHome, 0, "v1.2.19", "v1.2.0", "v1.1.38", "v1.1.2", "v1.0.36", "v0.8.1")
	offlineBun(t)

	for _, tc := range []struct{ query, want string }{
		// Ranges, which used to fail.
		{"^1.1", "v1.2.19"},
		{">=1.1.0", "v1.2.19"},
		{">=1.0 <1.2", "v1.1.38"},
		{"~1.1.2", "v1.1.38"},
		{"1.x", "v1.2.19"},
		{"^0.8 || ^1.0.0", "v1.2.19"},
		// Exact and prefix, which must not change. An exact version is taken
		// as given even when it is not in the list, because that path builds
		// the download URL without consulting it.
		{"1.1", "v1.1.38"},
		{"v1.0.36", "v1.0.36"},
		{"1.2.5", "v1.2.5"},
		{"latest", "v1.2.19"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			got, err := (BunProvider{}).resolveInstallVersion(tc.query, nvxHome)
			if err != nil {
				t.Fatalf("resolveInstallVersion(%q): %v", tc.query, err)
			}
			if got != tc.want {
				t.Errorf("resolveInstallVersion(%q) = %q, want %q", tc.query, got, tc.want)
			}
		})
	}

	// A range nothing satisfies still says so, and a syntax nvx does not read
	// is named rather than reported as a missing release.
	if _, err := (BunProvider{}).resolveInstallVersion("^3", nvxHome); err == nil ||
		!strings.Contains(err.Error(), "no Bun release matches") {
		t.Errorf("^3 against a list topping out at 1.2.19: got %v, want a no-match error", err)
	}
	if _, err := (BunProvider{}).resolveInstallVersion("1.0 - 1.2", nvxHome); err == nil ||
		!strings.Contains(err.Error(), "hyphen") {
		t.Errorf("a hyphen range: got %v, want the error to name hyphen ranges", err)
	}
}
