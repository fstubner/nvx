package nvx

import "testing"

// A leading "~" was read as a home-directory path, so a tilde range such as
// `left-pad@~1.3.0`, or `~1.3.0` declared in package.json, was classified as a
// local path. A local path gets only the blocklist, so those packages skipped
// the advisory, release-age and typosquat checks. Only "~/" (or "~\" on
// Windows) names a path.
func TestATildeRangeIsNotALocalPath(t *testing.T) {
	for _, spec := range []string{"left-pad@~1.3.0", "~1.3.0", "~1", "@scope/pkg@~2.0.0", "alias@npm:left-pad@~1.3.0"} {
		if kind := nonRegistrySpecKind(spec); kind != "" {
			t.Errorf("%q classified as %s; a tilde range comes from the registry", spec, kind)
		}
	}
	for _, spec := range []string{"~/projects/lib", `~\projects\lib`, "lib@~/projects/lib"} {
		if kind := nonRegistrySpecKind(spec); kind != "a local path" {
			t.Errorf("%q classified as %q, want a local path", spec, kind)
		}
	}
}
