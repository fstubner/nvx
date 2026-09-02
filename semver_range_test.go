package main

import (
	"strings"
	"testing"
)

// The installed set the audit ran against, plus a 2.x to catch prefix matching
// that is really string matching.
var installed = []string{"v20.20.2", "v22.11.0", "v22.23.2", "v24.20.0", "v26.8.1", "v2.5.0"}

func TestRangeResolvesAgainstWhatIsInstalled(t *testing.T) {
	for _, tc := range []struct{ expr, want string }{
		// The two the audit measured picking the wrong version. ">=18 <25" wanted
		// to DOWNLOAD 18 with three satisfying versions already on disk; "^20 || ^22"
		// chose 20 over the newer 22 that also satisfies it.
		{">=18 <25", "v24.20.0"},
		{"^20 || ^22", "v22.23.2"},

		{"22", "v22.23.2"},
		{"22.11", "v22.11.0"},
		{"v22.23.2", "v22.23.2"},
		{"22.x", "v22.23.2"},
		{"22.*", "v22.23.2"},
		{"^22.11.0", "v22.23.2"},
		{"~22.11.0", "v22.11.0"},
		{"~22.11", "v22.11.0"},
		{">=24", "v26.8.1"},
		{">24", "v26.8.1"},
		// npm's X-range rule: a partial version in an upper bound shifts to the
		// next part. "<=22" is every 22.x, and ">24" starts at 25 rather than
		// admitting v24.20.0.
		{"<=22", "v22.23.2"},
		{"<22", "v20.20.2"},
		{"<=22.11", "v22.11.0"},
		{">22", "v26.8.1"},
		{"=20.20.2", "v20.20.2"},
		{"20.x", "v20.20.2"},
		{"*", "v26.8.1"},
		{"", "v26.8.1"},
		{"latest", "v26.8.1"},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			got, err := highestMatching(tc.expr, installed)
			if err != nil {
				t.Fatalf("highestMatching(%q) errored: %v", tc.expr, err)
			}
			if got != tc.want {
				t.Errorf("highestMatching(%q) = %s, want %s", tc.expr, got, tc.want)
			}
		})
	}
}

// "22" must not reach v2.5.0. A prefix compare on the string would.
func TestAMajorDoesNotSweepInAnotherByPrefix(t *testing.T) {
	got, err := highestMatching("2", installed)
	if err != nil {
		t.Fatal(err)
	}
	if got != "v2.5.0" {
		t.Errorf(`query "2" resolved to %s, want v2.5.0`, got)
	}
	if got, _ := highestMatching("22", installed); got == "v2.5.0" {
		t.Error(`query "22" reached v2.5.0; the match is on version parts, not on text`)
	}
}

// Nothing satisfying is an error, not a silent nearest-guess.
func TestNoMatchIsAnError(t *testing.T) {
	for _, expr := range []string{"^99", ">=30 <40", "19"} {
		if got, err := highestMatching(expr, installed); err == nil {
			t.Errorf("highestMatching(%q) returned %s; nothing installed satisfies it", expr, got)
		}
	}
}

// What is outside the supported subset must SAY so. A matcher that guesses picks
// a plausible wrong version and is found out much later; one that refuses costs
// a single clear message.
func TestUnsupportedSyntaxIsRefusedByName(t *testing.T) {
	for _, tc := range []struct{ expr, mustMention string }{
		{"18 - 24", "hyphen"},
		{"22.0.0-rc.1", "prerelease"},
		{"^22.0.0+build.5", "build"},
		{"22.1.2.3", "three"},
		{"banana", "not a version number"},
		{">=nonsense", "not a version number"},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			_, err := parseVersionRange(tc.expr)
			if err == nil {
				t.Fatalf("parseVersionRange(%q) accepted syntax it does not implement; it would "+
					"then match against a version it mis-read", tc.expr)
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.mustMention)) {
				t.Errorf("error for %q was %q, which does not tell the user what was wrong "+
					"(expected it to mention %q)", tc.expr, err, tc.mustMention)
			}
		})
	}
}

// The caret and tilde ceilings, including the zero-major rules that differ.
func TestCaretAndTildeCeilings(t *testing.T) {
	for _, tc := range []struct {
		expr    string
		allowed []string
		denied  []string
	}{
		{"^1.2.3", []string{"1.2.3", "1.9.9"}, []string{"1.2.2", "2.0.0"}},
		{"^0.2.3", []string{"0.2.3", "0.2.9"}, []string{"0.3.0", "0.2.2"}},
		{"^0.0.3", []string{"0.0.3"}, []string{"0.0.4", "0.1.0"}},
		{"~1.2.3", []string{"1.2.3", "1.2.9"}, []string{"1.3.0", "1.2.2"}},
		{"~1", []string{"1.0.0", "1.9.9"}, []string{"2.0.0"}},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			r, err := parseVersionRange(tc.expr)
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range tc.allowed {
				v, _, _ := parseSemver(s)
				if !r.allows(v) {
					t.Errorf("%s should satisfy %s", s, tc.expr)
				}
			}
			for _, s := range tc.denied {
				v, _, _ := parseSemver(s)
				if r.allows(v) {
					t.Errorf("%s should NOT satisfy %s", s, tc.expr)
				}
			}
		})
	}
}
