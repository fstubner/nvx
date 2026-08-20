package main

import (
	"strings"
	"testing"
)

// `npm install npm@latest` carried the literal string "latest" through every
// check, because resolution only replaced an EMPTY query. Reported from real use
// 2026-08-20 as a scary, detail-free advisory list; the noisy output was the
// symptom, and these are the causes:
//
//   - meta.Versions["latest"] misses, so the install-script prompt never appeared
//   - meta.Time["latest"] misses, so the release-age check never fired
//   - OSV was queried for version "latest", so the answer was not about the
//     version being installed
//
// Two security checks silently did nothing for the commonest way to name a
// package, and neither failed loudly. That is the shape this pins.
func TestVersionQueryResolvesToSomethingCheckable(t *testing.T) {
	meta := NpmRegistryMetadata{
		DistTags: map[string]string{
			"latest": "11.19.0",
			"next":   "12.0.2",
			"broken": "99.0.0", // a tag naming a version the registry did not describe
		},
		Versions: map[string]NpmVersionDetails{
			"11.19.0": {},
			"12.0.2":  {},
			"11.11.0": {},
		},
	}

	t.Run("resolves to a version the other checks can look up", func(t *testing.T) {
		cases := map[string]string{
			"":        "11.19.0", // no version given: latest
			"latest":  "11.19.0", // the reported case
			"next":    "12.0.2",  // any dist-tag, not just latest
			"11.11.0": "11.11.0", // an exact version passes through
			" latest": "11.19.0", // whitespace is not a different tag
		}
		for query, want := range cases {
			got, err := resolveVersionQuery(query, meta)
			if err != nil {
				t.Errorf("resolveVersionQuery(%q) errored: %v", query, err)
				continue
			}
			if got != want {
				t.Errorf("resolveVersionQuery(%q) = %q, want %q", query, got, want)
			}
			// The point of resolving: the result must be a key the later checks
			// use. A result that is not in Versions silently disables them.
			if _, ok := meta.Versions[got]; !ok {
				t.Errorf("resolveVersionQuery(%q) returned %q, which is not a published version; "+
					"the install-script and release-age checks would silently do nothing", query, got)
			}
		}
	})

	// Anything unresolvable is an error, not a pass-through. The caller turns it
	// into "could not verify registry metadata ... proceed?", which is honest --
	// nvx cannot check a version it cannot name. Silently continuing with every
	// check disabled is the bug.
	t.Run("refuses what it cannot resolve", func(t *testing.T) {
		for _, query := range []string{"^4.17.0", "~1.2", "1.x", ">=2", "nosuchtag", "broken"} {
			got, err := resolveVersionQuery(query, meta)
			if err == nil {
				t.Errorf("resolveVersionQuery(%q) = %q with no error; an unresolvable query must "+
					"not pass through as if it were a version", query, got)
			}
		}
	})

	t.Run("says so when the registry names no latest", func(t *testing.T) {
		empty := NpmRegistryMetadata{DistTags: map[string]string{}, Versions: map[string]NpmVersionDetails{}}
		if _, err := resolveVersionQuery("", empty); err == nil {
			t.Error("an empty query against metadata with no latest tag must error")
		}
	})

	// An exact version wins over a same-named tag, so a package publishing a tag
	// that collides with a version number cannot redirect the check.
	t.Run("an exact version is not redirected by a tag of the same name", func(t *testing.T) {
		odd := NpmRegistryMetadata{
			DistTags: map[string]string{"11.11.0": "99.99.99"},
			Versions: map[string]NpmVersionDetails{"11.11.0": {}},
		}
		got, err := resolveVersionQuery("11.11.0", odd)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "11.11.0" {
			t.Errorf("got %q, want the exact version 11.11.0 rather than the tag's target", got)
		}
	})

	t.Run("the error names the query so the prompt is actionable", func(t *testing.T) {
		_, err := resolveVersionQuery("^4.17.0", meta)
		if err == nil {
			t.Fatal("expected an error")
		}
		if !strings.Contains(err.Error(), "^4.17.0") {
			t.Errorf("error %q does not name the query the user typed", err)
		}
	})
}
