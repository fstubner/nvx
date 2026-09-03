//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSeatbeltLaunchFailsClosedWithoutSandboxExec is the macOS half of the
// fail-closed guarantee, and the last cell in the macOS column of
// docs/enforcement-matrix.md that rested on reading the code.
//
// Every other macOS claim is asserted by scripts/sandbox-enforcement-macos.sh
// against a real runner. This one cannot be: it needs /usr/bin/sandbox-exec to
// be absent, and it cannot be removed from the machine the test runs on. So the
// path is a variable and the test moves it, which checks the branch that
// matters -- nvx refusing to launch -- rather than the filesystem.
//
// The claim is narrow on purpose. It says nvx does not run the command
// uncontained when the sandbox binary is missing. It does not say anything about
// what Seatbelt enforces, which is what the enforcement probe is for.
func TestSeatbeltLaunchFailsClosedWithoutSandboxExec(t *testing.T) {
	orig := seatbeltExecPath
	defer func() { seatbeltExecPath = orig }()
	seatbeltExecPath = filepath.Join(tempDir(t), "no-such-sandbox-exec")

	// A command that would leave a trace if it ran. If containment is skipped
	// rather than refused, this file appears and the assertion below is not the
	// only thing that fails.
	marker := filepath.Join(tempDir(t), "should-not-exist")
	config := SandboxConfig{
		Command: "/bin/sh",
		Args:    []string{"-c", "touch " + marker},
	}

	code, _ := platformLaunchNative(config, tempDir(t), tempDir(t), "/bin/sh", nil, NetworkLaunchContext{Mode: "proxy"})
	if code == 0 {
		t.Error("nvx reported success with sandbox-exec missing; it must fail closed rather than run uncontained")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the command RAN with sandbox-exec missing: that is running uncontained, not failing closed")
	}
}
