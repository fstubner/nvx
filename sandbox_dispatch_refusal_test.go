package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// runSandbox refuses a provider it does not know, rather than running the
// command anyway.
//
// This is the dispatcher: it picks the containment backend and every contained
// command in nvx goes through it. Nothing called it directly from a test --
// the containment probes drive the layer below -- which is the structural
// reason a dispatch bug survived a green suite once already. The behaviour
// worth pinning is fail-closed: a provider name nvx cannot honour must stop the
// run, never fall through to running the command uncontained.
//
// The command is a marker-writing script, so "it did not run" is measured
// rather than inferred from an exit code.
func TestRunSandboxRefusesAnUnknownFilesystemProvider(t *testing.T) {
	nvxHome := tempDir(t)
	marker, cmdPath := markerCommand(t)

	var code int
	out := captureStderrHere(t, func() {
		code = runSandbox(SandboxConfig{
			NvxHome:            nvxHome,
			Command:            cmdPath,
			FilesystemProvider: "definitely-not-a-provider",
		})
	})

	if code == 0 {
		t.Fatal("an unknown containment provider was accepted")
	}
	// The message, not just the exit code: a dispatcher that quietly fell back
	// to some other provider would also return non-zero when that provider's
	// own launch failed, and the run would look refused when it was not.
	if !strings.Contains(out, "Unknown filesystem provider") {
		t.Fatalf("the run stopped, but not because the provider was rejected:\n%s", out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the command ran despite the containment provider being unknown")
	}
}

// And an experimental provider needs to be asked for. wsl, wslc and nspawn are
// unfinished; running under one because a policy file named it, with no opt-in,
// is containment the user was told they had and did not.
func TestRunSandboxRefusesAnExperimentalProviderUnlessEnabled(t *testing.T) {
	t.Setenv("NVX_EXPERIMENTAL", "")
	nvxHome := tempDir(t)
	marker, cmdPath := markerCommand(t)

	var code int
	out := captureStderrHere(t, func() {
		code = runSandbox(SandboxConfig{
			NvxHome:            nvxHome,
			Command:            cmdPath,
			FilesystemProvider: "systemd-nspawn",
		})
	})

	if code == 0 {
		t.Fatal("an experimental containment provider ran without NVX_EXPERIMENTAL")
	}
	if !strings.Contains(out, "experimental") {
		t.Fatalf("the run stopped, but not because the provider is experimental:\n%s", out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the command ran under an experimental provider that was never enabled")
	}
}

// markerCommand returns the path a command would create if it ran, and the
// command that creates it.
func markerCommand(t *testing.T) (marker, cmdPath string) {
	t.Helper()
	dir := tempDir(t)
	marker = filepath.Join(dir, "it-ran")
	if runtime.GOOS == "windows" {
		cmdPath = filepath.Join(dir, "run.bat")
		body := "@echo off\r\necho ran > " + marker + "\r\n"
		if err := os.WriteFile(cmdPath, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
		return marker, cmdPath
	}
	cmdPath = filepath.Join(dir, "run.sh")
	body := "#!/bin/sh\necho ran > " + marker + "\n"
	if err := os.WriteFile(cmdPath, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return marker, cmdPath
}
