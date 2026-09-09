package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
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

	// Recorded before the call, because it decides which path runSandbox takes.
	// runSandbox returns through execBareCommand when a session is already
	// active, and that path never reaches the provider check -- so it is the one
	// way to get a non-zero exit with nothing printed, which is exactly what a CI
	// failure showed and what no local run has reproduced. Nothing else in the
	// package touches this counter, so it should always be zero here; the next
	// failure says whether that held.
	sessionDepth := atomic.LoadInt32(&sandboxSessionActive)

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
		// The exit code and the byte count are in the message because the output
		// alone has already failed to explain one CI failure: it arrived empty,
		// which says the run stopped without printing anything and leaves no way to
		// tell which branch returned. Both facts are cheap here, and the second
		// occurrence of a flake is not.
		t.Fatalf("the run stopped with exit %d (sandbox session depth on entry: %d), and not because the provider was rejected; stderr was %d bytes:\n%s",
			code, sessionDepth, len(out), out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the command ran despite the containment provider being unknown")
	}
}

// The refusal names every provider that does exist.
//
// With the experimental backends retired, this message is the only place a
// person is told what the valid names are, and it was a hand-written list that
// omitted the macOS one. Derived from the registry now, so a provider added or
// removed cannot leave the message behind.
func TestTheRefusalListsEveryProviderThereIs(t *testing.T) {
	out := captureStderrHere(t, func() {
		runSandbox(SandboxConfig{
			NvxHome:            tempDir(t),
			Command:            "does-not-matter",
			FilesystemProvider: "definitely-not-a-provider",
		})
	})
	for _, name := range []string{"native", "docker", "sandbox-exec"} {
		if !strings.Contains(out, name) {
			t.Fatalf("the refusal does not mention the %s provider:\n%s", name, out)
		}
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
