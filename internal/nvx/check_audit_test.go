package nvx

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// checkScenario sets up one pre-install check to fire, and nothing else.
type checkScenario struct {
	name    string
	pkg     string
	policy  string
	version string
	age     time.Duration // since publication
	// noPublishTime is a registry that gives no publish time for the version.
	noPublishTime bool
	scripts       bool
	regErr        error
	vulns         map[string][]OSVVuln
	osvErr        error
	// command, when set, runs verifyBeforeRun for this package manager command
	// (pm first, then its arguments) so flags on the command line are seen.
	command []string
	contain bool
	// what the audit record and the refusal text must say
	check string
	by    string // for a refusal with nobody to ask
	// narrow strings the refusal text must contain, in order of appearance
	remedy []string
}

func checkScenarios() []checkScenario {
	return []checkScenario{
		{
			name: "typosquat", pkg: "reakt", check: "typosquat", by: "non_interactive",
			policy: `{}`, age: 1000 * time.Hour,
			remedy: []string{"typosquatting.trusted_packages", `{"typosquatting":{"trusted_packages":["reakt"]}}`},
		},
		{
			name: "install scripts", pkg: "some-pkg", check: "install_scripts", by: "non_interactive",
			policy: `{"typosquatting":{"enabled":false}}`, age: 1000 * time.Hour, scripts: true,
			remedy: []string{"install_scripts.trusted_packages", `{"install_scripts":{"trusted_packages":["some-pkg"]}}`},
		},
		{
			name: "release age", pkg: "some-pkg", check: "release_age", by: "non_interactive",
			policy: `{"typosquatting":{"enabled":false}}`, age: 2 * time.Hour,
			remedy: []string{"release_age.trusted_packages", `{"release_age":{"trusted_packages":["some-pkg"]}}`},
		},
		{
			name: "release age unknown", pkg: "some-pkg", check: "release_age", by: "non_interactive",
			policy: `{"typosquatting":{"enabled":false}}`, noPublishTime: true,
			remedy: []string{"release_age.trusted_packages", `{"release_age":{"trusted_packages":["some-pkg"]}}`, "release_age.enabled"},
		},
		{
			name: "vulnerability", pkg: "some-pkg", check: "vulnerability", by: "non_interactive",
			policy: `{"typosquatting":{"enabled":false}}`, age: 1000 * time.Hour,
			vulns:  map[string][]OSVVuln{"some-pkg@1.0.0": {{ID: "GHSA-test-0001", Summary: "bad", Severity: "HIGH"}}},
			remedy: []string{"vulnerabilities.allowed_advisories", `{"vulnerabilities":{"allowed_advisories":["GHSA-test-0001"]}}`, "vulnerabilities.min_severity"},
		},
		{
			name: "registry unreachable", pkg: "some-pkg", check: "registry_unreachable", by: "non_interactive",
			policy: `{"typosquatting":{"enabled":false}}`, regErr: fmt.Errorf("503 Service Unavailable"),
			remedy: []string{"No policy setting waives"},
		},
		{
			name: "osv unreachable", pkg: "some-pkg", check: "osv_unreachable", by: "non_interactive",
			policy: `{"typosquatting":{"enabled":false}}`, age: 1000 * time.Hour, osvErr: fmt.Errorf("timeout"),
			remedy: []string{"No policy setting waives"},
		},
	}
}

// runScenario runs runVerifyInstall for a scenario in a fresh home and returns
// its exit code, what it printed and the home.
func runScenario(t *testing.T, sc checkScenario, args ...string) (int, string, string) {
	t.Helper()
	home := tempDir(t)
	if err := os.WriteFile(filepath.Join(home, "policy.json"), []byte(sc.policy), 0o600); err != nil {
		t.Fatal(err)
	}
	// A fresh cache is used as-is, so the check never reaches for the network.
	if err := os.WriteFile(filepath.Join(home, "popular_packages.json"), []byte(`["react","lodash"]`), 0o600); err != nil {
		t.Fatal(err)
	}

	origResolve := resolveNpmPackageDetailsForVerify
	resolveNpmPackageDetailsForVerify = func(pkgName, versionQuery string) (string, time.Time, bool, error) {
		if sc.regErr != nil {
			return "", time.Time{}, false, sc.regErr
		}
		if sc.noPublishTime {
			return "1.0.0", time.Time{}, sc.scripts, nil
		}
		return "1.0.0", time.Now().Add(-sc.age), sc.scripts, nil
	}
	origScan := scanVulnerabilitiesBatchForVerify
	scanVulnerabilitiesBatchForVerify = func(q []OSVQuery) (map[string][]OSVVuln, error) {
		return sc.vulns, sc.osvErr
	}
	origDownloads := weeklyDownloads
	weeklyDownloads = func(name string) (int, error) {
		if name == "react" {
			return 20_000_000, nil
		}
		return 10, nil
	}
	t.Cleanup(func() {
		resolveNpmPackageDetailsForVerify = origResolve
		scanVulnerabilitiesBatchForVerify = origScan
		weeklyDownloads = origDownloads
	})

	if len(args) == 0 {
		args = []string{sc.pkg}
	}
	var code int
	if len(sc.command) > 0 {
		out := captureStderrHere(t, func() {
			code, _, _ = verifyBeforeRun(verifyRequest{pmCmd: sc.command[0], pmArgs: sc.command[1:], nvxHome: home, contain: sc.contain})
		})
		return code, out, home
	}
	out := captureStderrHere(t, func() { code, _ = runVerifyInstall(args, home) })
	return code, out, home
}

func readAuditRecords(t *testing.T, home string) []map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "audit.log"))
	if err != nil {
		return nil
	}
	var out []map[string]string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		rec := map[string]string{}
		for k, v := range raw {
			rec[k] = fmt.Sprint(v)
		}
		out = append(out, rec)
	}
	return out
}

func findRecord(recs []map[string]string, event, check string) map[string]string {
	for _, r := range recs {
		if r["event"] == event && r["check"] == check {
			return r
		}
	}
	return nil
}

// A check that -y, --agent-mode or NVX_YES waved through leaves a record and one
// line on stderr, whatever NVX_TRACE says.
//
// Measured before the fix: `NVX_AGENT_MODE=1 nvx npm install reakt lodash@4.17.15`
// installed both, said nothing about the typosquat, and `nvx audit` read "No
// audit records yet".
func TestAutoApprovedChecksAreRecordedAndAnnounced(t *testing.T) {
	sources := []struct {
		name, by, label string
		set             func(t *testing.T)
	}{
		{"-y", "yes_flag", "-y", func(t *testing.T) {
			old := yesFlag
			yesFlag = true
			t.Cleanup(func() { yesFlag = old })
		}},
		{"--agent-mode", "agent_mode", "--agent-mode", func(t *testing.T) {
			oldY, oldA, oldQ := yesFlag, agentModeFlag, quietFlag
			yesFlag, agentModeFlag, quietFlag = true, true, true
			t.Cleanup(func() { yesFlag, agentModeFlag, quietFlag = oldY, oldA, oldQ })
		}},
		{"NVX_YES", "nvx_yes", "NVX_YES", func(t *testing.T) { t.Setenv("NVX_YES", "true") }},
	}
	for _, src := range sources {
		t.Run(src.name, func(t *testing.T) {
			t.Setenv("NVX_TRACE", "")
			src.set(t)
			// Every check fires for one package: typosquat of react, fresh,
			// with install scripts, and one advisory.
			sc := checkScenario{
				pkg: "reakt", policy: `{}`, age: 2 * time.Hour, scripts: true,
				vulns: map[string][]OSVVuln{"reakt@1.0.0": {{ID: "GHSA-test-0001", Severity: "HIGH"}}},
			}
			code, out, home := runScenario(t, sc)
			if code != 0 {
				t.Fatalf("the approved install was refused (exit %d):\n%s", code, out)
			}
			recs := readAuditRecords(t, home)
			for _, check := range []string{"typosquat", "install_scripts", "release_age", "vulnerability"} {
				r := findRecord(recs, "check_approved", check)
				if r == nil {
					t.Errorf("no check_approved record for %s; records: %v", check, recs)
					continue
				}
				if r["by"] != src.by {
					t.Errorf("%s was recorded as approved by %q, want %q", check, r["by"], src.by)
				}
			}
			if r := findRecord(recs, "check_approved", "typosquat"); r != nil && r["package"] != "reakt" {
				t.Errorf("the typosquat approval names package %q, want reakt", r["package"])
			}
			if got := strings.Count(out, "Approved without asking ("+src.label+")"); got < 4 {
				t.Errorf("want a stderr line for each of the 4 approved checks, got %d:\n%s", got, out)
			}
			if !strings.Contains(out, "reakt looks like a typosquat of react") {
				t.Errorf("the stderr line does not say what was approved:\n%s", out)
			}
		})
	}
}

// Every check that stops an install writes a record saying so.
func TestRefusedChecksAreRecorded(t *testing.T) {
	for _, sc := range checkScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			t.Setenv("NVX_NONINTERACTIVE", "1")
			t.Setenv("NVX_YES", "")
			code, out, home := runScenario(t, sc)
			if code == 0 {
				t.Fatalf("the check did not refuse:\n%s", out)
			}
			r := findRecord(readAuditRecords(t, home), "check_refused", sc.check)
			if r == nil {
				t.Fatalf("no check_refused record for %s; log: %v", sc.check, readAuditRecords(t, home))
			}
			if r["by"] != sc.by {
				t.Errorf("recorded as answered by %q, want %q", r["by"], sc.by)
			}
		})
	}
}

// Refusals with no prompt behind them are recorded too: a blocklist entry, and
// install scripts under enforce_ignore_scripts.
func TestPolicyRefusalsAreRecorded(t *testing.T) {
	cases := []struct {
		name, policy, check string
		scripts             bool
	}{
		{"blocked package", `{"blocked_packages":["some-pkg"],"typosquatting":{"enabled":false}}`, "blocked_package", false},
		{"enforce_ignore_scripts", `{"enforce_ignore_scripts":true,"typosquatting":{"enabled":false}}`, "enforce_ignore_scripts", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sc := checkScenario{pkg: "some-pkg", policy: c.policy, age: 1000 * time.Hour, scripts: c.scripts}
			code, out, home := runScenario(t, sc)
			if code == 0 {
				t.Fatalf("not refused:\n%s", out)
			}
			r := findRecord(readAuditRecords(t, home), "check_refused", c.check)
			if r == nil || r["by"] != "policy" {
				t.Errorf("want a check_refused record for %s answered by policy, got %v", c.check, readAuditRecords(t, home))
			}
		})
	}
}

// A refusal names the narrowest remedy first, as the exact policy line, and the
// blanket switches come after it.
func TestRefusalTextNamesTheNarrowRemedyBeforeTheBlanketSwitch(t *testing.T) {
	for _, sc := range checkScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			t.Setenv("NVX_NONINTERACTIVE", "1")
			t.Setenv("NVX_YES", "")
			_, out, _ := runScenario(t, sc)
			prompt := ""
			for _, line := range strings.Split(out, "\n") {
				if strings.Contains(line, "denying prompt") {
					prompt = line
				}
			}
			if prompt == "" {
				t.Fatalf("no denial line in:\n%s", out)
			}
			last := -1
			for _, want := range sc.remedy {
				i := strings.Index(prompt, want)
				if i < 0 {
					t.Fatalf("the refusal does not contain %q:\n%s", want, prompt)
				}
				last = i
			}
			if blanket := strings.Index(prompt, "NVX_YES"); blanket >= 0 && blanket < last {
				t.Errorf("NVX_YES is named before the narrow remedy:\n%s", prompt)
			}
		})
	}
}

// A command that turns install scripts off has nothing for the install-script
// check to ask about. Measured 2026-10-04 before the fix: `npm install esbuild
// --ignore-scripts` aborted non-interactively with exit 77, and
// `npm ci --ignore-scripts` stopped at the prompt.
func TestIgnoreScriptsSkipsTheInstallScriptCheck(t *testing.T) {
	enforce := `{"enforce_ignore_scripts":true,"typosquatting":{"enabled":false}}`
	prompt := `{"typosquatting":{"enabled":false}}`
	projectWith := func(t *testing.T, npmrc string) {
		t.Helper()
		dir := tempDir(t)
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"p"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if npmrc != "" {
			if err := os.WriteFile(filepath.Join(dir, ".npmrc"), []byte(npmrc), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		prev, _ := os.Getwd()
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chdir(prev) })
	}
	cases := []struct {
		name    string
		policy  string
		command []string
		contain bool
		env     string // npm_config_ignore_scripts
		npmrc   string
		pass    bool
		by      string // for a pass, the check_skipped record's by
	}{
		{name: "flag, pnpm", policy: prompt, command: []string{"pnpm", "add", "some-pkg", "--ignore-scripts"}, pass: true, by: "ignore_scripts_flag"},
		{name: "flag, yarn", policy: prompt, command: []string{"yarn", "add", "some-pkg", "--ignore-scripts"}, pass: true, by: "ignore_scripts_flag"},
		{name: "flag, bun", policy: prompt, command: []string{"bun", "add", "some-pkg", "--ignore-scripts"}, pass: true, by: "ignore_scripts_flag"},
		{name: "flag=true", policy: prompt, command: []string{"pnpm", "add", "some-pkg", "--ignore-scripts=true"}, pass: true, by: "ignore_scripts_flag"},
		{name: "flag, contained", policy: prompt, command: []string{"pnpm", "add", "some-pkg", "--ignore-scripts"}, contain: true, pass: true, by: "ignore_scripts_flag"},
		{name: "flag satisfies enforce_ignore_scripts", policy: enforce, command: []string{"pnpm", "add", "some-pkg", "--ignore-scripts"}, pass: true, by: "ignore_scripts_flag"},
		{name: "environment", policy: prompt, command: []string{"pnpm", "add", "some-pkg"}, env: "true", pass: true, by: "ignore_scripts_env"},
		{name: "project .npmrc", policy: prompt, command: []string{"pnpm", "add", "some-pkg"}, npmrc: "ignore-scripts=true\n", pass: true, by: "ignore_scripts_npmrc"},
		{name: "project .npmrc satisfies enforce_ignore_scripts", policy: enforce, command: []string{"pnpm", "add", "some-pkg"}, npmrc: "ignore-scripts=true\n", pass: true, by: "ignore_scripts_npmrc"},

		{name: "no flag still asks", policy: prompt, command: []string{"pnpm", "add", "some-pkg"}},
		{name: "no flag still refuses under enforce_ignore_scripts", policy: enforce, command: []string{"pnpm", "add", "some-pkg"}},
		{name: "flag=false still asks", policy: prompt, command: []string{"pnpm", "add", "some-pkg", "--ignore-scripts=false"}},
		{name: "flag=false still refuses under enforce_ignore_scripts", policy: enforce, command: []string{"pnpm", "add", "some-pkg", "--ignore-scripts=false"}},
		{name: "flag after -- is not the package manager's", policy: prompt, command: []string{"pnpm", "add", "some-pkg", "--", "--ignore-scripts"}},
		{name: "flag=false beats the project .npmrc", policy: prompt, command: []string{"pnpm", "add", "some-pkg", "--ignore-scripts=false"}, npmrc: "ignore-scripts=true\n"},
		{name: "environment false beats the project .npmrc", policy: prompt, command: []string{"pnpm", "add", "some-pkg"}, env: "false", npmrc: "ignore-scripts=true\n"},
		{name: "ignore-scripts=false in .npmrc", policy: prompt, command: []string{"pnpm", "add", "some-pkg"}, npmrc: "ignore-scripts=false\n"},
		// A contained npm is given no npm_config_* variables, so it would run the scripts.
		{name: "environment is not seen by a contained run", policy: prompt, command: []string{"pnpm", "add", "some-pkg"}, contain: true, env: "true"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("NVX_NONINTERACTIVE", "1")
			t.Setenv("NVX_YES", "")
			t.Setenv("npm_config_ignore_scripts", c.env)
			projectWith(t, c.npmrc)
			sc := checkScenario{pkg: "some-pkg", policy: c.policy, age: 1000 * time.Hour, scripts: true,
				command: c.command, contain: c.contain}
			code, out, home := runScenario(t, sc)
			recs := readAuditRecords(t, home)
			if !c.pass {
				if code == 0 {
					t.Fatalf("the install-script check did not stop it:\n%s", out)
				}
				return
			}
			if code != 0 {
				t.Fatalf("a command that ignores scripts was stopped (exit %d):\n%s", code, out)
			}
			if strings.Contains(out, "install scripts") || strings.Contains(out, "installation scripts") {
				t.Errorf("the run printed about install scripts:\n%s", out)
			}
			r := findRecord(recs, "check_skipped", "install_scripts")
			if r == nil || r["by"] != c.by {
				t.Errorf("want one check_skipped record for install_scripts with by=%s, got %v", c.by, recs)
			}
			for _, r := range recs {
				if r["event"] == "check_refused" || r["event"] == "check_approved" {
					t.Errorf("unexpected record: %v", r)
				}
			}
		})
	}
}

// Skipping the install-script check leaves every other check running, and is
// recorded once however many packages carry scripts.
func TestIgnoreScriptsLeavesTheOtherChecksRunning(t *testing.T) {
	t.Setenv("NVX_NONINTERACTIVE", "1")
	t.Setenv("NVX_YES", "")
	t.Setenv("npm_config_ignore_scripts", "")
	sc := checkScenario{pkg: "reakt", policy: `{}`, age: 1000 * time.Hour, scripts: true,
		command: []string{"pnpm", "add", "reakt", "lodash", "--ignore-scripts"}}
	code, out, home := runScenario(t, sc)
	if code == 0 {
		t.Fatalf("the typosquat check was skipped along with the script check:\n%s", out)
	}
	if findRecord(readAuditRecords(t, home), "check_refused", "typosquat") == nil {
		t.Errorf("no typosquat refusal recorded: %v", readAuditRecords(t, home))
	}

	sc = checkScenario{pkg: "some-pkg", policy: `{"typosquatting":{"enabled":false}}`, age: 1000 * time.Hour, scripts: true,
		command: []string{"pnpm", "add", "a-pkg", "b-pkg", "--ignore-scripts"}}
	if code, out, home = runScenario(t, sc); code != 0 {
		t.Fatalf("stopped:\n%s", out)
	}
	n := 0
	for _, r := range readAuditRecords(t, home) {
		if r["event"] == "check_skipped" && r["check"] == "install_scripts" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("want the skip recorded once for two packages, got %d", n)
	}
}

// The refusal under enforce_ignore_scripts names the flag as a way through, and
// the policy line for the package.
func TestEnforceIgnoreScriptsRefusalNamesTheFlag(t *testing.T) {
	t.Setenv("NVX_NONINTERACTIVE", "1")
	sc := checkScenario{pkg: "some-pkg", policy: `{"enforce_ignore_scripts":true,"typosquatting":{"enabled":false}}`,
		age: 1000 * time.Hour, scripts: true}
	code, out, _ := runScenario(t, sc)
	if code == 0 {
		t.Fatalf("not refused:\n%s", out)
	}
	for _, want := range []string{"pass --ignore-scripts", "install_scripts.trusted_packages", `{"install_scripts":{"trusted_packages":["some-pkg"]}}`, "enforce_ignore_scripts to false"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, out)
		}
	}
}

// The remedy sent to an MCP client names the policy line for each prompt-based
// refusal, and NVX_YES only after it.
func TestMCPRemedyNamesThePolicyKeyFirst(t *testing.T) {
	cases := map[string]string{
		"a package looked like a typosquat and the warning was not approved":          "typosquatting.trusted_packages",
		"a package runs install scripts and the warning was not approved":             "install_scripts.trusted_packages",
		"a package version was published inside the release-age cooling-off window":   "release_age.trusted_packages",
		"a package has a known active vulnerability and the warning was not approved": "vulnerabilities.allowed_advisories",
		"the security policy disallows package install scripts":                       "install_scripts.trusted_packages",
	}
	for reason, key := range cases {
		got := remedyFor(reason)
		i := strings.Index(got, key)
		if i < 0 {
			t.Errorf("reason %q: remedy does not name %s: %s", reason, key, got)
			continue
		}
		if j := strings.Index(got, "NVX_YES"); j >= 0 && j < i {
			t.Errorf("reason %q: NVX_YES comes before %s: %s", reason, key, got)
		}
	}
}

// A refused egress host names the allow_hosts line, and does so under -q, which
// hides LogInfo.
func TestEgressRefusalNamesAllowHosts(t *testing.T) {
	t.Setenv("NVX_TRUST_YES", "")
	old := quietFlag
	quietFlag = true
	t.Cleanup(func() { quietFlag = old })

	p := newPromptingProxy(t, nil)
	public := []net.IP{net.ParseIP("104.16.0.1")}
	out := captureStderrHere(t, func() {
		if p.allowed(parseHostPortSpec("example.com", 443), public) {
			t.Fatal("the host was allowed with nobody to ask")
		}
	})
	want := `{"isolation":{"network":{"allow_hosts":["example.com:443"]}}}`
	i := strings.Index(out, want)
	if i < 0 {
		t.Fatalf("the refusal does not give the allow_hosts line:\n%s", out)
	}
	if j := strings.Index(out, "NVX_TRUST_YES"); j >= 0 && j < i {
		t.Errorf("NVX_TRUST_YES is named before the allow_hosts line:\n%s", out)
	}

	p2 := newTestProxy(t, "proxy", nil)
	out = captureStderrHere(t, func() { p2.allowed(parseHostPortSpec("example.org", 443), public) })
	if !strings.Contains(out, `["example.org:443"]`) {
		t.Errorf("the plain denial under -q does not give the allow_hosts line:\n%s", out)
	}
}

// A failed download lookup is said to have failed, and the result is called a
// guess.
func TestTyposquatLookupFailureIsNamedInThePrompt(t *testing.T) {
	t.Setenv("NVX_NONINTERACTIVE", "1")
	t.Setenv("NVX_YES", "")
	origDownloads := weeklyDownloads
	t.Cleanup(func() { weeklyDownloads = origDownloads })

	// Set up by hand rather than through runScenario, which installs its own
	// download counts.
	h := tempDir(t)
	os.WriteFile(filepath.Join(h, "policy.json"), []byte(`{}`), 0o600)
	os.WriteFile(filepath.Join(h, "popular_packages.json"), []byte(`["react"]`), 0o600)
	weeklyDownloads = func(string) (int, error) { return 0, fmt.Errorf("HTTP 429 Too Many Requests") }
	origResolve := resolveNpmPackageDetailsForVerify
	resolveNpmPackageDetailsForVerify = func(string, string) (string, time.Time, bool, error) {
		return "1.0.0", time.Now().Add(-1000 * time.Hour), false, nil
	}
	t.Cleanup(func() { resolveNpmPackageDetailsForVerify = origResolve })

	out := captureStderrHere(t, func() { runVerifyInstall([]string{"reakt"}, h) })
	for _, want := range []string{"lookup", "failed", "429", "name-similarity guess"} {
		if !strings.Contains(out, want) {
			t.Errorf("the prompt does not say %q:\n%s", want, out)
		}
	}
	if r := findRecord(readAuditRecords(t, h), "check_refused", "typosquat"); r == nil || !strings.Contains(r["detail"], "lookup failed") {
		t.Errorf("the audit record does not note the failed lookup: %v", readAuditRecords(t, h))
	}
}

// A name nvx's own list holds is not called a squat because a request failed.
// The fetched list replaces the embedded one, so `redis` can sit beside a
// fetched `ioredis` without being in the list it is compared against.
func TestLookupFailureDoesNotFlagAPopularListName(t *testing.T) {
	origDownloads := weeklyDownloads
	weeklyDownloads = func(string) (int, error) { return 0, fmt.Errorf("HTTP 429 Too Many Requests") }
	t.Cleanup(func() { weeklyDownloads = origDownloads })

	var got string
	out := captureStderrHere(t, func() { got = CheckTyposquattingAuthority("redis", []string{"ioredis"}, 2) })
	if got != "" {
		t.Errorf("redis is on the embedded popular list and was flagged as a squat of %q", got)
	}
	if !strings.Contains(out, "Could not look up weekly downloads") {
		t.Errorf("the failed lookup was not warned about:\n%s", out)
	}

	// A name on no list keeps the strict default.
	if got := CheckTyposquattingAuthority("ioredss", []string{"ioredis"}, 2); got != "ioredis" {
		t.Errorf("a name on no list must still be flagged when the lookup fails, got %q", got)
	}
}
