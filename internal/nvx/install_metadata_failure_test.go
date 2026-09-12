package nvx

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Approving a registry-metadata failure gives up the vulnerability scan too, and
// has to say so.
//
// The advisory scan is driven by the resolved version, which is collected at the
// bottom of the same loop that resolves metadata. When resolution fails and the
// prompt is approved, that iteration continues -- so the package never reaches
// the OSV query, and the run finishes reporting no known vulnerabilities in a
// package it never asked about. The question said "Proceed without metadata
// checks?", which reads as the registry lookup that just failed.
//
// nvx cannot run the scan in that state: OSV is queried by exact version and the
// version is what could not be resolved. So the invariant is not "always scan" --
// it is that a package which was not scanned is named as not scanned. Written
// that way rather than as "the scanner is not called", so that teaching nvx to
// scan without a resolved version later satisfies this test instead of failing
// it.
func TestApprovingAMetadataFailureSaysTheVulnerabilityScanIsSkippedToo(t *testing.T) {
	const pkg = "some-registry-package"

	origResolve := resolveNpmPackageDetailsForVerify
	resolveNpmPackageDetailsForVerify = func(pkgName, versionQuery string) (string, time.Time, bool, error) {
		return "", time.Time{}, false, fmt.Errorf("503 Service Unavailable")
	}
	t.Cleanup(func() { resolveNpmPackageDetailsForVerify = origResolve })

	scanned := false
	origScan := scanVulnerabilitiesBatchForVerify
	scanVulnerabilitiesBatchForVerify = func(packages []OSVQuery) (map[string][]OSVVuln, error) {
		for _, q := range packages {
			if q.Package.Name == pkg {
				scanned = true
			}
		}
		return nil, nil
	}
	t.Cleanup(func() { scanVulnerabilitiesBatchForVerify = origScan })

	t.Setenv("NVX_YES", "true")

	var code int
	out := captureStderrHere(t, func() {
		code, _ = runVerifyInstall([]string{pkg}, testNvxHomeWithTyposquattingDisabled(t))
	})
	if code != 0 {
		t.Fatalf("the approved install was refused (exit %d):\n%s", code, out)
	}
	if scanned {
		return // it was scanned after all, so there is nothing to disclose
	}

	lower := strings.ToLower(out)
	if !strings.Contains(lower, "vulnerabilit") {
		t.Errorf("%s was never scanned for advisories and nothing said so; "+
			"approving a registry error silently gives up the CVE check.\nOutput:\n%s", pkg, out)
	}
}

// The same disclosure has to be in the QUESTION, not only in the aftermath.
//
// A warning printed after the answer is given cannot inform the answer. This is
// the case that matters: a person deciding whether a transient 503 is worth
// proceeding through is deciding about the advisory scan as well, and the
// wording is the only thing that tells them.
//
// Read off the non-interactive denial, which echoes the prompt verbatim -- the
// one way to see the text of a prompt that no test can answer.
func TestTheMetadataFailurePromptItselfNamesTheVulnerabilityScan(t *testing.T) {
	origResolve := resolveNpmPackageDetailsForVerify
	resolveNpmPackageDetailsForVerify = func(pkgName, versionQuery string) (string, time.Time, bool, error) {
		return "", time.Time{}, false, fmt.Errorf("503 Service Unavailable")
	}
	t.Cleanup(func() { resolveNpmPackageDetailsForVerify = origResolve })

	t.Setenv("NVX_NONINTERACTIVE", "1")

	out := captureStderrHere(t, func() {
		runVerifyInstall([]string{"some-registry-package"}, testNvxHomeWithTyposquattingDisabled(t))
	})

	prompt := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "Prompt was:") && strings.Contains(line, "registry metadata") {
			prompt = strings.ToLower(line)
			break
		}
	}
	if prompt == "" {
		t.Fatalf("the metadata-failure prompt was not asked at all:\n%s", out)
	}
	if !strings.Contains(prompt, "vulnerability") {
		t.Errorf("the prompt asks only about metadata while approving it also skips the advisory scan:\n  %s", prompt)
	}
}
