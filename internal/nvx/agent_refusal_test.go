package nvx

import (
	"os"
	"strings"
	"testing"
	"time"
)

// agentParagraph is how every check refusal's last paragraph begins.
const agentParagraph = "If you are an automated agent: do not retry with -y or NVX_YES, and do not edit the policy yourself. Tell the person you work for"

// lastLine is the last non-empty line of out.
func lastLine(out string) string {
	lines := strings.Split(strings.TrimRight(out, "\r\n "), "\n")
	return lines[len(lines)-1]
}

// --agent-mode refuses what would ask, and says why. It approved every check:
// measured before the change, `nvx --agent-mode` installed a package inside the
// release-age window with "Approved without asking (--agent-mode)" on stderr and
// check_approved by=agent_mode in the audit log. Someone who set it to stop an
// agent hanging turned the checks into log lines.
func TestAgentModeRefusesInsteadOfApproving(t *testing.T) {
	t.Setenv("NVX_YES", "")
	t.Setenv("NVX_TRUST_YES", "")
	oldY, oldA, oldQ := yesFlag, agentModeFlag, quietFlag
	t.Cleanup(func() { yesFlag, agentModeFlag, quietFlag = oldY, oldA, oldQ })
	yesFlag, agentModeFlag, quietFlag = false, false, false

	// Read the flags as the startup code does, then set -y from what they returned.
	_, yes, _, _, _ := parseStartupFlags([]string{"nvx", "--agent-mode", "npm", "install", "some-pkg"})
	yesFlag = yes

	sc := checkScenario{pkg: "some-pkg", policy: `{"typosquatting":{"enabled":false}}`, age: 2 * time.Hour}
	code, out, home := runScenario(t, sc)
	if code == 0 {
		t.Fatalf("--agent-mode approved a release inside the cooling-off window:\n%s", out)
	}
	recs := readAuditRecords(t, home)
	if r := findRecord(recs, "check_refused", "release_age"); r == nil || r["by"] != "agent_mode" {
		t.Errorf("want the refusal recorded as check_refused by=agent_mode, got %v", recs)
	}
	if findRecord(recs, "check_approved", "release_age") != nil {
		t.Errorf("--agent-mode recorded an approval: %v", recs)
	}
	if !strings.Contains(out, "--agent-mode") {
		t.Errorf("the refusal does not say --agent-mode is why nvx did not ask:\n%s", out)
	}
	if !strings.Contains(lastLine(out), agentParagraph) {
		t.Errorf("the refusal does not end with the paragraph for an agent:\n%s", out)
	}
}

// Every check refusal ends with one paragraph for an agent. It says not to
// approve the check itself, to tell the person, and gives the line the person
// can add. Only the global-install refusal spoke to an agent. The others named -y, NVX_YES and
// ~/.nvx/policy.json as the way past them, which an agent outside the sandbox
// can use.
func TestEveryCheckRefusalEndsWithTheAgentParagraph(t *testing.T) {
	noTypo := `"typosquatting":{"enabled":false}`
	scenarios := checkScenarios()
	scenarios = append(scenarios,
		checkScenario{name: "blocked package", pkg: "some-pkg", age: 1000 * time.Hour,
			policy: `{"blocked_packages":["some-pkg"],` + noTypo + `}`},
		checkScenario{name: "enforce_ignore_scripts", pkg: "some-pkg", age: 1000 * time.Hour, scripts: true,
			policy: `{"enforce_ignore_scripts":true,` + noTypo + `}`,
			remedy: []string{`{"install_scripts":{"trusted_packages":["some-pkg"]}}`}},
		checkScenario{name: "malicious package", pkg: "some-pkg", age: 1000 * time.Hour,
			policy: `{` + noTypo + `}`,
			vulns:  map[string][]OSVVuln{"some-pkg@1.0.0": {{ID: "MAL-2025-18479"}}},
			remedy: []string{`{"vulnerabilities":{"allowed_advisories":["MAL-2025-18479"]}}`}},
	)
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			t.Setenv("NVX_NONINTERACTIVE", "1")
			t.Setenv("NVX_YES", "")
			code, out, _ := runScenario(t, sc)
			if code == 0 {
				t.Fatalf("not refused:\n%s", out)
			}
			last := lastLine(out)
			if !strings.Contains(last, agentParagraph) {
				t.Fatalf("the refusal does not end with the paragraph for an agent:\n%s", out)
			}
			// The narrowest policy line the refusal shows ends the paragraph too.
			for _, want := range sc.remedy {
				if strings.HasPrefix(want, "{") && !strings.HasSuffix(strings.TrimSpace(last), want) {
					t.Errorf("the paragraph for an agent does not end with the policy line %s:\n%s", want, last)
				}
			}
		})
	}
}

// A release inside the cooling-off window is offered an older version first,
// which an agent may pin itself, before the policy line only the person should
// add.
func TestReleaseAgeRefusalOffersAnOlderVersionFirst(t *testing.T) {
	t.Setenv("NVX_NONINTERACTIVE", "1")
	t.Setenv("NVX_YES", "")
	sc := checkScenario{pkg: "some-pkg", policy: `{"typosquatting":{"enabled":false}}`, age: 2 * time.Hour}
	_, out, _ := runScenario(t, sc)
	pin := strings.Index(out, "some-pkg@<version>")
	line := strings.Index(out, "release_age.trusted_packages")
	if pin < 0 {
		t.Fatalf("the refusal does not offer an older version:\n%s", out)
	}
	if line >= 0 && line < pin {
		t.Errorf("the policy line comes before the older version:\n%s", out)
	}
}

// -y after the command belongs to the command, and when a check would have used
// it, nvx says so and how to give it to nvx. It said nothing: `npm install
// esbuild -y` was refused again, the same way, and the first thing an agent
// tries after a refusal failed with no hint.
func TestATrailingApprovalFlagIsNamedWhenACheckStops(t *testing.T) {
	t.Setenv("NVX_NONINTERACTIVE", "1")
	t.Setenv("NVX_YES", "")
	oldY, oldA := yesFlag, agentModeFlag
	t.Cleanup(func() { yesFlag, agentModeFlag = oldY, oldA })
	yesFlag, agentModeFlag = false, false
	saved := os.Args
	t.Cleanup(func() { os.Args = saved })

	refused := checkScenario{pkg: "some-pkg", policy: `{"typosquatting":{"enabled":false}}`, age: 1000 * time.Hour, scripts: true}
	for _, c := range []struct{ flag, how string }{
		{"-y", "NVX_YES=true npm ..."},
		{"--yes", "nvx -y npm ..."},
		{"--agent-mode", "NVX_AGENT_MODE=1 npm ..."},
	} {
		os.Args = []string{"nvx", "npm", "install", "some-pkg", c.flag}
		_, out, _ := runScenario(t, refused)
		if !strings.Contains(out, c.flag+" was passed to npm, not to nvx") || !strings.Contains(out, c.how) {
			t.Errorf("%s after the command: the refusal does not say it went to npm, or how to give it to nvx (%s):\n%s", c.flag, c.how, out)
		}
	}

	// Through a Windows shim the command arrives as `nvx shim npm ...`.
	os.Args = []string{"nvx", "shim", "npm", "install", "some-pkg", "-y"}
	if _, out, _ := runScenario(t, refused); !strings.Contains(out, "-y was passed to npm") {
		t.Errorf("through a shim, the refusal does not name the trailing -y:\n%s", out)
	}

	// After "--" it is the program's argument, not a misplaced nvx flag.
	os.Args = []string{"nvx", "npm", "install", "some-pkg", "--", "-y"}
	if _, out, _ := runScenario(t, refused); strings.Contains(out, "was passed to npm") {
		t.Errorf("a -y after -- was reported:\n%s", out)
	}

	// Nothing is said when no check stopped. There npx -y is npx's own flag,
	// and the person meant it for npx.
	os.Args = []string{"nvx", "npx", "-y", "some-pkg"}
	passes := checkScenario{pkg: "some-pkg", policy: `{"typosquatting":{"enabled":false}}`, age: 1000 * time.Hour}
	if code, out, _ := runScenario(t, passes); code != 0 || strings.Contains(out, "was passed to") {
		t.Errorf("a run no check stopped talked about -y (exit %d):\n%s", code, out)
	}
}

// nvx doctor says when the environment answers nvx's questions, and what each
// variable turns off. They are set once, in a profile or an agent's settings,
// and nothing about a later run shows they are there.
func TestDoctorWarnsAboutApprovalVariables(t *testing.T) {
	home := t.TempDir()
	seedDoctorShims(t, home)
	restore := reportSandboxLaunchFn
	t.Cleanup(func() { reportSandboxLaunchFn = restore })
	reportSandboxLaunchFn = func(string) bool { return true }

	t.Setenv("NVX_YES", "")
	t.Setenv("NVX_AGENT_MODE", "")
	t.Setenv("NVX_TRUST_YES", "")
	quiet := captureStderrHere(t, func() { runDoctorQuietly(t, home) })
	for _, name := range []string{"NVX_YES", "NVX_AGENT_MODE", "NVX_TRUST_YES"} {
		if strings.Contains(quiet, name+" is set") {
			t.Errorf("doctor warned about %s with it unset:\n%s", name, quiet)
		}
	}

	t.Setenv("NVX_YES", "true")
	t.Setenv("NVX_AGENT_MODE", "1")
	t.Setenv("NVX_TRUST_YES", "1")
	out := captureStderrHere(t, func() { runDoctorQuietly(t, home) })
	for name, turnsOff := range map[string]string{
		"NVX_YES":        "pre-install checks",
		"NVX_AGENT_MODE": "questions",
		"NVX_TRUST_YES":  "widen the sandbox",
	} {
		i := strings.Index(out, name+" is set")
		if i < 0 {
			t.Errorf("doctor does not warn that %s is set:\n%s", name, out)
			continue
		}
		line := out[i:]
		if j := strings.Index(line, "\n"); j >= 0 {
			line = line[:j]
		}
		if !strings.Contains(line, "turns off") || !strings.Contains(line, turnsOff) {
			t.Errorf("the %s warning does not say what it turns off (%s):\n%s", name, turnsOff, line)
		}
	}
}
