package nvx

import "testing"

// release_age.trusted_packages waives the cooling-off window, and the
// typosquat list no longer does.
//
// One list used to turn off both checks, so waiving the 24-hour window meant
// also giving up typosquat detection for that name -- two unrelated judgements
// that could not be made separately. The pair below is the whole point: each
// list waives its own check and neither reaches across.
func TestEachTrustedListWaivesItsOwnCheck(t *testing.T) {
	var p Policy
	p.ReleaseAge.TrustedPackages = []string{"chrome-devtools-mcp"}
	p.Typosquatting.TrustedPackages = []string{"my-internal-helper"}

	if !p.IsReleaseAgeTrusted("chrome-devtools-mcp") {
		t.Error("a package named in release_age.trusted_packages is still held to the window")
	}
	if p.IsTrustedPackage("chrome-devtools-mcp") {
		t.Error("waiving the release-age window also waived typosquat detection")
	}
	if !p.IsTrustedPackage("my-internal-helper") {
		t.Error("a package named in typosquatting.trusted_packages is still typosquat-checked")
	}
	if p.IsReleaseAgeTrusted("my-internal-helper") {
		t.Error("trusting a name against typosquats also waived the release-age window")
	}
}

// Both lists match the same way: case-insensitively, with globs, ignoring
// surrounding space.
//
// They share one matcher for this reason. Two copies would be free to drift in
// how they compare, and a difference between "the typosquat list accepts a glob
// and the release-age list does not" is the kind of thing nobody thinks to test
// until a policy file silently stops exempting something.
func TestTheTwoTrustedListsMatchTheSameWay(t *testing.T) {
	cases := []struct {
		entry, pkg string
		want       bool
	}{
		{"Chrome-DevTools-MCP", "chrome-devtools-mcp", true},
		{"  chrome-devtools-mcp  ", "chrome-devtools-mcp", true},
		{"@upstash/*", "@upstash/context7-mcp", true},
		{"@upstash/*", "@other/context7-mcp", false},
		{"chrome-devtools-mcp", "chrome-devtools-mcp-evil", false},
		{"", "anything", false},
	}
	for _, c := range cases {
		var byRelease, byTypo Policy
		byRelease.ReleaseAge.TrustedPackages = []string{c.entry}
		byTypo.Typosquatting.TrustedPackages = []string{c.entry}

		if got := byRelease.IsReleaseAgeTrusted(c.pkg); got != c.want {
			t.Errorf("release_age entry %q vs %q = %v, want %v", c.entry, c.pkg, got, c.want)
		}
		if got := byTypo.IsTrustedPackage(c.pkg); got != c.want {
			t.Errorf("typosquatting entry %q vs %q = %v, want %v", c.entry, c.pkg, got, c.want)
		}
	}
}

// A project file adding a name to the release-age list asks for approval.
//
// The list is an exemption from a supply-chain check, and a .nvx-policy.json
// lives in the repository -- so without this, one line in a pull request would
// waive the cooling-off window for a package of its choosing, on the day that
// window matters most.
func TestAddingAReleaseAgeExemptionIsALoosening(t *testing.T) {
	before := DefaultPolicy()
	after := DefaultPolicy()
	after.ReleaseAge.TrustedPackages = []string{"something-new"}

	if !policyLoosens(before, after) {
		t.Error("a project policy could waive the release-age window for a package with no approval")
	}
	if policyLoosens(after, before) {
		t.Error("removing an exemption counted as a loosening")
	}
}

// The merged list is the union, and a project file cannot remove a global entry.
func TestReleaseAgeExemptionsMerge(t *testing.T) {
	var global, local Policy
	global.ReleaseAge.TrustedPackages = []string{"mine-globally"}
	local.ReleaseAge.TrustedPackages = []string{"mine-locally", "MINE-GLOBALLY"}

	merged := MergePolicies(global, local)
	if !merged.IsReleaseAgeTrusted("mine-globally") {
		t.Error("a project file dropped a global exemption")
	}
	if !merged.IsReleaseAgeTrusted("mine-locally") {
		t.Error("a project file's own exemption was ignored")
	}
	if len(merged.ReleaseAge.TrustedPackages) != 2 {
		t.Errorf("the same entry in both files was kept twice: %v", merged.ReleaseAge.TrustedPackages)
	}
}
