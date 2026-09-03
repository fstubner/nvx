//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Streaming a child's output from inside the sandbox, driven through the real
// binary.
//
// `spawn(cmd, {stdio: 'pipe'})` used to block forever in a contained process:
// Windows builds piped stdio out of named pipes, an AppContainer cannot create
// one, and libuv waits before the child exists. It is the limitation that
// stranded 17 processes on the development machine -- `npx vitest`, `npx
// playwright`, `npm install` -- some blocked for 13 hours.
//
// Everything below was verified by hand first. This exists so it stays verified:
// the fix spans a Go pipe broker, an environment variable, and a JavaScript
// preload, and no unit test on any one of those three would notice the others
// drifting. It runs the real nvx against real node in a real AppContainer for
// that reason.
//
// NVX_PROBE-gated like the other sandbox probes, so a plain `go test ./...`
// stays fast; CI sets it and runs everything.
func TestContainedProcessCanStreamAChildsOutput(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (builds nvx and launches a real AppContainer)")
	}

	dir := tempDir(t)
	nvxExe := filepath.Join(dir, "nvx.exe")
	if out, err := exec.Command("go", "build", "-o", nvxExe, ".").CombinedOutput(); err != nil {
		t.Skipf("cannot build nvx for this test: %v\n%s", err, out)
	}

	// Asserted on inside the container and reported through a file, because the
	// contained process's own stdout is not what is under test here and mixing
	// the two is how an earlier by-hand run read as a silent failure.
	const script = `const { spawn } = require('child_process');
const fs = require('fs');
const out = process.argv[2];
const say = m => { try { fs.appendFileSync(out, m + '\n'); } catch (e) {} };
const N = 500;
try {
  const c = spawn(process.execPath,
    ['-e', 'for(let i=0;i<' + N + ';i++)console.log("line"+i); console.error("on-stderr")'],
    { stdio: 'pipe' });
  if (!c.stdout) { say('NO-STDOUT-STREAM'); process.exit(0); }
  let got = '', err = '';
  c.stdout.on('data', d => got += d);
  c.stderr.on('data', d => err += d);
  c.on('error', e => say('SPAWN-ERROR ' + e.message));
  c.on('close', code => {
    const lines = got.split('\n').filter(Boolean);
    say('code=' + code);
    say('lines=' + lines.length);
    say('first=' + lines[0]);
    say('last=' + lines[lines.length - 1]);
    say('stderr=' + err.trim());
    say('DONE');
    process.exit(0);
  });
} catch (e) { say('THREW ' + e.message); process.exit(0); }
setTimeout(() => { say('HUNG'); process.exit(9); }, 60000);
`
	scriptPath := filepath.Join(dir, "stream.js")
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join(dir, "report.txt")

	// The deadline belongs out here, not in the script. When this defect is
	// present, spawn blocks SYNCHRONOUSLY inside libuv, so the script's own
	// setTimeout is never registered and it cannot report anything -- verified
	// by disabling the channels, where the test ran 400 seconds and died on the
	// package timeout instead of failing legibly.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// --strict so ordinary `node` is contained; the limitation is a property of
	// containment, not of which command triggers it.
	cmd := exec.CommandContext(ctx, nvxExe, "--strict", "shim", "node", scriptPath, reportPath)
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("the contained command never finished within 90s. That is the original defect: "+
			"a contained spawn with stdio:'pipe' blocks before the child exists, and every such "+
			"command strands a process forever.\nnvx said:\n%s", out)
	}

	report, err := os.ReadFile(reportPath)
	if err != nil {
		failUnlessHostRefusedLaunch(t, string(out), err, "the contained process")
	}
	got := string(report)
	t.Logf("contained process reported:\n%s", strings.TrimSpace(got))

	if strings.Contains(got, "HUNG") {
		t.Fatal("a contained spawn with stdio:'pipe' hung. This is the original defect: " +
			"every such command strands a process forever.")
	}
	if strings.Contains(got, "NO-STDOUT-STREAM") {
		t.Fatal("child.stdout was null, so the preload substituted descriptors without attaching " +
			"a readable side; callers doing child.stdout.on('data') get nothing")
	}
	if !strings.Contains(got, "DONE") {
		t.Fatalf("the contained process did not finish:\n%s", got)
	}
	for _, want := range []string{
		"code=0",
		"lines=500",   // every line, not merely some
		"first=line0", // in order from the start
		"last=line499",
		"stderr=on-stderr", // stderr is its own channel, not merged into stdout
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in the contained report:\n%s", want, got)
		}
	}
}

// Past the channel pool, output must still all arrive -- and the test says WHEN.
//
// Acceptance found the docs claiming delivery "at exit" while the fallback
// replays on stream close. A caller that accumulates via 'data' and reads its
// buffer in the child's exit handler therefore sees it full for the first
// children and empty for the rest, inside one process, with exit code 0. That
// reads as a flaky test rather than a limit being crossed.
//
// The timing cannot be fixed -- the fallback only learns the child finished when
// exit fires, and a stream write emits on the next tick -- so it is pinned here
// instead, along with the guarantee that actually holds: nothing is dropped.
func TestOutputPastTheChannelPoolArrivesCompleteOnClose(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (builds nvx and launches a real AppContainer)")
	}

	dir := tempDir(t)
	nvxExe := filepath.Join(dir, "nvx.exe")
	if out, err := exec.Command("go", "build", "-o", nvxExe, ".").CombinedOutput(); err != nil {
		t.Skipf("cannot build nvx for this test: %v\n%s", err, out)
	}

	// 12 children, comfortably past the 8 the pool can stream.
	const script = `const { spawn } = require('child_process');
const fs = require('fs');
const out = process.argv[2];
const say = m => { try { fs.appendFileSync(out, m + '\n'); } catch (e) {} };
const N = 12, LINES = 200;
let closed = 0;
for (let i = 0; i < N; i++) {
  const c = spawn(process.execPath,
    ['-e', 'for(let j=0;j<' + LINES + ';j++)console.log("c' + i + '-"+j)'],
    { stdio: 'pipe' });
  let buf = '';
  c.stdout.on('data', d => buf += d);
  c.on('exit', () => say('exit ' + i + ' lines=' + buf.split('\n').filter(Boolean).length));
  c.on('close', () => {
    const lines = buf.split('\n').filter(Boolean);
    say('close ' + i + ' lines=' + lines.length + ' last=' + lines[lines.length - 1]);
    if (++closed === N) { say('DONE'); process.exit(0); }
  });
}
setTimeout(() => { say('HUNG closed=' + closed); process.exit(9); }, 90000);
`
	scriptPath := filepath.Join(dir, "many.js")
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join(dir, "report.txt")

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, nvxExe, "--strict", "shim", "node", scriptPath, reportPath)
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("12 concurrent piped children never finished:\n%s", out)
	}

	report, err := os.ReadFile(reportPath)
	if err != nil {
		failUnlessHostRefusedLaunch(t, string(out), err, "the 12 concurrent piped children")
	}
	got := string(report)
	t.Logf("report:\n%s", strings.TrimSpace(got))

	if !strings.Contains(got, "DONE") {
		t.Fatalf("not every child closed:\n%s", got)
	}
	// The guarantee: by close, every child delivered every line, in order.
	for i := 0; i < 12; i++ {
		want := fmt.Sprintf("close %d lines=200 last=c%d-199", i, i)
		if !strings.Contains(got, want) {
			t.Errorf("child %d lost output past the pool; wanted %q in:\n%s", i, want, got)
		}
	}
	// And the documented caveat: the later children have nothing yet at exit.
	// Asserted so that if this ever becomes deliverable at exit, the docs and
	// the warning that describe it are updated rather than left stale.
	if !strings.Contains(got, "exit 11 lines=0") {
		t.Log("NOTE: the last child now has output at 'exit'. If that is reliable, README, " +
			"SECURITY.md, CHANGELOG and the preload's warning all describe a limit that no " +
			"longer exists and should be corrected.")
	}
	if !strings.Contains(string(out), "more concurrent piped children") {
		t.Error("crossing the pool limit printed no warning; the difference in behaviour between " +
			"the first children and the rest is exactly what must not be silent")
	}
}

// failUnlessHostRefusedLaunch decides what a missing report from a contained
// process means.
//
// Both callers used to skip whenever the report file was absent, for any
// reason at all. That is the failure mode requireAppContainerLaunch exists to
// prevent, applied to a different signal: these two guard the streaming-stdio
// fix, and if that fix regressed the contained process would write no report
// and the test would report a skip. The thing they were written to catch --
// `npx vitest` stranding processes forever -- would come back silently.
//
// nvx prints "AppContainer launch failed" when the host refuses the launch, and
// a hosted Windows runner refuses every one. That, and only that, is a skip.
func failUnlessHostRefusedLaunch(t *testing.T, out string, readErr error, what string) {
	t.Helper()
	if strings.Contains(out, "AppContainer launch failed") {
		t.Skipf("this host cannot create AppContainer children, so %s could not run: %v", what, readErr)
	}
	t.Fatalf("%s wrote no report although the sandbox launched, so this is the feature and not the "+
		"host: %v\nnvx said:\n%s", what, readErr, out)
}
