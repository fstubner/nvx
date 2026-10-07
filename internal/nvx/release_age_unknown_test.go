package nvx

import (
	"strings"
	"testing"
	"time"
)

// A version the registry gives no publish time for is asked about like one
// inside the release-age window, and the release_age settings waive it as they
// waive that. It passed unasked until 2026-10-07, because publishAgeShouldWarn
// returns false for a zero time, and ResolveNpmPackageDetails returns one when
// the registry leaves the version out of `time`.
func TestUnknownReleaseAgeIsAskedAbout(t *testing.T) {
	cases := []struct {
		name, pkg, policy string
		yes               bool
		pass              bool
	}{
		{name: "nobody to ask", pkg: "some-pkg", policy: `{"typosquatting":{"enabled":false}}`},
		{name: "trusted by scope", pkg: "@corp/lib", pass: true,
			policy: `{"typosquatting":{"enabled":false},"release_age":{"trusted_packages":["@corp/*"]}}`},
		{name: "release_age off", pkg: "some-pkg", pass: true,
			policy: `{"typosquatting":{"enabled":false},"release_age":{"enabled":false}}`},
		{name: "-y", pkg: "some-pkg", yes: true, pass: true, policy: `{"typosquatting":{"enabled":false}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("NVX_NONINTERACTIVE", "1")
			t.Setenv("NVX_YES", "")
			oldY, oldA := yesFlag, agentModeFlag
			yesFlag, agentModeFlag = c.yes, false
			t.Cleanup(func() { yesFlag, agentModeFlag = oldY, oldA })

			code, out, home := runScenario(t, checkScenario{pkg: c.pkg, policy: c.policy, noPublishTime: true})
			recs := readAuditRecords(t, home)
			if !c.pass {
				if code == 0 {
					t.Fatalf("a version with no publish time went through unasked:\n%s", out)
				}
				if r := findRecord(recs, "check_refused", "release_age"); r == nil || r["detail"] != "publish time unknown" {
					t.Errorf("want a release_age refusal for an unknown publish time, got %v", recs)
				}
				return
			}
			if code != 0 {
				t.Fatalf("refused (exit %d):\n%s", code, out)
			}
			if c.yes {
				if r := findRecord(recs, "check_approved", "release_age"); r == nil || r["by"] != "yes_flag" {
					t.Errorf("want the -y approval recorded, got %v", recs)
				}
			} else if strings.Contains(out, "publish time") {
				t.Errorf("an exempt package was asked about:\n%s", out)
			}
		})
	}
}

// `nvx policy check --online` reports a dependency with no publish time as a
// release-age finding. It passed it.
func TestPolicyCheckReportsAnUnknownReleaseAge(t *testing.T) {
	origScan := scanVulnerabilitiesBatchForVerify
	origResolve := resolveNpmPackageDetailsForVerify
	scanVulnerabilitiesBatchForVerify = func([]OSVQuery) (map[string][]OSVVuln, error) { return nil, nil }
	resolveNpmPackageDetailsForVerify = func(name, query string) (string, time.Time, bool, error) {
		return "1.3.0", time.Time{}, false, nil
	}
	t.Cleanup(func() {
		scanVulnerabilitiesBatchForVerify = origScan
		resolveNpmPackageDetailsForVerify = origResolve
	})

	home := tempDir(t)
	project := tempDir(t)
	writePolicyFixture(t, project, "package.json", `{"dependencies": {"left-pad": "^1.3.0"}}`)
	writePolicyFixture(t, project, "package-lock.json", `{"packages": {"node_modules/left-pad": {"version": "1.3.0"}}}`)
	inProjectDir(t, project)

	result := evaluatePolicyCheck(home, project, true)
	found := false
	for _, f := range result.Findings {
		found = found || (f.Class == "release_age" && strings.Contains(f.Message, "no publish time"))
	}
	if !found || result.ExitCode == exitPolicyPass {
		t.Errorf("findings = %+v, exit %d; want a release-age finding for the unknown publish time", result.Findings, result.ExitCode)
	}
}
