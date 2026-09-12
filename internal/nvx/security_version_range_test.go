package nvx

import (
	"strings"
	"testing"
)

// A semver range resolves to the version npm would install, so the checks run
// against it.
//
// resolveVersionQuery accepted an exact version or a dist-tag and made
// everything else an error. The caller turns that error into "Could not verify
// registry metadata ... Proceed without metadata checks?", which is an
// ordinary prompt, and -y / NVX_YES approve ordinary prompts by design. So
// `npm install lodash@^4 -y` -- a range, the shape most declared dependencies
// take -- skipped the install-script, release-age and OSV checks, with a
// warning nobody unattended was reading. nvx already had a range parser, in
// semver_range.go, for engine constraints; it was never used here.
//
// The version chosen is the highest published one the range allows, which is
// what npm chooses, so the checks describe the thing actually installed.
func TestARangeResolvesToTheVersionNpmWouldInstall(t *testing.T) {
	meta := NpmRegistryMetadata{
		DistTags: map[string]string{"latest": "5.0.0", "next": "6.0.0-beta.1"},
		Versions: map[string]NpmVersionDetails{
			"4.17.0": {}, "4.17.21": {}, "4.18.0": {}, "5.0.0": {}, "6.0.0-beta.1": {},
		},
	}
	for _, tc := range []struct{ query, want string }{
		{"^4.17.0", "4.18.0"},
		{"~4.17.0", "4.17.21"},
		{">=4 <5", "4.18.0"},
		{"4.x", "4.18.0"},
		{"4.17.x", "4.17.21"},
		{"5", "5.0.0"},
		{">=5", "5.0.0"}, // a prerelease is not the highest match unless asked for
		{"latest", "5.0.0"},
		{"4.17.21", "4.17.21"},
	} {
		got, err := resolveVersionQuery(tc.query, meta)
		if err != nil {
			t.Errorf("%q: %v -- the caller would prompt, and -y would skip every check", tc.query, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%q resolved to %q, want %q", tc.query, got, tc.want)
		}
	}
}

// What cannot be resolved is still an error, so the caller still asks. This is
// the honest answer for a typo or a spec nvx does not understand, and the
// prompt is where -y's approval is a deliberate choice rather than a gap.
func TestAnUnresolvableSpecIsStillAnError(t *testing.T) {
	meta := NpmRegistryMetadata{
		DistTags: map[string]string{"latest": "5.0.0"},
		Versions: map[string]NpmVersionDetails{"4.17.21": {}, "5.0.0": {}},
	}
	for _, q := range []string{"^7.0.0", "not-a-version", "1.2.3 - 4.5.6", "github:user/repo"} {
		if got, err := resolveVersionQuery(q, meta); err == nil {
			t.Errorf("%q resolved to %q; it should be an error the caller turns into a prompt", q, got)
		} else if !strings.Contains(err.Error(), q) {
			t.Errorf("%q: the error should name the spec so the prompt does: %v", q, err)
		}
	}
}
