package nvx

import (
	"fmt"
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
		return "1.0.0", time.Time{}, false, nil
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
