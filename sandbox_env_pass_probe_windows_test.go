//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// isolation.environment.allow is WIRED IN, checked through a real AppContainer.
//
// Every test in sandbox_env_scrub_test.go calls scrubEnvironmentAllowing
// directly and would pass unchanged if PassEnv never reached the sandbox at all
// -- if env.go stopped filling it in, or a launch path rebuilt the environment
// from scratch. That is the failure this project has shipped before: a warning
// that said the right thing from a call site nothing reached.
//
// So this asserts through the built binary, in a real contained run, on the
// thing a user would check: the variable is visible to the program inside.
//
// The negative halves ride along in the same run deliberately. A pass-through
// that let everything through would satisfy the positive half on its own, and a
// credential must not cross the boundary however the policy is written.
func TestPassedEnvironmentReachesAContainedProcess(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (builds nvx and launches a real AppContainer)")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed; the contained program needs it")
	}

	proj := tempDir(t)
	nvxExe := filepath.Join(tempDir(t), "nvx.exe")
	if out, err := exec.Command("go", "build", "-o", nvxExe, ".").CombinedOutput(); err != nil {
		t.Fatalf("build nvx: %v\n%s", err, out)
	}

	policy := `{"isolation":{"environment":{"allow":["NVX_PROBE_PASSED","AWS_NVX_PROBE_SECRET"]}}}`
	if err := os.WriteFile(filepath.Join(proj, ".nvx-policy.json"), []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	show := `for (const k of ["NVX_PROBE_PASSED","NVX_PROBE_UNNAMED","AWS_NVX_PROBE_SECRET"])
  console.log("PROBE " + k + "=" + (process.env[k] === undefined ? "<absent>" : process.env[k]));`
	if err := os.WriteFile(filepath.Join(proj, "show.js"), []byte(show), 0o600); err != nil {
		t.Fatal(err)
	}

	// --strict contains "your own code" too, which is what makes a plain node
	// script a contained run rather than a direct one.
	cmd := exec.Command(nvxExe, "--strict", "shim", "node", "show.js")
	cmd.Dir = proj
	cmd.Env = append(os.Environ(),
		"NVX_PROBE_PASSED=reached",
		"NVX_PROBE_UNNAMED=should-not-reach",
		// AWS_ is a sensitive prefix, and the policy above names this anyway.
		"AWS_NVX_PROBE_SECRET=shh",
		// The policy loosens, so it is ignored until trusted. Neither -y nor
		// agent mode approves this on purpose; this is the documented opt-in.
		"NVX_TRUST_YES=true",
	)
	out, err := cmd.CombinedOutput()
	got := string(out)
	// A host that cannot create AppContainers at all refuses before the probe
	// runs; that is a skip, not a finding. See requireContainedRunLaunched.
	requireContainedRunLaunched(t, got)
	if err != nil && !strings.Contains(got, "PROBE ") {
		t.Fatalf("the contained run produced no probe output: %v\n%s", err, got)
	}

	if !strings.Contains(got, "PROBE NVX_PROBE_PASSED=reached") {
		t.Errorf("a variable named in isolation.environment.allow did not reach the contained process.\n"+
			"The policy is not being carried into the sandbox -- which every unit test would still pass "+
			"through.\nnvx said:\n%s", got)
	}
	if !strings.Contains(got, "PROBE NVX_PROBE_UNNAMED=<absent>") {
		t.Errorf("a variable the policy never named reached the contained process; the scrub is not "+
			"filtering.\nnvx said:\n%s", got)
	}
	if !strings.Contains(got, "PROBE AWS_NVX_PROBE_SECRET=<absent>") {
		t.Errorf("a variable matching a sensitive prefix reached the contained process even though only "+
			"the policy asked for it. A file in the repository must not be able to hand a credential to "+
			"package code.\nnvx said:\n%s", got)
	}
	if !strings.Contains(got, "AWS_NVX_PROBE_SECRET") || !strings.Contains(got, "not passed in") {
		t.Errorf("nothing told the policy author their credential entry was ignored.\nnvx said:\n%s", got)
	}
}
