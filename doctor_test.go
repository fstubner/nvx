package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolveCommandOnPath(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()

	// Command name differs by platform: on Windows shims are "<cmd>.cmd".
	shimName := "npm"
	if runtime.GOOS == "windows" {
		shimName = "npm.cmd"
	}
	writeExec(t, filepath.Join(dirA, shimName))
	writeExec(t, filepath.Join(dirB, shimName))

	pathEnv := dirA + string(os.PathListSeparator) + dirB
	got := resolveCommandOnPath("npm", pathEnv)
	want := filepath.Join(dirA, shimName)
	if got != want {
		t.Fatalf("resolveCommandOnPath = %q, want %q (first dir wins)", got, want)
	}

	if resolveCommandOnPath("does-not-exist", pathEnv) != "" {
		t.Fatalf("expected empty for missing command")
	}

	// Unix: a non-executable file (0644) must not resolve as a command.
	if runtime.GOOS != "windows" {
		dirC := t.TempDir()
		if err := os.WriteFile(filepath.Join(dirC, "tool"), []byte("data\n"), 0644); err != nil { // #nosec G306 -- test fixture
			t.Fatal(err)
		}
		if resolveCommandOnPath("tool", dirC) != "" {
			t.Fatalf("expected empty for non-executable file")
		}
	}
}

func TestDiagnosePath(t *testing.T) {
	nvxHome := t.TempDir()
	shimDir := filepath.Join(nvxHome, "bin")
	current := filepath.Join(nvxHome, "current")
	if err := os.MkdirAll(shimDir, 0755); err != nil { // #nosec G301 -- test fixture
		t.Fatal(err)
	}
	if err := os.MkdirAll(current, 0755); err != nil { // #nosec G301 -- test fixture
		t.Fatal(err)
	}

	// Healthy: shim dir first, current after.
	healthy := shimDir + string(os.PathListSeparator) + current
	rep := diagnosePath(healthy, nvxHome, nil)
	if !rep.shimDirOnPath || rep.shimDirIndex != 0 {
		t.Fatalf("healthy: shimDirOnPath=%v index=%d, want true/0", rep.shimDirOnPath, rep.shimDirIndex)
	}
	if len(rep.shadowedBy) != 0 {
		t.Fatalf("healthy: want no shadowing, got %+v", rep.shadowedBy)
	}

	// Broken: current before shim dir -> shadowing reported.
	broken := current + string(os.PathListSeparator) + shimDir
	rep = diagnosePath(broken, nvxHome, nil)
	if !rep.shimDirOnPath || rep.shimDirIndex != 1 {
		t.Fatalf("broken: index=%d, want 1", rep.shimDirIndex)
	}
	if len(rep.shadowedBy) != 1 || rep.shadowedBy[0].index != 0 {
		t.Fatalf("broken: want current shadowing at index 0, got %+v", rep.shadowedBy)
	}

	// Absent: shim dir not on PATH at all.
	rep = diagnosePath(current, nvxHome, nil)
	if rep.shimDirOnPath {
		t.Fatalf("absent: shimDirOnPath should be false")
	}
}

func TestDiagnosePathCommands(t *testing.T) {
	nvxHome := t.TempDir()
	shimDir := filepath.Join(nvxHome, "bin")
	current := filepath.Join(nvxHome, "current")
	if err := os.MkdirAll(shimDir, 0755); err != nil { // #nosec G301 -- test fixture
		t.Fatal(err)
	}
	if err := os.MkdirAll(current, 0755); err != nil { // #nosec G301 -- test fixture
		t.Fatal(err)
	}

	// Shim in bin/ and a shadowing copy in current/.
	shimName := "npm"
	if runtime.GOOS == "windows" {
		shimName = "npm.cmd"
	}
	writeExec(t, filepath.Join(shimDir, shimName))
	writeExec(t, filepath.Join(current, shimName))

	// Shim dir first -> command resolves via the shim.
	front := shimDir + string(os.PathListSeparator) + current
	rep := diagnosePath(front, nvxHome, []string{"npm"})
	if len(rep.commands) != 1 {
		t.Fatalf("want 1 command resolution, got %d", len(rep.commands))
	}
	if !rep.commands[0].viaShim {
		t.Fatalf("shim-first: viaShim=false, want true (resolved %q)", rep.commands[0].resolved)
	}
	if !dirWithin(rep.commands[0].resolved, shimDir) {
		t.Fatalf("shim-first: resolved %q not under shim dir %q", rep.commands[0].resolved, shimDir)
	}

	// current/ first -> command resolves outside the shim dir.
	back := current + string(os.PathListSeparator) + shimDir
	rep = diagnosePath(back, nvxHome, []string{"npm"})
	if rep.commands[0].viaShim {
		t.Fatalf("current-first: viaShim=true, want false (resolved %q)", rep.commands[0].resolved)
	}
	if !dirWithin(rep.commands[0].resolved, current) {
		t.Fatalf("current-first: resolved %q not under current dir %q", rep.commands[0].resolved, current)
	}
}

func TestShimPathPrependSnippet(t *testing.T) {
	// POSIX: must reference the bash-form dir and export PATH with it in front.
	bash := shimPathPrependSnippet("bash", "/home/u/.nvx/bin")
	if !strings.Contains(bash, "/home/u/.nvx/bin") {
		t.Fatalf("bash snippet missing shim dir: %s", bash)
	}
	if !strings.Contains(bash, "export PATH=") {
		t.Fatalf("bash snippet must export PATH: %s", bash)
	}

	// PowerShell: must filter the existing entry and reassign $env:PATH.
	ps := shimPathPrependSnippet("powershell", `C:\Users\u\.nvx\bin`)
	if !strings.Contains(ps, `.nvx\bin`) {
		t.Fatalf("powershell snippet missing shim dir: %s", ps)
	}
	if !strings.Contains(ps, "$env:PATH") {
		t.Fatalf("powershell snippet must set $env:PATH: %s", ps)
	}
}

func TestFormatDoctorReport(t *testing.T) {
	shimDir := filepath.FromSlash("/home/u/.nvx/bin")
	healthy := doctorReport{
		shimDir: shimDir, shimDirOnPath: true, shimDirIndex: 0,
		commands: []commandResolution{
			{name: "npm", resolved: filepath.Join(shimDir, "npm"), viaShim: true},
		},
	}
	out := formatDoctorReport(healthy)
	if !strings.Contains(out, "npm") || !strings.Contains(strings.ToLower(out), "ok") {
		t.Fatalf("healthy report should mark npm OK:\n%s", out)
	}

	broken := doctorReport{
		shimDir: shimDir, shimDirOnPath: true, shimDirIndex: 2,
		shadowedBy: []pathShadow{{dir: filepath.FromSlash("/home/u/.nvx/current"), index: 0}},
		commands: []commandResolution{
			{name: "npm", resolved: filepath.FromSlash("/home/u/.nvx/current/npm"), viaShim: false},
		},
	}
	out = formatDoctorReport(broken)
	if !strings.Contains(out, "current") {
		t.Fatalf("broken report should name the shadowing dir:\n%s", out)
	}
}

// writeExec creates an executable file (0755) so Unix resolution accepts it.
func writeExec(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0755); err != nil { // #nosec G306 -- test fixture
		t.Fatal(err)
	}
}
