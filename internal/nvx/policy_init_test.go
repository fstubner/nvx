package nvx

import "testing"

// `nvx policy init` in a project must not change the policy in force.
//
// It wrote the whole default policy into .nvx-policy.json. Every value in it
// counted as the project's choice, so under an enforced global policy with a
// 48 hour release-age window the file read as a loosening, and every command
// in the project was then refused. Its default_allow list also switched off
// the runtime providers' own default hosts.
func TestPolicyInitInAProjectLeavesTheGlobalPolicyInForce(t *testing.T) {
	home := tempDir(t)
	writePolicyFixture(t, home, "policy.json", `{
	  "enforced": true,
	  "isolation": {"level": "strict"},
	  "release_age": {"enabled": true, "min_age_hours": 48}
	}`)
	inProjectDir(t, tempDir(t))

	if code := runPolicyInit(nil, home); code != 0 {
		t.Fatalf("policy init exited %d", code)
	}
	policy, err := LoadPolicy(home)
	if err != nil {
		t.Fatalf("the scaffolded project policy made the policy unloadable: %v", err)
	}
	if policy.Isolation.Level != "strict" || policy.ReleaseAgeMinHours() != 48 {
		t.Fatalf("level=%q min_age_hours=%d, want the global strict and 48", policy.Isolation.Level, policy.ReleaseAgeMinHours())
	}
	if policy.Isolation.Network.DefaultAllowSet {
		t.Fatal("the scaffold pins default_allow, which drops the runtime providers' default hosts")
	}
}
