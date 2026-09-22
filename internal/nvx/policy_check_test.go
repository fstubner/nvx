package nvx

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// findingClasses lists the classes a result carries, for readable assertions.
func findingClasses(result policyCheckResult) []string {
	var out []string
	for _, f := range result.Findings {
		out = append(out, f.Class)
	}
	return out
}

// A CI job has to be able to tell a policy violation from a network blip, and
// before this every nvx command exited 1 for both.
func TestPolicyCheckExitsWithTheBlockedPackageCode(t *testing.T) {
	home := tempDir(t)
	writePolicyFixture(t, home, "policy.json", `{"blocked_packages": ["left-pad"]}`)
	project := tempDir(t)
	writePolicyFixture(t, project, "package.json", `{"dependencies": {"left-pad": "^1.3.0", "react": "^18.0.0"}}`)
	inProjectDir(t, project)

	result := evaluatePolicyCheck(home, project, false)
	if result.ExitCode != exitBlockedPackage {
		t.Fatalf("exit code = %d (%v), want %d for a blocked package", result.ExitCode, findingClasses(result), exitBlockedPackage)
	}
	if len(result.Findings) != 1 || !strings.Contains(result.Findings[0].Message, "left-pad") {
		t.Fatalf("findings = %+v, want one naming left-pad", result.Findings)
	}
}

// A clean project passes, and says what it did not check.
//
// The second half matters as much as the first: a green result that skipped the
// vulnerability scan is a different claim from a green result that ran it, and
// nobody reading a pipeline log should have to work out which from the flags.
func TestPolicyCheckPassesACleanProjectAndNamesWhatItSkipped(t *testing.T) {
	home := tempDir(t)
	writePolicyFixture(t, home, "policy.json", `{"blocked_packages": ["left-pad"]}`)
	project := tempDir(t)
	writePolicyFixture(t, project, "package.json", `{"dependencies": {"react": "^18.0.0"}}`)
	inProjectDir(t, project)

	result := evaluatePolicyCheck(home, project, false)
	if result.ExitCode != exitPolicyPass || !result.OK {
		t.Fatalf("exit code = %d, findings = %+v, want a pass", result.ExitCode, result.Findings)
	}
	if len(result.Skipped) == 0 || !strings.Contains(strings.Join(result.Skipped, " "), "--online") {
		t.Fatalf("skipped = %v, want the network checks named as not run", result.Skipped)
	}
}

// The offline default makes no network request. A check that fails because OSV is
// having a bad afternoon is indistinguishable, in a pipeline, from a real
// violation.
func TestPolicyCheckMakesNoNetworkRequestByDefault(t *testing.T) {
	scanCalled := false
	resolveCalled := false
	origScan := scanVulnerabilitiesBatchForVerify
	origResolve := resolveNpmPackageDetailsForVerify
	scanVulnerabilitiesBatchForVerify = func([]OSVQuery) (map[string][]OSVVuln, error) {
		scanCalled = true
		return nil, nil
	}
	resolveNpmPackageDetailsForVerify = func(string, string) (string, time.Time, bool, error) {
		resolveCalled = true
		return "", time.Time{}, false, nil
	}
	t.Cleanup(func() {
		scanVulnerabilitiesBatchForVerify = origScan
		resolveNpmPackageDetailsForVerify = origResolve
	})

	home := tempDir(t)
	project := tempDir(t)
	writePolicyFixture(t, project, "package.json", `{"dependencies": {"react": "^18.0.0"}}`)
	inProjectDir(t, project)

	evaluatePolicyCheck(home, project, false)
	if scanCalled || resolveCalled {
		t.Fatalf("the offline default reached the network (osv=%v, registry=%v)", scanCalled, resolveCalled)
	}
}

// --online runs the two checks that need the network, and each has its own exit
// code.
func TestPolicyCheckReportsVulnerabilitiesAndReleaseAgeSeparately(t *testing.T) {
	origScan := scanVulnerabilitiesBatchForVerify
	origResolve := resolveNpmPackageDetailsForVerify
	scanVulnerabilitiesBatchForVerify = func(queries []OSVQuery) (map[string][]OSVVuln, error) {
		return map[string][]OSVVuln{
			"left-pad@1.3.0": {{ID: "GHSA-test", Severity: "HIGH"}},
		}, nil
	}
	resolveNpmPackageDetailsForVerify = func(name, query string) (string, time.Time, bool, error) {
		return "1.3.0", time.Now().Add(-time.Hour), false, nil
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
	classes := strings.Join(findingClasses(result), " ")
	if !strings.Contains(classes, "vulnerability") {
		t.Errorf("findings = %+v, want a vulnerability", result.Findings)
	}
	if !strings.Contains(classes, "release_age") {
		t.Errorf("findings = %+v, want a release-age violation", result.Findings)
	}
	// Vulnerability outranks release age, and both outrank nothing else here.
	if result.ExitCode != exitVulnerability {
		t.Errorf("exit code = %d, want %d: a vulnerability outranks a release-age violation", result.ExitCode, exitVulnerability)
	}
}

// A policy file that cannot be parsed has produced no verdict, and must not be
// reported as a pass.
func TestPolicyCheckReportsAnUnparseablePolicyFile(t *testing.T) {
	home := tempDir(t)
	project := tempDir(t)
	writePolicyFixture(t, project, ".nvx-policy.json", `{"isolation": {`)
	inProjectDir(t, project)

	result := evaluatePolicyCheck(home, project, false)
	if result.ExitCode != exitPolicyFileInvalid {
		t.Fatalf("exit code = %d (%+v), want %d for a policy file that will not parse",
			result.ExitCode, result.Findings, exitPolicyFileInvalid)
	}
}

// A project policy file that loosens settings and has never been trusted is not
// in force, so what a developer reads in their own repository is not what nvx
// enforces. That is worth failing a pipeline over, and it is worth doing without
// asking: a prompt in CI is a hang.
func TestPolicyCheckReportsAnUntrustedProjectPolicyWithoutPrompting(t *testing.T) {
	home := tempDir(t)
	project := tempDir(t)
	writePolicyFixture(t, project, ".nvx-policy.json", `{"isolation": {"enabled": false}}`)
	inProjectDir(t, project)

	done := make(chan policyCheckResult, 1)
	go func() { done <- evaluatePolicyCheck(home, project, false) }()
	select {
	case result := <-done:
		if result.ExitCode != exitPolicyViolation {
			t.Fatalf("exit code = %d (%+v), want %d", result.ExitCode, result.Findings, exitPolicyViolation)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the check did not finish; something on this path is waiting for an answer, " +
			"and in CI that is a hang rather than a failure")
	}
}

// A project with no manifest is not a failure, and must not silently count as a
// pass of checks that never ran.
func TestPolicyCheckSaysSoWhenThereIsNothingToCheck(t *testing.T) {
	home := tempDir(t)
	project := tempDir(t)
	inProjectDir(t, project)

	result := evaluatePolicyCheck(home, project, false)
	if result.ExitCode != exitPolicyPass {
		t.Fatalf("exit code = %d, want a pass for a directory with no dependencies", result.ExitCode)
	}
	if !strings.Contains(strings.Join(result.Skipped, " "), "package.json") {
		t.Fatalf("skipped = %v, want the dependency checks named as not run", result.Skipped)
	}
}

// The exit codes are a published contract, and the documentation is how anyone
// learns them. Two hand-maintained lists drift; this is the same argument the
// README/help parity test makes, applied to the numbers a pipeline branches on.
func TestExitCodesMatchTheirDocumentation(t *testing.T) {
	data, err := os.ReadFile("../../docs/exit-codes.md")
	if err != nil {
		t.Fatal(err)
	}
	row := regexp.MustCompile("(?m)^\\| *([0-9]+) *\\| *`([a-z_]+)` *\\|")
	documented := map[string]int{}
	for _, m := range row.FindAllStringSubmatch(string(data), -1) {
		code, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("exit-codes.md: %q is not a number", m[1])
		}
		documented[m[2]] = code
	}
	if len(documented) == 0 {
		t.Fatal("no exit codes found in docs/exit-codes.md; the table's shape changed and this test " +
			"would pass without reading anything")
	}
	for _, class := range policyCheckClasses {
		code, ok := documented[class.Name]
		if !ok {
			t.Errorf("%s (%d) is a failure class nvx can return and docs/exit-codes.md does not document it",
				class.Name, class.Code)
			continue
		}
		if code != class.Code {
			t.Errorf("%s is %d in code and %d in docs/exit-codes.md", class.Name, class.Code, code)
		}
		delete(documented, class.Name)
	}
	for name, code := range documented {
		t.Errorf("docs/exit-codes.md documents %s (%d), which nvx never returns", name, code)
	}
}
