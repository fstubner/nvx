package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestShimsResolveInGitBashOnWindows covers the gap that once left Git Bash
// unprotected.
//
// cmd.exe and PowerShell find `npm` through PATHEXT. bash does not consult
// PATHEXT: it looks for `npm`, then `npm.exe`. When the shims were npm.cmd and
// npm.ps1, a bare `npm` in Git Bash resolved straight past nvx to the real npm
// -- no audit, no sandbox -- and `nvx doctor` reported interception as healthy,
// because it was answering the PATHEXT question rather than the one bash asks.
// An extensionless sh script closed that; the .exe shim closes it without a
// shell process, because bash resolves npm.exe itself.
//
// That is not a fringe configuration on Windows: agent harnesses commonly run
// Git Bash, and agent-driven installs are the case nvx exists for.
func TestShimsResolveInGitBashOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("exe shims are a Windows-only layout")
	}
	nvxHome := tempDir(t)
	if err := generateShims(nvxHome); err != nil {
		t.Fatalf("generateShims: %v", err)
	}

	shimDir := filepath.Join(nvxHome, "bin")
	for _, cmd := range []string{"npm", "npx", "node"} {
		if _, err := os.Stat(filepath.Join(shimDir, cmd+".exe")); err != nil {
			t.Errorf("no %s.exe shim: %v\nGit Bash would run the real %s, unwrapped.", cmd, err, cmd)
		}
		// Nothing bash would pick ahead of it: a bare `npm` beats `npm.exe` there.
		if _, err := os.Stat(filepath.Join(shimDir, cmd)); err == nil {
			t.Errorf("an extensionless %s shim is present; bash runs it through sh instead of the .exe", cmd)
		}
	}
}

// TestDoctorReportsMissingExeShims pins the second half of the same defect. An
// install made by an older nvx has .cmd/.ps1 shims and no .exe, and doctor must
// say so rather than reporting health from the PATHEXT answer alone.
func TestDoctorReportsMissingExeShims(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("exe shims are only separately reported on Windows")
	}
	nvxHome := tempDir(t)
	shimDir := filepath.Join(nvxHome, "bin")
	if err := os.MkdirAll(shimDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// The complete set an older nvx wrote -- including the extensionless sh
	// shim the previous check looked for -- and no npm.exe.
	for _, name := range []string{"npm.cmd", "npm.ps1", "npm"} {
		if err := os.WriteFile(filepath.Join(shimDir, name), []byte("@echo off\r\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	rep := diagnosePath(shimDir, nvxHome, []string{"npm"})
	if len(rep.missingExeShims) == 0 {
		t.Error("doctor did not notice the missing npm.exe shim; it would report interception as healthy " +
			"while a bare `npm` in Git Bash runs unwrapped")
	}
	out := formatDoctorReport(rep)
	if !strings.Contains(out, ".exe") || !strings.Contains(out, "Bash") {
		t.Errorf("the report does not say what is missing or which shell it affects:\n%s", out)
	}
	if !strings.Contains(out, "init-shims") {
		t.Errorf("the report does not say how to fix it:\n%s", out)
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

// TestProjectBinShimsExistForBashOnWindows covers the directory the first Git
// Bash fix missed. `~/.nvx/bin` got extensionless shims; `.nvx/project-bin` did
// not, so a project's own CLI (vite, eslint) still resolved past nvx in Git Bash
// with "command not found" — same shell, same cause, same release.
func TestProjectBinShimsExistForBashOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the extensionless shim only differs from the POSIX one on Windows")
	}
	project := tempDir(t)
	nvxHome := tempDir(t)
	binDir := filepath.Join(project, "node_modules", ".bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A name that resolves nowhere else, so the anti-shadow rule does not skip it.
	const cli = "nvx-fixture-vite"
	for _, name := range []string{cli + ".cmd", cli + ".ps1", cli} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := generateProjectBinShims(project, nvxHome); err != nil {
		t.Fatalf("generateProjectBinShims: %v", err)
	}

	shimDir := projectBinDir(project, nvxHome)
	if _, err := os.Stat(filepath.Join(shimDir, cli+".cmd")); err != nil {
		t.Errorf("no .cmd shim for a local CLI: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(shimDir, cli))
	if err != nil {
		t.Fatalf("no extensionless shim for a local CLI: %v (Git Bash would report command not found)", err)
	}
	if !strings.HasPrefix(string(data), "#!") {
		t.Errorf("the extensionless project-bin shim has no shebang: %s", string(data))
	}
}
