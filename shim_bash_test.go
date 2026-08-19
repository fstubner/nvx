package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestShimsExistForBashOnWindows covers the gap that left Git Bash unprotected.
//
// cmd.exe and PowerShell find `npm` through PATHEXT and pick up npm.cmd/npm.ps1.
// bash does not consult PATHEXT: it looks for a file named exactly `npm`. With
// only the two extensioned shims present, a bare `npm` in Git Bash resolved
// straight past nvx to the real npm -- no audit, no sandbox -- and `nvx doctor`
// reported interception as healthy, because it was answering the PATHEXT
// question rather than the one bash asks.
//
// That is not a fringe configuration on Windows: agent harnesses commonly run
// Git Bash, and agent-driven installs are the case nvx exists for.
func TestShimsExistForBashOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the extensionless shim only differs from the POSIX one on Windows")
	}
	nvxHome := t.TempDir()
	if err := generateShims(nvxHome); err != nil {
		t.Fatalf("generateShims: %v", err)
	}

	shimDir := filepath.Join(nvxHome, "bin")
	for _, cmd := range []string{"npm", "npx", "node"} {
		p := filepath.Join(shimDir, cmd)
		data, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("no extensionless shim for %q: %v\nGit Bash would run the real %s, unwrapped.", cmd, err, cmd)
			continue
		}
		if !strings.HasPrefix(string(data), "#!") {
			t.Errorf("%s has no shebang; bash would not treat it as a script:\n%s", p, string(data))
		}
		// quotePOSIXShell quotes the command name, so match either spelling
		// rather than pinning the quoting style.
		body := string(data)
		if !strings.Contains(body, "shim "+cmd) && !strings.Contains(body, "shim '"+cmd+"'") {
			t.Errorf("%s does not dispatch through nvx:\n%s", p, body)
		}
	}

	// The extensioned shims must survive alongside it, or cmd.exe and PowerShell
	// lose interception in exchange for bash gaining it.
	for _, name := range []string{"npm.cmd", "npm.ps1"} {
		if _, err := os.Stat(filepath.Join(shimDir, name)); err != nil {
			t.Errorf("%s went missing: %v", name, err)
		}
	}
}

// TestDoctorReportsMissingBashShims pins the second half of the same defect. An
// install made by an older nvx has no extensionless shims, and doctor must say so
// rather than reporting health from the PATHEXT answer alone.
func TestDoctorReportsMissingBashShims(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("bash shims are only separately reported on Windows")
	}
	nvxHome := t.TempDir()
	shimDir := filepath.Join(nvxHome, "bin")
	if err := os.MkdirAll(shimDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Only the PATHEXT-visible shim, as an older nvx would have left it.
	if err := os.WriteFile(filepath.Join(shimDir, "npm.cmd"), []byte("@echo off\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	rep := diagnosePath(shimDir, nvxHome, []string{"npm"})
	if len(rep.missingPosixShims) == 0 {
		t.Error("doctor did not notice the missing bash shim; it would report interception as healthy " +
			"while a bare `npm` in Git Bash runs unwrapped")
	}
	out := formatDoctorReport(rep)
	if !strings.Contains(out, "bash") {
		t.Errorf("the report does not mention bash, so the reader cannot tell what is wrong:\n%s", out)
	}
}

// TestStdinIsInteractiveIsFalseUnderTest is the fail-closed half.
//
// `go test` runs with stdin not attached to a terminal, so this must be false
// here. It used to be decided by whether a console existed at all, which on
// Windows is true even with stdin redirected from NUL -- so a prompt was issued
// to a console nobody was watching and the process simply stopped, instead of
// denying as README and SECURITY.md both promise.
func TestStdinIsInteractiveIsFalseUnderTest(t *testing.T) {
	if stdinIsInteractive() {
		t.Error("stdin reported as interactive under `go test`; a security prompt would block " +
			"instead of failing closed in CI and agent harnesses")
	}
}
