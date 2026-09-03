package main

import "testing"

// A real package is not a typosquat of a shorter, more popular one.
//
// `nvx install tsc` refused to run: TypeScript's compiler was reported as
// "suspiciously close to popular package ms (edit distance <= 2)", and in a
// non-interactive shell that is a hard stop with no way past it short of
// approving every prompt in the session.
//
// Two things were wrong and both are asserted here. `tsc` and `ms` share one
// letter, so distance 2 across a 2- and 3-character name is not a mistyping at
// all; and the authority test was a pure ratio, so 780,294 weekly downloads
// counted for nothing against 545,506,628.
func TestAnEstablishedPackageIsNotATyposquat(t *testing.T) {
	real := map[string]int{"tsc": 780294, "ms": 545506628}
	orig := weeklyDownloads
	t.Cleanup(func() { weeklyDownloads = orig })
	weeklyDownloads = func(name string) (int, error) { return real[name], nil }

	if got := CheckTyposquattingAuthority("tsc", []string{"ms"}, 2); got != "" {
		t.Errorf("installing tsc was blocked as a typosquat of %q; it has %d weekly downloads",
			got, real["tsc"])
	}
}

// The check still catches an actual squat: a name one edit from a giant, with
// almost no downloads of its own.
func TestARealTyposquatIsStillCaught(t *testing.T) {
	real := map[string]int{"lodahs": 42, "lodash": 60000000}
	orig := weeklyDownloads
	t.Cleanup(func() { weeklyDownloads = orig })
	weeklyDownloads = func(name string) (int, error) { return real[name], nil }

	if got := CheckTyposquattingAuthority("lodahs", []string{"lodash"}, 2); got != "lodash" {
		t.Errorf("a 42-downloads package one edit from lodash was not flagged (got %q). "+
			"The false-positive fix has disabled the check it belongs to.", got)
	}
}

// Short names are close to each other by default, so distance alone cannot mean
// similarity there. This is the guard that runs before any download lookup.
func TestDistanceAcrossShortNamesIsNotSimilarity(t *testing.T) {
	for _, tc := range []struct {
		a, b     string
		dist     int
		wantTypo bool
	}{
		{"tsc", "ms", 2, false}, // one shared letter; the reported bug
		{"ls", "fs", 1, true},   // one edit of two, still a plausible slip
		{"lodahs", "lodash", 2, true},
		{"reactt", "react", 1, true},
		{"vue", "vuex", 1, true},
	} {
		if got := plausibleTypo(tc.a, tc.b, tc.dist); got != tc.wantTypo {
			t.Errorf("plausibleTypo(%q, %q, %d) = %v, want %v", tc.a, tc.b, tc.dist, got, tc.wantTypo)
		}
	}
}
