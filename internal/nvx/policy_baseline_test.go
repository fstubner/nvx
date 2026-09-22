package nvx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePolicyFixture writes one policy file and returns its path.
func writePolicyFixture(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// inProjectDir runs the rest of the test with dir as the working directory.
//
// os.Chdir with a restore rather than t.Chdir, which needs Go 1.24 and this
// module targets 1.23.
func inProjectDir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

// A project file that loosens an enforced baseline is REFUSED, not ignored.
//
// This is the whole point of the setting. Before it existed, MergePolicies took
// the project's word for typosquatting.enabled, release_age.min_age_hours,
// vulnerabilities.min_severity and isolation.enabled, and the only thing standing
// in front of that was a prompt the developer could answer -- which is the wrong
// gate when the baseline was set by somebody else, because the person being asked
// is the person it exists to constrain.
//
// Refused rather than dropped: a setting that silently never applied is how
// somebody believes they have a protection they do not have. LoadPolicy returns
// the error, so the command does not run at all.
func TestAnEnforcedBaselineRefusesALooseningProjectPolicy(t *testing.T) {
	home := tempDir(t)
	writePolicyFixture(t, home, "policy.json", `{
	  "enforced": true,
	  "typosquatting": {"enabled": true, "max_distance": 2},
	  "release_age": {"enabled": true, "min_age_hours": 48}
	}`)

	project := tempDir(t)
	writePolicyFixture(t, project, ".nvx-policy.json", `{
	  "typosquatting": {"enabled": false},
	  "release_age": {"min_age_hours": 1}
	}`)
	inProjectDir(t, project)

	_, err := LoadPolicy(home)
	if err == nil {
		t.Fatal("a project file that switched off typosquatting and cut the release-age window " +
			"was accepted under an enforced baseline; the baseline enforces nothing")
	}
	msg := err.Error()
	// The message has to name the field and both values, or the developer is told
	// only that something was refused and has to bisect their own file.
	// The file is named by path; asserted on its name rather than the whole path,
	// because the working directory a temp dir reports back can differ from the one
	// it was created as (/var against /private/var, and worse on Windows).
	for _, want := range []string{"typosquatting.enabled", "release_age.min_age_hours", "48", "1", ".nvx-policy.json"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, msg)
		}
	}
}

// Without "enforced", nothing changes. This is the backward-compatibility half,
// and it is the half that would break quietly: every policy file already on disk
// omits the key.
//
// The same two files as the test above. The loosening is IGNORED, exactly as it
// was before enforcement existed -- no error, and the baseline's own values stand
// -- so the approve-once trust prompt is still what decides.
func TestWithoutEnforcedALooseningProjectPolicyBehavesAsBefore(t *testing.T) {
	home := tempDir(t)
	writePolicyFixture(t, home, "policy.json", `{
	  "typosquatting": {"enabled": true, "max_distance": 2},
	  "release_age": {"enabled": true, "min_age_hours": 48}
	}`)

	project := tempDir(t)
	writePolicyFixture(t, project, ".nvx-policy.json", `{
	  "typosquatting": {"enabled": false},
	  "release_age": {"min_age_hours": 1}
	}`)
	inProjectDir(t, project)

	policy, err := LoadPolicy(home)
	if err != nil {
		t.Fatalf("an unenforced global policy refused a project file: %v\n"+
			"enforcement is opt-in, and every policy file already written omits the key", err)
	}
	if !policy.Typosquatting.Enabled {
		t.Error("the untrusted project file switched off typosquatting; it should have been ignored")
	}
	if policy.ReleaseAgeMinHours() != 48 {
		t.Errorf("release_age.min_age_hours = %d, want the global 48", policy.ReleaseAgeMinHours())
	}
}

// Tightening is the point of allowing a project file at all under a baseline.
func TestAnEnforcedBaselineHonoursATighteningProjectPolicy(t *testing.T) {
	home := tempDir(t)
	writePolicyFixture(t, home, "policy.json", `{
	  "enforced": true,
	  "typosquatting": {"enabled": true, "max_distance": 2},
	  "release_age": {"enabled": true, "min_age_hours": 24}
	}`)

	project := tempDir(t)
	writePolicyFixture(t, project, ".nvx-policy.json", `{
	  "typosquatting": {"max_distance": 3},
	  "release_age": {"min_age_hours": 72},
	  "blocked_packages": ["left-pad"]
	}`)
	inProjectDir(t, project)

	policy, err := LoadPolicy(home)
	if err != nil {
		t.Fatalf("a project file that only tightened settings was refused: %v", err)
	}
	if policy.Typosquatting.MaxDistance != 3 {
		t.Errorf("typosquatting.max_distance = %d, want the project's stricter 3", policy.Typosquatting.MaxDistance)
	}
	if policy.ReleaseAgeMinHours() != 72 {
		t.Errorf("release_age.min_age_hours = %d, want the project's longer 72", policy.ReleaseAgeMinHours())
	}
	if !policy.IsBlocked("left-pad") {
		t.Error("the project's addition to blocked_packages did not apply; adding a block is a tightening")
	}
}

// Every list that MergePolicies unions is a loosening when a project file adds to
// it, and under a baseline each addition is refused by name.
func TestAnEnforcedBaselineRefusesEveryKindOfAddition(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		field string
	}{
		{"a trusted package", `{"typosquatting":{"trusted_packages":["evil"]}}`, "typosquatting.trusted_packages"},
		{"an accepted advisory", `{"vulnerabilities":{"allowed_advisories":["GHSA-xxxx"]}}`, "vulnerabilities.allowed_advisories"},
		{"an install-script exemption", `{"install_scripts":{"trusted_packages":["evil"]}}`, "install_scripts.trusted_packages"},
		{"an egress host", `{"isolation":{"network":{"allow_hosts":["evil.example:443"]}}}`, "isolation.network.allow_hosts"},
		{"a raised severity floor", `{"vulnerabilities":{"min_severity":"critical"}}`, "vulnerabilities.min_severity"},
		{"switching the sandbox off", `{"isolation":{"enabled":false}}`, "isolation.enabled"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := tempDir(t)
			writePolicyFixture(t, home, "policy.json", `{"enforced": true}`)
			project := tempDir(t)
			writePolicyFixture(t, project, ".nvx-policy.json", tc.body)
			inProjectDir(t, project)

			_, err := LoadPolicy(home)
			if err == nil {
				t.Fatalf("%s was accepted under an enforced baseline", tc.name)
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Errorf("the refusal does not name %s:\n%s", tc.field, err.Error())
			}
		})
	}
}

// A project file cannot take an entry OFF the blocklist, enforced or not.
//
// The spec for enforcement asks for removals from blocked_packages to be refused.
// They cannot be expressed: MergePolicies unions the two lists, so a project file
// that omits a blocked package adds nothing and removes nothing. This pins that,
// because the day the union becomes a replacement the refusal above stops being
// unnecessary and nothing else would notice.
func TestAProjectFileCannotRemoveABlockedPackage(t *testing.T) {
	home := tempDir(t)
	writePolicyFixture(t, home, "policy.json", `{"enforced": true, "blocked_packages": ["left-pad"]}`)
	project := tempDir(t)
	writePolicyFixture(t, project, ".nvx-policy.json", `{"blocked_packages": ["other-thing"]}`)
	inProjectDir(t, project)

	policy, err := LoadPolicy(home)
	if err != nil {
		t.Fatalf("adding to the blocklist was refused: %v", err)
	}
	if !policy.IsBlocked("left-pad") {
		t.Fatal("a project file that did not list left-pad removed it from the blocklist")
	}
	if !policy.IsBlocked("other-thing") {
		t.Fatal("the project's own addition to the blocklist did not apply")
	}
}

// "enforced" in a project file would let the file being constrained appoint
// itself the constraint.
func TestEnforcedInAProjectFileDoesNotMakeItABaseline(t *testing.T) {
	home := tempDir(t)
	writePolicyFixture(t, home, "policy.json", `{"typosquatting": {"enabled": true}}`)
	project := tempDir(t)
	writePolicyFixture(t, project, ".nvx-policy.json", `{"enforced": true}`)
	inProjectDir(t, project)

	policy, err := LoadPolicy(home)
	if err != nil {
		t.Fatalf("LoadPolicy: %v", err)
	}
	if policy.Enforced {
		t.Fatal("a project file declared itself an enforced baseline; enforcement is the global policy's to set")
	}
}

// policyLoosens and the baseline's refusals are one list of rules, so a new rule
// added to either reaches both.
func TestLooseningsAndTheBooleanAgree(t *testing.T) {
	before := DefaultPolicy()
	normalizePolicy(&before)
	after := before
	after.Isolation.Enabled = false

	loosenings := policyLoosenings(before, after)
	if len(loosenings) == 0 {
		t.Fatal("switching the sandbox off produced no loosening")
	}
	if !policyLoosens(before, after) {
		t.Fatal("policyLoosens disagreed with policyLoosenings")
	}
	if loosenings[0].Field != "isolation.enabled" {
		t.Fatalf("field = %q, want isolation.enabled", loosenings[0].Field)
	}
	if policyLoosens(before, before) {
		t.Fatal("a policy loosened itself")
	}
}
