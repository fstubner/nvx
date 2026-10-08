package nvx

import (
	"strings"
	"testing"
)

// When nvx has already said why npm could not resolve an install because it
// refused a host, it does not ask "Proceed?" as well. Measured 2026-10-08 on
// Windows, a project whose .npmrc named registry.example.org got the block, the
// `nvx allow-host` line, and then the generic question, whose answer is the
// broadest approval there is (NVX_YES) and cannot help, since the install itself
// goes through the same host. A sandbox that did not start has its own refusal,
// in verify_resolved_set_test.go.
func TestARefusedHostEndsTheResolutionWithoutAskingToProceed(t *testing.T) {
	w := newVerifyWorld(t, `{"typosquatting":{"enabled":false}}`)
	w.write("package.json", `{"name":"app"}`)
	w.addTsx(fakeRegistryPkg{})
	t.Cleanup(forgetWideningRefusals)
	resolve := func(launch func() int) (int, string) {
		launchNpmResolution = func(SandboxConfig, bool) int { return launch() }
		forgetWideningRefusals()
		return w.run("npm", "install", "tsx")
	}
	refusesAHost := func() int {
		recordWideningRefusal("a connection to registry.example.org:443")
		return 1
	}

	// A failure nvx has not explained is still asked about. npm failing on its
	// own, for a package that does not exist or a registry that is down, is the
	// case the question is for.
	code, out := resolve(func() int { return 1 })
	if code == 0 || !strings.Contains(out, "Proceed?") {
		t.Fatalf("an unexplained resolution failure was not asked about (exit %d):\n%s", code, out)
	}

	code, out = resolve(refusesAHost)
	if code != 1 {
		t.Errorf("exit %d, want npm's 1 (runShim turns it into 77 for the refused host)", code)
	}
	for _, unwanted := range []string{"Proceed?", "NVX_YES", "-y before the command"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("a failure nvx had explained was followed by the generic refusal (%q):\n%s", unwanted, out)
		}
	}
	if !strings.Contains(out, "because nvx refused a connection") {
		t.Errorf("the run does not say the install was stopped:\n%s", out)
	}
	byPolicy := false
	for _, rec := range readAuditRecords(t, w.home) {
		if rec["event"] == "check_refused" && rec["check"] == checkResolution && rec["by"] == answeredByPolicy {
			byPolicy = true
		}
	}
	if !byPolicy {
		t.Errorf("the refusal is not in the audit log as one nobody was asked about: %v", readAuditRecords(t, w.home))
	}

	// -y and NVX_YES cannot approve it, since the install would meet the same host.
	t.Setenv("NVX_YES", "1")
	if code, out := resolve(refusesAHost); code == 0 {
		t.Errorf("NVX_YES approved an install whose host nvx had refused:\n%s", out)
	}
}
