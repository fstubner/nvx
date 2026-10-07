package nvx

import (
	"strings"
	"testing"
	"time"
)

// A package OSV lists as malicious (an OpenSSF MAL- record) is refused, and
// only an allowed_advisories entry naming that advisory lets it through. -y,
// --agent-mode and NVX_YES approved one until 2026-10-07. Measured that day in
// a container, NVX_AGENT_MODE=1 nvx npm install discord.dll installed it despite
// MAL-2025-18479, and exited 0.
func TestMaliciousPackagesAreRefusedWhateverApprovesPrompts(t *testing.T) {
	mal := map[string][]OSVVuln{"some-pkg@1.0.0": {{ID: "MAL-2025-18479", Summary: "Malicious code in some-pkg (npm)"}}}
	lowMal := map[string][]OSVVuln{"some-pkg@1.0.0": {{ID: "MAL-2025-18479", Severity: "LOW"}}}
	const noTypo = `"typosquatting":{"enabled":false}`
	cases := []struct {
		name   string
		policy string
		vulns  map[string][]OSVVuln
		set    func(t *testing.T)
		pass   bool
	}{
		{name: "-y", policy: `{` + noTypo + `}`, vulns: mal, set: func(t *testing.T) {
			old := yesFlag
			yesFlag = true
			t.Cleanup(func() { yesFlag = old })
		}},
		{name: "--agent-mode", policy: `{` + noTypo + `}`, vulns: mal, set: func(t *testing.T) {
			oldY, oldA := yesFlag, agentModeFlag
			yesFlag, agentModeFlag = true, true
			t.Cleanup(func() { yesFlag, agentModeFlag = oldY, oldA })
		}},
		{name: "NVX_YES", policy: `{` + noTypo + `}`, vulns: mal, set: func(t *testing.T) { t.Setenv("NVX_YES", "true") }},
		{name: "NVX_TRUST_YES", policy: `{` + noTypo + `}`, vulns: mal, set: func(t *testing.T) { t.Setenv("NVX_TRUST_YES", "true") }},
		{name: "a pattern", policy: `{` + noTypo + `,"vulnerabilities":{"allowed_advisories":["MAL-*"]}}`, vulns: mal},
		{name: "min_severity, rated low", policy: `{` + noTypo + `,"vulnerabilities":{"min_severity":"critical"}}`, vulns: lowMal},
		{name: "min_severity, unrated, with -y", policy: `{` + noTypo + `,"vulnerabilities":{"min_severity":"low"}}`, vulns: mal,
			set: func(t *testing.T) { t.Setenv("NVX_YES", "true") }},
		{name: "named exactly", policy: `{` + noTypo + `,"vulnerabilities":{"allowed_advisories":["mal-2025-18479"]}}`, vulns: mal, pass: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("NVX_NONINTERACTIVE", "1")
			t.Setenv("NVX_YES", "")
			t.Setenv("NVX_TRUST_YES", "")
			if c.set != nil {
				c.set(t)
			}
			code, out, home := runScenario(t, checkScenario{pkg: "some-pkg", policy: c.policy, age: 1000 * time.Hour, vulns: c.vulns})
			recs := readAuditRecords(t, home)
			if c.pass {
				if code != 0 {
					t.Fatalf("an advisory the policy names was refused (exit %d):\n%s", code, out)
				}
				if findRecord(recs, "vulnerability_allowed", "") == nil && !strings.Contains(out, "MAL-2025-18479") {
					t.Errorf("the allowed advisory was not reported:\n%s", out)
				}
				return
			}
			if code == 0 {
				t.Fatalf("a malicious package was installed:\n%s", out)
			}
			r := findRecord(recs, "check_refused", "malicious_package")
			if r == nil || r["by"] != "policy" || r["detail"] != "MAL-2025-18479" {
				t.Errorf("want a malicious_package refusal by policy, got %v", recs)
			}
			if findRecord(recs, "check_approved", "vulnerability") != nil {
				t.Errorf("the advisory was recorded as approved: %v", recs)
			}
			if !strings.Contains(out, `"allowed_advisories":["MAL-2025-18479"]`) {
				t.Errorf("the refusal does not name the one entry that allows it:\n%s", out)
			}
		})
	}
}

// min_severity does not lower a malicious advisory below the line in `nvx
// policy check` either, which reads the same rule.
func TestMinSeverityNeverLetsAMaliciousAdvisoryThrough(t *testing.T) {
	p := Policy{Vulnerabilities: VulnerabilityPolicy{MinSeverity: "critical"}}
	if !p.BlocksInstall(OSVVuln{ID: "MAL-2025-18479", Severity: "LOW"}) {
		t.Error("a malicious advisory rated low passed a critical floor")
	}
	if p.BlocksInstall(OSVVuln{ID: "GHSA-test-0001", Severity: "LOW"}) {
		t.Error("an ordinary low advisory stopped the install under a critical floor")
	}
	p.Vulnerabilities.AllowedAdvisories = []string{"*"}
	if !p.BlocksInstall(OSVVuln{ID: "MAL-2025-18479"}) {
		t.Error(`allowed_advisories ["*"] let a malicious advisory through`)
	}
}
