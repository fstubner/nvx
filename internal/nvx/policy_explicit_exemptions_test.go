package nvx

import "testing"

// Nothing is exempt by default.
//
// The reason these lists exist is to name an exception, so a default that
// exempted anything would be the feature undoing the checks it is meant to make
// usable. Worth pinning because it is a one-word change to a default in
// DefaultPolicy and nothing else would notice.
func TestNoExemptionsByDefault(t *testing.T) {
	p := DefaultPolicy()
	normalizePolicy(&p)

	if p.InstallScriptsTrusted("esbuild") {
		t.Error("a package's install scripts run unasked with no policy saying so")
	}
	if p.IsAllowedAdvisory("GHSA-anything") {
		t.Error("an advisory is accepted with no policy saying so")
	}
	if p.IsReleaseAgeTrusted("anything") {
		t.Error("the release-age window is waived with no policy saying so")
	}
	if p.IsTrustedPackage("anything") {
		t.Error("typosquat detection is waived with no policy saying so")
	}
}

// Each of the four exemption lists waives its own check and reaches no further.
//
// This is the property the whole shape exists for. One list doing double duty is
// what made the narrow intent inexpressible before, and the failure mode is
// silent: a policy that meant to skip a cooling-off window would also stop
// checking a name against popular packages, with nothing on screen to say so.
func TestEveryExemptionListIsIndependent(t *testing.T) {
	var p Policy
	p.Typosquatting.TrustedPackages = []string{"typo-only"}
	p.ReleaseAge.TrustedPackages = []string{"age-only"}
	p.InstallScripts.TrustedPackages = []string{"scripts-only"}
	p.Vulnerabilities.AllowedAdvisories = []string{"GHSA-only"}

	checks := map[string]func(string) bool{
		"typo-only":    p.IsTrustedPackage,
		"age-only":     p.IsReleaseAgeTrusted,
		"scripts-only": p.InstallScriptsTrusted,
		"GHSA-only":    p.IsAllowedAdvisory,
	}
	for named, itsOwnCheck := range checks {
		if !itsOwnCheck(named) {
			t.Errorf("%q is named in its list and not exempt by it", named)
		}
		for other, otherCheck := range checks {
			if other == named {
				continue
			}
			if otherCheck(named) {
				t.Errorf("%q was named in one list and is exempt from another check too", named)
			}
		}
	}
}

// Every list matches the same way, because they share one matcher.
func TestExemptionListsMatchConsistently(t *testing.T) {
	var p Policy
	p.InstallScripts.TrustedPackages = []string{"@my-scope/*", "  EsBuild  "}
	p.Vulnerabilities.AllowedAdvisories = []string{"ghsa-abcd-*"}

	if !p.InstallScriptsTrusted("@my-scope/tool") {
		t.Error("a glob in the install-scripts list did not match")
	}
	if !p.InstallScriptsTrusted("esbuild") {
		t.Error("a padded, differently-cased entry did not match")
	}
	if p.InstallScriptsTrusted("@other/tool") {
		t.Error("a glob matched outside its scope")
	}
	if !p.IsAllowedAdvisory("GHSA-ABCD-1234") {
		t.Error("an advisory glob did not match case-insensitively")
	}
	if p.IsAllowedAdvisory("GHSA-WXYZ-1234") {
		t.Error("an advisory outside the glob was accepted")
	}
}

// Adding to either new list needs approval when a project file does it.
//
// Install scripts are the sharper of the two: the entry runs arbitrary code at
// install time without asking, so a pull request that adds one would otherwise
// hand itself execution on every machine that installs the project.
func TestAddingEitherExemptionIsALoosening(t *testing.T) {
	base := DefaultPolicy()

	withScripts := DefaultPolicy()
	withScripts.InstallScripts.TrustedPackages = []string{"something"}
	if !policyLoosens(base, withScripts) {
		t.Error("a project policy could run a package's install scripts unasked with no approval")
	}

	withAdvisory := DefaultPolicy()
	withAdvisory.Vulnerabilities.AllowedAdvisories = []string{"GHSA-1234"}
	if !policyLoosens(base, withAdvisory) {
		t.Error("a project policy could accept a known vulnerability with no approval")
	}

	if policyLoosens(withScripts, base) || policyLoosens(withAdvisory, base) {
		t.Error("removing an exemption counted as a loosening")
	}
}

// Both lists union on merge, and a project file cannot drop a global entry.
func TestNewExemptionListsMerge(t *testing.T) {
	var global, local Policy
	global.InstallScripts.TrustedPackages = []string{"global-pkg"}
	global.Vulnerabilities.AllowedAdvisories = []string{"GHSA-global"}
	local.InstallScripts.TrustedPackages = []string{"local-pkg", "GLOBAL-PKG"}
	local.Vulnerabilities.AllowedAdvisories = []string{"GHSA-local"}

	merged := MergePolicies(global, local)
	for _, want := range []string{"global-pkg", "local-pkg"} {
		if !merged.InstallScriptsTrusted(want) {
			t.Errorf("%s was lost when the policies merged", want)
		}
	}
	if len(merged.InstallScripts.TrustedPackages) != 2 {
		t.Errorf("an entry present in both files was kept twice: %v", merged.InstallScripts.TrustedPackages)
	}
	for _, want := range []string{"GHSA-global", "GHSA-local"} {
		if !merged.IsAllowedAdvisory(want) {
			t.Errorf("%s was lost when the policies merged", want)
		}
	}
}

// An accepted advisory is filtered out; anything else still reaches the prompt.
func TestAcceptedAdvisoriesAreSeparatedFromTheRest(t *testing.T) {
	var p Policy
	p.Vulnerabilities.AllowedAdvisories = []string{"GHSA-known"}

	found := map[string][]OSVVuln{
		"left-pad@1.0.0": {{ID: "GHSA-known"}, {ID: "GHSA-new"}},
		"ms@2.1.3":       {{ID: "GHSA-known"}},
	}
	remaining, accepted := splitAllowedAdvisories(p, found)

	if len(remaining) != 1 || len(remaining["left-pad@1.0.0"]) != 1 || remaining["left-pad@1.0.0"][0].ID != "GHSA-new" {
		t.Fatalf("the unaccepted advisory did not survive the filter: %v", remaining)
	}
	if len(accepted["left-pad@1.0.0"]) != 1 || len(accepted["ms@2.1.3"]) != 1 {
		t.Fatalf("the accepted advisory was not recorded per package: %v", accepted)
	}
}

// A package is exempted per advisory, never wholesale.
//
// Exempting the package would waive every advisory it will ever have, including
// ones published after the assessment that justified the entry. That is the
// difference between "we looked at this finding" and "stop looking at this
// package", and only the first is on offer.
func TestAnExemptionDoesNotWaiveAPackagesOtherAdvisories(t *testing.T) {
	var p Policy
	p.Vulnerabilities.AllowedAdvisories = []string{"GHSA-assessed"}

	remaining, _ := splitAllowedAdvisories(p, map[string][]OSVVuln{
		"pkg@1.0.0": {{ID: "GHSA-assessed"}, {ID: "GHSA-published-later"}},
	})
	if len(remaining["pkg@1.0.0"]) != 1 {
		t.Fatalf("accepting one advisory silenced the package's others: %v", remaining)
	}
}
