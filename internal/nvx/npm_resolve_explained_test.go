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

func resolutionStopsWith(t *testing.T, launch func(cfg SandboxConfig) int) (code int, out string, w *verifyWorld) {
	t.Helper()
	w = newVerifyWorld(t, `{"typosquatting":{"enabled":false}}`)
	w.write("package.json", `{"name":"app"}`)
	w.addTsx(fakeRegistryPkg{})
	forgetWideningRefusals()
	t.Cleanup(forgetWideningRefusals)
	launchNpmResolution = func(cfg SandboxConfig, contain bool) int { return launch(cfg) }
	code, out = w.run("npm", "install", "tsx")
	return code, out, w
}

func TestARefusedHostEndsTheResolutionWithoutAskingToProceed(t *testing.T) {
	code, out, w := resolutionStopsWith(t, func(SandboxConfig) int {
		recordWideningRefusal("a connection to registry.example.org:443")
		return 1
	})
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
	if rec := findRecord(readAuditRecords(t, w.home), "check_refused", checkResolution); rec == nil {
		t.Errorf("the refusal is not in the audit log: %v", readAuditRecords(t, w.home))
	}

	// -y and NVX_YES cannot approve it: the install would meet the same host.
	t.Setenv("NVX_YES", "1")
	forgetWideningRefusals()
	if code, out := w.run("npm", "install", "tsx"); code == 0 {
		t.Errorf("NVX_YES approved an install whose host nvx had refused:\n%s", out)
	}
}

// A failure nvx has not explained is still asked about. npm failing on its own
// (a package that does not exist, a registry that is down) is the case the
// question is for.
func TestAnUnexplainedResolutionFailureIsStillAskedAbout(t *testing.T) {
	code, out, _ := resolutionStopsWith(t, func(SandboxConfig) int { return 1 })
	if code == 0 {
		t.Fatalf("a failed resolution nobody explained was waved through:\n%s", out)
	}
	if !strings.Contains(out, "Proceed?") {
		t.Errorf("the question was not asked:\n%s", out)
	}
}
