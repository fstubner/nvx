package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolveCommandOnPath(t *testing.T) {
	dirA := tempDir(t)
	dirB := tempDir(t)

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
		dirC := tempDir(t)
		if err := os.WriteFile(filepath.Join(dirC, "tool"), []byte("data\n"), 0644); err != nil { // #nosec G306 -- test fixture
			t.Fatal(err)
		}
		if resolveCommandOnPath("tool", dirC) != "" {
			t.Fatalf("expected empty for non-executable file")
		}
	}
}

func TestDiagnosePath(t *testing.T) {
	nvxHome := tempDir(t)
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
	nvxHome := tempDir(t)
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

// The healthy line must describe what was actually checked.
//
// It said "shim dir is first on PATH (position 53)", which contradicts itself on
// the page and is not the test: diagnosePath only establishes that no nvx
// raw-runtime directory sits ahead of the shim dir, and position is irrelevant
// to that. Someone diagnosing a PATH problem was being told nvx had verified
// something it never looked at.
//
// The existing healthy case above cannot catch this -- its index is 0, so
// "first" happens to be true. A shim dir that is genuinely not first, with
// nothing shadowing it, is the case that exposes the claim.
func TestTheHealthyPathLineDoesNotClaimACheckThatWasNotMade(t *testing.T) {
	out := formatDoctorReport(doctorReport{
		shimDir:       filepath.FromSlash("/home/u/.nvx/bin"),
		shimDirOnPath: true,
		shimDirIndex:  53,
	})
	if !strings.Contains(out, "[OK]") {
		t.Fatalf("a shim dir on PATH with nothing shadowing it is healthy:\n%s", out)
	}
	if strings.Contains(out, "first on PATH") {
		t.Fatalf("claimed the shim dir is first while reporting position 53:\n%s", out)
	}
	if !strings.Contains(out, "53") {
		t.Fatalf("the position is still worth reporting:\n%s", out)
	}
}

func TestRebuildUserPath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows user-PATH repair semantics")
	}
	shimDir := `C:\Users\u\.nvx\bin`
	current := `C:\Users\u\.nvx\current`
	other := `C:\Windows\System32`

	// current is ahead of the shim dir and must be dropped; shim dir must lead.
	existing := current + ";" + other + ";" + shimDir
	got := rebuildUserPath(existing, shimDir, []string{current})
	want := shimDir + ";" + other
	if got != want {
		t.Fatalf("rebuildUserPath = %q, want %q", got, want)
	}

	// Idempotent: a healthy PATH is unchanged.
	if again := rebuildUserPath(got, shimDir, []string{current}); again != want {
		t.Fatalf("rebuildUserPath not idempotent: %q", again)
	}
}

func TestPathIsShadowed(t *testing.T) {
	nvxHome := tempDir(t)
	shimDir := filepath.Join(nvxHome, "bin")
	current := filepath.Join(nvxHome, "current")
	sep := string(os.PathListSeparator)

	if pathIsShadowed(shimDir+sep+current, nvxHome) {
		t.Fatalf("healthy PATH should not be shadowed")
	}
	if !pathIsShadowed(current+sep+shimDir, nvxHome) {
		t.Fatalf("current ahead of shim dir should be shadowed")
	}

	// Shim dir absent entirely: not "shadowed" in the precedence sense, even
	// though a raw runtime dir is present — this is a distinct failure mode
	// (nvx doctor reports it separately as "shim dir is not on PATH").
	if pathIsShadowed(current, nvxHome) {
		t.Fatalf("shim dir absent from PATH should not report as shadowed")
	}
}

// hintIfShadowed must persist its "already shown" state across process
// boundaries, not just within one process: a single user-facing command
// routinely spawns a tree of separate nvx.exe processes (an npm lifecycle
// script alone can nest prepublishOnly -> build -> clean -> node), so an
// in-process sync.Once reprints the warning once per process in that tree
// rather than once overall. It must also re-arm once the condition clears, so
// a later recurrence is not silently suppressed forever.
func TestHintIfShadowedPersistsAcrossProcessesAndRearms(t *testing.T) {
	nvxHome := tempDir(t)
	shimDir := filepath.Join(nvxHome, "bin")
	current := filepath.Join(nvxHome, "current")
	sep := string(os.PathListSeparator)
	shadowedPath := current + sep + shimDir
	healthyPath := shimDir + sep + current

	t.Setenv("PATH", shadowedPath)
	marker := shadowHintMarkerPath(nvxHome)

	if _, err := os.Stat(marker); err == nil {
		t.Fatal("marker should not exist before the first call")
	}
	hintIfShadowed(nvxHome)
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("expected the marker to persist to disk, surviving past this process's lifetime")
	}

	// A second call under the same still-shadowed PATH must not error or
	// duplicate the marker; the guard is "already shown", not "shown exactly
	// once ever".
	hintIfShadowed(nvxHome)
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("marker should remain present while still shadowed")
	}

	// Condition clears (e.g. `nvx doctor` fixed PATH) -> marker must clear too,
	// so a later recurrence is reported again rather than staying suppressed.
	t.Setenv("PATH", healthyPath)
	hintIfShadowed(nvxHome)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("expected the marker to be removed once the condition clears")
	}

	t.Setenv("PATH", shadowedPath)
	hintIfShadowed(nvxHome)
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("expected a recurrence after clearing to be reported (marker re-armed)")
	}
}

// writeExec creates an executable file (0755) so Unix resolution accepts it.
func writeExec(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0755); err != nil { // #nosec G306 -- test fixture
		t.Fatal(err)
	}
}
