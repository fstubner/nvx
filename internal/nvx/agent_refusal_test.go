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

// Agent mode wins over -y, --yes and NVX_YES, however each is given. Agents
// pass -y by habit, and with -y beside --agent-mode a release inside the
// cooling-off window was approved and recorded as check_approved by=yes_flag.
// The refusal says, in one line, which of them it ignored.
func TestAgentModeIgnoresYesAndNvxYes(t *testing.T) {
	sc := checkScenario{pkg: "some-pkg", policy: `{"typosquatting":{"enabled":false}}`, age: 2 * time.Hour}
	for _, c := range []struct {
		name    string
		flags   []string
		env     map[string]string
		ignored string
	}{
		{"--agent-mode -y", []string{"--agent-mode", "-y"}, nil, "-y was ignored"},
		{"-y --agent-mode", []string{"-y", "--agent-mode"}, nil, "-y was ignored"},
		{"--agent-mode --yes", []string{"--agent-mode", "--yes"}, nil, "-y was ignored"},
		{"--agent-mode and NVX_YES", []string{"--agent-mode"}, map[string]string{"NVX_YES": "true"}, "NVX_YES was ignored"},
		{"NVX_AGENT_MODE and -y", []string{"-y"}, map[string]string{"NVX_AGENT_MODE": "1"}, "-y was ignored"},
		{"NVX_AGENT_MODE and NVX_YES", nil, map[string]string{"NVX_AGENT_MODE": "1", "NVX_YES": "1"}, "NVX_YES was ignored"},
		{"all of them", []string{"--agent-mode", "-y"}, map[string]string{"NVX_YES": "true"}, "-y and NVX_YES were ignored"},
	} {
		t.Run(c.name, func(t *testing.T) {
			for _, k := range []string{"NVX_YES", "NVX_AGENT_MODE", "NVX_TRUST_YES", "NVX_QUIET", "NVX_VERBOSE"} {
				t.Setenv(k, "")
			}
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			oldY, oldA, oldQ, oldV := yesFlag, agentModeFlag, quietFlag, verboseFlag
			t.Cleanup(func() { yesFlag, agentModeFlag, quietFlag, verboseFlag = oldY, oldA, oldQ, oldV })
			yesFlag, agentModeFlag, quietFlag, verboseFlag = false, false, false, false

			// As the startup code reads them: the leading flags, then the environment.
			args := append(append([]string{"nvx"}, c.flags...), "npm", "install", sc.pkg)
			_, yes, _, _, _ := parseStartupFlags(args)
			yesFlag = yes
			applyEnvironmentFlags()

			code, out, home := runScenario(t, sc)
			if code == 0 {
				t.Fatalf("a release inside the cooling-off window was approved in agent mode:\n%s", out)
			}
			recs := readAuditRecords(t, home)
			if r := findRecord(recs, "check_refused", "release_age"); r == nil || r["by"] != "agent_mode" {
				t.Errorf("want the refusal recorded as check_refused by=agent_mode, got %v", recs)
			}
			if findRecord(recs, "check_approved", "release_age") != nil {
				t.Errorf("agent mode recorded an approval: %v", recs)
			}
			if n := strings.Count(out, "ignored, because agent mode is on"); n != 1 || !strings.Contains(out, c.ignored) {
				t.Errorf("want one line saying %q, got %d such lines:\n%s", c.ignored, n, out)
			}
		})
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

	warning := func(out, name string) string {
		i := strings.Index(out, name+" is set")
		if i < 0 {
			t.Errorf("doctor does not warn that %s is set:\n%s", name, out)
			return ""
		}
		line := out[i:]
		if j := strings.Index(line, "\n"); j >= 0 {
			line = line[:j]
		}
		return line
	}

	t.Setenv("NVX_YES", "true")
	t.Setenv("NVX_TRUST_YES", "1")
	out := captureStderrHere(t, func() { runDoctorQuietly(t, home) })
	for name, turnsOff := range map[string]string{
		"NVX_YES":       "pre-install checks",
		"NVX_TRUST_YES": "widen the sandbox",
	} {
		if line := warning(out, name); !strings.Contains(line, "turns off") || !strings.Contains(line, turnsOff) {
			t.Errorf("the %s warning does not say what it turns off (%s):\n%s", name, turnsOff, line)
		}
	}

	// Agent mode ignores NVX_YES, and doctor says that rather than what
	// NVX_YES would turn off.
	t.Setenv("NVX_AGENT_MODE", "1")
	out = captureStderrHere(t, func() { runDoctorQuietly(t, home) })
	if line := warning(out, "NVX_AGENT_MODE"); !strings.Contains(line, "turns off") || !strings.Contains(line, "questions") {
		t.Errorf("the NVX_AGENT_MODE warning does not say what it turns off (questions):\n%s", line)
	}
	if line := warning(out, "NVX_YES"); !strings.Contains(line, "ignores NVX_YES") || strings.Contains(line, "turns off") {
		t.Errorf("with NVX_AGENT_MODE set, the NVX_YES warning does not say agent mode ignores it:\n%s", line)
	}
}
