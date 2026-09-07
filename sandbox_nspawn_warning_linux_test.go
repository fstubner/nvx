//go:build linux

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The systemd-nspawn provider says, before it runs anything, that what the
// command writes in the project will belong to root.
//
// nspawn requires root and the working directory is bound in writable, so files
// the command creates are root-owned on the host and the next install as
// yourself fails on files it cannot replace. The warning is the whole of the
// mitigation, which makes "it is printed" the thing to measure rather than
// assume: it sits after five guards (platform, binary present, euid 0, sandbox
// id, guest profile), any of which returning early would leave it unreachable
// with nothing to show for it.
//
// Root is required to reach it, so this runs under the privileged CI step and
// skips otherwise. The nspawn binary is a stub on PATH: this measures nvx's own
// output, which is emitted before the container is launched, and running a real
// container of the host root filesystem is not something a unit test should do.
func TestNspawnWarnsThatFilesWillBeRootOwned(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("the nspawn provider refuses to run without root, so the warning is unreachable")
	}
	stubDir := t.TempDir()
	stubLog := filepath.Join(stubDir, "invoked")
	stub := filepath.Join(stubDir, "systemd-nspawn")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho \"$@\" > "+stubLog+"\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := exec.LookPath("systemd-nspawn"); err != nil {
		t.Fatalf("the stub is not on PATH: %v", err)
	}

	workDir := t.TempDir()
	var code int
	out := captureStderrHere(t, func() {
		code = runNspawnSandbox(SandboxConfig{
			NvxHome: t.TempDir(),
			Command: "/bin/true",
			WorkDir: workDir,
		})
	})

	if _, err := os.Stat(stubLog); err != nil {
		t.Fatalf("the provider never reached the launch, so the warning's position is untested (exit %d):\n%s", code, out)
	}
	if !strings.Contains(out, "owned by root") {
		t.Fatalf("the provider ran without warning that files will be root-owned:\n%s", out)
	}
	if !strings.Contains(out, workDir) {
		t.Fatalf("the warning does not name the directory it is about:\n%s", out)
	}
}
