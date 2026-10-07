package nvx

import (
	"strings"
	"testing"
)

// `nvx default` must not tell anyone to put the `current` link on PATH.
//
// It printed "Make sure '...\current' is added to your environment PATH."
// Only the shim directory belongs on PATH, and doctor reports a runtime
// directory ahead of it as shadowing the shims, so following the advice breaks
// interception.
func TestDefaultDoesNotAdviseAddingTheCurrentLinkToPath(t *testing.T) {
	nvxHome := tempDir(t)
	installedLTSLayout(t, nvxHome, "v22.11.0")

	out := captureStderrHere(t, func() { runDefault("v22.11.0", nvxHome) })
	if got := getGlobalDefaultVersion(nvxHome); got != "v22.11.0" {
		t.Fatalf("default = %q after `nvx default v22.11.0`\n%s", got, out)
	}
	// The shim directory is the one that belongs on PATH, and `nvx default` says so
	// when it has just written the shims there. What it must never name is the
	// `current` link or a runtime directory.
	if strings.Contains(out, "current") || strings.Contains(out, "versions") {
		t.Errorf("`nvx default` gave PATH advice naming a runtime directory:\n%s", out)
	}
}
