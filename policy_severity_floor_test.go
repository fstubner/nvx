package main

import "testing"

// With no floor set, every advisory stops the install.
//
// The default this protects is the one that existed before a floor could be set,
// and it is the whole reason the feature is safe to add: a policy that says
// nothing about severity gets the strict behaviour, not a lenient one.
func TestWithoutAFloorEveryAdvisoryBlocks(t *testing.T) {
	p := DefaultPolicy()
	normalizePolicy(&p)

	for _, sev := range []string{"CRITICAL", "HIGH", "MODERATE", "LOW", ""} {
		if !p.BlocksInstall(OSVVuln{ID: "GHSA-x", Severity: sev}) {
			t.Errorf("severity %q did not stop the install with no floor set", sev)
		}
	}
}

// A floor lets the advisories below it through and keeps the rest.
func TestAFloorAppliesFromItsLevelUpward(t *testing.T) {
	var p Policy
	p.Vulnerabilities.MinSeverity = "high"

	for _, sev := range []string{"CRITICAL", "HIGH"} {
		if !p.BlocksInstall(OSVVuln{ID: "GHSA-x", Severity: sev}) {
			t.Errorf("%s is at or above the high floor and must still stop the install", sev)
		}
	}
	for _, sev := range []string{"MODERATE", "LOW"} {
		if p.BlocksInstall(OSVVuln{ID: "GHSA-x", Severity: sev}) {
			t.Errorf("%s is below the high floor and should not stop the install", sev)
		}
	}
}

// An advisory nvx could not rate stops the install, whatever the floor says.
//
// This is the one that decides whether the feature is safe. Severity comes from
// a network lookup that is bounded and best-effort, so "" means "not measured",
// and treating that as low would turn a failed request, a rate limit or an
// advisory past the lookup cap into a finding that silently passed the floor.
func TestAnUnratedAdvisoryBlocksAtEveryFloor(t *testing.T) {
	for _, floor := range []string{"low", "moderate", "high", "critical"} {
		var p Policy
		p.Vulnerabilities.MinSeverity = floor
		if !p.BlocksInstall(OSVVuln{ID: "GHSA-unrated", Severity: ""}) {
			t.Errorf("an advisory with no severity passed the %s floor", floor)
		}
		if !p.BlocksInstall(OSVVuln{ID: "GHSA-odd", Severity: "banana"}) {
			t.Errorf("an advisory with an unrecognised severity passed the %s floor", floor)
		}
	}
}

// A typo in min_severity leaves every advisory blocking.
//
// The strict direction, deliberately: a floor nvx cannot read is no floor. It is
// also reported at load time, because a security setting that was ignored in
// silence is the failure this codebase keeps finding.
func TestAnUnrecognisedFloorIsNoFloor(t *testing.T) {
	var p Policy
	p.Vulnerabilities.MinSeverity = "hihg"
	if _, ok := p.SeverityFloor(); ok {
		t.Error("a misspelt severity was accepted as a floor")
	}
	if !p.BlocksInstall(OSVVuln{ID: "GHSA-x", Severity: "LOW"}) {
		t.Error("a misspelt floor let a low advisory through")
	}
}

// The allowlist works independently of the floor.
func TestAnAllowlistedAdvisoryPassesWhateverItsSeverity(t *testing.T) {
	var p Policy
	p.Vulnerabilities.AllowedAdvisories = []string{"GHSA-assessed"}

	if p.BlocksInstall(OSVVuln{ID: "GHSA-assessed", Severity: "CRITICAL"}) {
		t.Error("an assessed advisory was blocked by its severity")
	}
	if !p.BlocksInstall(OSVVuln{ID: "GHSA-other", Severity: "CRITICAL"}) {
		t.Error("accepting one advisory let another through")
	}
}

// "medium" means "moderate".
//
// Most other tools say medium, and a policy that says it should not fail open on
// a word nvx merely spells differently.
func TestMediumIsAcceptedForModerate(t *testing.T) {
	var p Policy
	p.Vulnerabilities.MinSeverity = "medium"
	if p.BlocksInstall(OSVVuln{ID: "GHSA-x", Severity: "LOW"}) {
		t.Error("low should be below a medium floor")
	}
	if !p.BlocksInstall(OSVVuln{ID: "GHSA-x", Severity: "MODERATE"}) {
		t.Error("moderate should be at a medium floor")
	}
}

// Raising the floor needs approval; lowering it does not.
func TestRaisingTheFloorIsALoosening(t *testing.T) {
	floorOf := func(v string) Policy {
		p := DefaultPolicy()
		p.Vulnerabilities.MinSeverity = v
		return p
	}
	if !policyLoosens(floorOf(""), floorOf("high")) {
		t.Error("a project policy could set a severity floor with no approval")
	}
	if !policyLoosens(floorOf("moderate"), floorOf("critical")) {
		t.Error("raising the floor did not count as a loosening")
	}
	if policyLoosens(floorOf("critical"), floorOf("low")) {
		t.Error("lowering the floor counted as a loosening")
	}
	if policyLoosens(floorOf("high"), floorOf("")) {
		t.Error("removing the floor counted as a loosening")
	}
}

// The split routes each advisory by the same rule, so what is reported as
// allowed and what stops the install cannot disagree.
func TestTheSplitFollowsTheFloor(t *testing.T) {
	var p Policy
	p.Vulnerabilities.MinSeverity = "high"

	remaining, accepted := splitAllowedAdvisories(p, map[string][]OSVVuln{
		"pkg@1.0.0": {
			{ID: "GHSA-crit", Severity: "CRITICAL"},
			{ID: "GHSA-mod", Severity: "MODERATE"},
			{ID: "GHSA-unrated"},
		},
	})
	if len(remaining["pkg@1.0.0"]) != 2 {
		t.Fatalf("the critical and the unrated advisory must both stop the install: %v", remaining)
	}
	if len(accepted["pkg@1.0.0"]) != 1 || accepted["pkg@1.0.0"][0].ID != "GHSA-mod" {
		t.Fatalf("only the moderate advisory is below the floor: %v", accepted)
	}
}
