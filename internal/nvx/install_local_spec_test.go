package nvx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A local path is not a package name, and is not sent to the registry as one.
//
// `npm install ./vendor/thing.tgz`, `npm i ../sibling`, `npm i file:../lib` and
// `npm i github:user/repo` are all ordinary npm specs and none of them names a
// registry package. nvx passed the spec through as a package name, asked
// registry.npmjs.org for it, got a 404, and then asked whether to proceed
// without metadata checks -- which, with no terminal to ask (an agent, CI),
// denies. So installing a local tarball failed, and the reason given was that
// its "registry metadata could not be verified".
//
// The checks that do not apply are skipped and said to be skipped. Nothing here
// weakens a registry install: a name that is a registry name still gets every
// check it got before.
func TestALocalOrGitSpecIsNotLookedUpInTheRegistry(t *testing.T) {
	specs := []string{
		"./vendor/thing.tgz",
		"../sibling",
		"file:../lib",
		"github:someone/repo",
		"https://example.invalid/pkg.tgz",
		"/abs/path/pkg.tgz",
	}
	for _, spec := range specs {
		t.Run(spec, func(t *testing.T) {
			asked := ""
			origResolve := resolveNpmPackageDetailsForVerify
			resolveNpmPackageDetailsForVerify = func(pkgName, versionQuery string) (string, time.Time, bool, error) {
				asked = pkgName
				return "", time.Time{}, false, fmt.Errorf("Not Found")
			}
			t.Cleanup(func() { resolveNpmPackageDetailsForVerify = origResolve })
			t.Setenv("NVX_NONINTERACTIVE", "1")

			var code int
			out := captureStderrHere(t, func() {
				code, _ = runVerifyInstall([]string{spec}, testNvxHomeWithTyposquattingDisabled(t))
			})

			if asked != "" {
				t.Fatalf("the registry was asked for %q, which is a local or remote spec, not a package name", asked)
			}
			if code != 0 {
				t.Fatalf("installing %s was refused (exit %d):\n%s", spec, code, out)
			}
			if !strings.Contains(out, spec) {
				t.Fatalf("nothing said that %s was not registry-checked:\n%s", spec, out)
			}
		})
	}
}

// The control: a registry name is still verified, so this does not become a way
// to skip the checks by dressing a package up as something else.
func TestARegistryNameIsStillVerified(t *testing.T) {
	asked := ""
	origResolve := resolveNpmPackageDetailsForVerify
	resolveNpmPackageDetailsForVerify = func(pkgName, versionQuery string) (string, time.Time, bool, error) {
		asked = pkgName
		// Published long ago. A version with no publish time is asked about.
		return "1.0.0", time.Now().Add(-1000 * time.Hour), false, nil
	}
	t.Cleanup(func() { resolveNpmPackageDetailsForVerify = origResolve })
	origScan := scanVulnerabilitiesBatchForVerify
	scanVulnerabilitiesBatchForVerify = func(packages []OSVQuery) (map[string][]OSVVuln, error) { return nil, nil }
	t.Cleanup(func() { scanVulnerabilitiesBatchForVerify = origScan })

	if code, _ := runVerifyInstall([]string{"@scope/pkg"}, testNvxHomeWithTyposquattingDisabled(t)); code != 0 {
		t.Fatalf("a scoped registry package was refused (exit %d)", code)
	}
	if asked != "@scope/pkg" {
		t.Fatalf("the registry was asked for %q, want @scope/pkg", asked)
	}
}

// `nvx NPM install x` is accepted as npm, so it gets npm's checks. The command
// was matched case-insensitively and then verified by an exact, lowercase
// switch, so the install ran with the blocklist never consulted.
func TestAnUppercaseCommandNameIsStillVerified(t *testing.T) {
	origResolve := resolveNpmPackageDetailsForVerify
	resolveNpmPackageDetailsForVerify = func(pkgName, versionQuery string) (string, time.Time, bool, error) {
		return "1.0.0", time.Time{}, false, nil
	}
	t.Cleanup(func() { resolveNpmPackageDetailsForVerify = origResolve })
	home := tempDir(t)
	// Isolation off and nothing on PATH, so a regression that skips the checks
	// fails to find npm and returns, rather than launching a sandbox or a real
	// install.
	policy := `{"typosquatting":{"enabled":false},"blocked_packages":["left-pad"],"isolation":{"enabled":false}}`
	if err := os.WriteFile(filepath.Join(home, "policy.json"), []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	inProjectDir(t, tempDir(t))
	t.Setenv("PATH", tempDir(t))

	var code int
	out := captureStderrHere(t, func() { code = runShim("NPM", []string{"install", "left-pad"}, home) })
	if code == 0 || !strings.Contains(out, "Blocked by security policy") {
		t.Fatalf("`NPM install left-pad` exited %d without the blocklist refusing it:\n%s", code, out)
	}
}

// An npm alias installs its target, so the target is what gets checked.
//
// `myalias@npm:left-pad` was checked as a package called "myalias", so a
// blocklist entry for left-pad did not stop it. The scoped form,
// `alias@npm:@scope/pkg`, read as the user/repo shorthand and skipped every
// check.
func TestAnNpmAliasIsCheckedUnderItsTarget(t *testing.T) {
	origResolve := resolveNpmPackageDetailsForVerify
	resolveNpmPackageDetailsForVerify = func(pkgName, versionQuery string) (string, time.Time, bool, error) {
		// Published long ago, so only the blocklist can refuse it.
		return "1.0.0", time.Now().Add(-1000 * time.Hour), false, nil
	}
	t.Cleanup(func() { resolveNpmPackageDetailsForVerify = origResolve })
	origScan := scanVulnerabilitiesBatchForVerify
	scanVulnerabilitiesBatchForVerify = func(packages []OSVQuery) (map[string][]OSVVuln, error) { return nil, nil }
	t.Cleanup(func() { scanVulnerabilitiesBatchForVerify = origScan })

	for _, spec := range []string{"myalias@npm:left-pad", "myalias@npm:left-pad@1.3.0", "@a/b@npm:@evil/pkg@^2"} {
		t.Run(spec, func(t *testing.T) {
			home := tempDir(t)
			policy := `{"typosquatting":{"enabled":false},"blocked_packages":["left-pad","@evil/pkg"]}`
			if err := os.WriteFile(filepath.Join(home, "policy.json"), []byte(policy), 0o600); err != nil {
				t.Fatal(err)
			}
			if code, _ := runVerifyInstall([]string{spec}, home); code == 0 {
				t.Fatalf("%s installs a blocked package and was allowed", spec)
			}
		})
	}

	for input, want := range map[string][2]string{
		"myalias@npm:left-pad":       {"left-pad", ""},
		"myalias@npm:left-pad@1.3.0": {"left-pad", "1.3.0"},
		"@a/b@npm:@evil/pkg@^2":      {"@evil/pkg", "^2"},
	} {
		if name, ver := parsePackageQuery(input); name != want[0] || ver != want[1] {
			t.Errorf("parsePackageQuery(%q) = (%q, %q), want (%q, %q)", input, name, ver, want[0], want[1])
		}
	}
}
