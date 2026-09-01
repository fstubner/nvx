package main

import "testing"

// Weakening a numeric protection is a loosening, and needs the trust prompt like
// any other.
//
// policyLoosens checked booleans and list membership but not the two settings
// whose STRENGTH is a number. MergePolicies takes any positive local value for
// both, so a checked-in project file could halve typosquat sensitivity or cut the
// release-age cooling-off window from a day to an hour, and nothing asked.
func TestWeakeningANumericProtectionCountsAsLoosening(t *testing.T) {
	base := func() Policy {
		p := DefaultPolicy()
		normalizePolicy(&p)
		return p
	}

	t.Run("lowering the typosquat edit distance", func(t *testing.T) {
		before := base()
		after := base()
		after.Typosquatting.MaxDistance = 1 // default is 2: finds fewer typosquats
		if !policyLoosens(before, after) {
			t.Fatal("halving typosquat sensitivity was not treated as a loosening; " +
				"a project file could do it with no prompt")
		}
	})

	t.Run("raising it is not a loosening", func(t *testing.T) {
		before := base()
		after := base()
		after.Typosquatting.MaxDistance = 4 // stricter
		if policyLoosens(before, after) {
			t.Fatal("raising typosquat sensitivity was treated as a loosening; " +
				"asking permission to be stricter trains people to say yes")
		}
	})

	t.Run("shortening the release-age window", func(t *testing.T) {
		before := base()
		after := base()
		after.ReleaseAge.MinAgeHours = 1 // default is 24
		if !policyLoosens(before, after) {
			t.Fatal("cutting the cooling-off window from 24h to 1h was not treated as a loosening")
		}
	})

	t.Run("lengthening it is not a loosening", func(t *testing.T) {
		before := base()
		after := base()
		after.ReleaseAge.MinAgeHours = 72
		if policyLoosens(before, after) {
			t.Fatal("a longer cooling-off window was treated as a loosening")
		}
	})
}

// Trusted packages are compared as a set rather than by length.
//
// Length is correct today only because MergePolicies unions these lists, which is
// a rule stated in another function. This pins the behaviour directly so the two
// cannot drift apart silently.
func TestAddingATrustedPackageIsALooseningEvenAtTheSameLength(t *testing.T) {
	before := DefaultPolicy()
	before.Typosquatting.TrustedPackages = []string{"react"}
	normalizePolicy(&before)

	after := DefaultPolicy()
	after.Typosquatting.TrustedPackages = []string{"evil-package"}
	normalizePolicy(&after)

	if !policyLoosens(before, after) {
		t.Fatal("swapping the trusted list for a different one of the same length was not a loosening; " +
			"a length comparison cannot see this, and trusting a package skips the typosquat " +
			"and release-age checks for it")
	}
}

// The two fields an audit flagged as missing are absent on purpose. If either
// ever starts replacing instead of unioning, this test is where that shows up.
func TestFieldsMergePoliciesMakesUnreachableAreNotTreatedAsLoosening(t *testing.T) {
	// BlockedPackages is unioned, so a local policy can only ADD to the blocklist.
	global := DefaultPolicy()
	global.BlockedPackages = []string{"known-bad"}
	local := DefaultPolicy()
	local.BlockedPackages = nil // a project trying to drop the entry

	merged := MergePolicies(global, local)
	found := false
	for _, p := range merged.BlockedPackages {
		if p == "known-bad" {
			found = true
		}
	}
	if !found {
		t.Fatal("a project policy removed a blocked package; MergePolicies is no longer union-only, " +
			"so policyLoosens must start checking BlockedPackages")
	}
}
