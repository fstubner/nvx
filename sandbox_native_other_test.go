//go:build !windows && !linux && !darwin

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A platform with no containment must refuse, not run the command anyway.
//
// The regression this guards is not subtle and shipped for a long time: this path
// executed the command with no isolation while nvx printed "Running in native
// sandbox". The test proves the refusal by handing it a command that leaves
// evidence behind -- if the file exists afterwards, the command ran, whatever the
// return code said.
func TestUnsupportedPlatformRefusesInsteadOfRunningUnprotected(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "it-ran")

	// /bin/sh exists on every Unix this build tag covers.
	config := SandboxConfig{
		Command: "sh",
		Args:    []string{"-c", "touch " + marker},
		NvxHome: t.TempDir(),
		WorkDir: dir,
	}

	code := platformLaunchNative(config, t.TempDir(), dir, "/bin/sh", os.Environ(), NetworkLaunchContext{})

	if code == 0 {
		t.Errorf("unsupported platform returned success; it must fail closed (got %d)", code)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the command ran: an uncontained process executed on a platform with no sandbox, which is the bug this refuses to allow")
	}
}
