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

// The ninth piped child in one process streams like the first eight.
//
// The pool holds sixteen output streams, so eight children with stdio:'pipe'.
// A ninth is not a ninth CONCURRENT child here: these run one after another,
// each finished before the next starts, so at most one channel pair is ever in
// use. The pool is nowhere near exhausted. What the ninth child draws is a
// RECYCLED name -- the preload hands a channel back to its free list when the
// child that used it closes -- and the Go side used to serve each name exactly
// once. So the ninth child opened a dead pipe, the open threw, and the
// preload's failure path was the raw spawn that blocks inside libuv. A test
// runner that spawns a worker per file, waits, and spawns the next hit this on
// its ninth file, with the same symptom as before the broker existed.
//
// Streamed, not merely delivered: the file fallback also gets every byte
// there, but at stream close rather than as produced, and it says so on
// stderr. Sequential children must never need it, so the warning's presence is
// itself a failure here -- it would mean the recycled name was treated as
// exhausted rather than reused.
func TestTheNinthSequentialPipedChildStillStreams(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (builds nvx and launches a real AppContainer)")
	}
	dir := tempDir(t)
	nvxExe := filepath.Join(dir, "nvx.exe")
	if out, err := exec.Command("go", "build", "-o", nvxExe, ".").CombinedOutput(); err != nil {
		t.Skipf("cannot build nvx for this test: %v\n%s", err, out)
	}

	const script = `const { spawn } = require('child_process');
const fs = require('fs');
const out = process.argv[2];
const say = m => { try { fs.appendFileSync(out, m + '\n'); } catch (e) {} };
const N = 10, LINES = 50;
function one(i, next) {
  let c;
  try {
    c = spawn(process.execPath,
      ['-e', 'for(let j=0;j<' + LINES + ';j++)console.log("c' + i + '-"+j)'],
      { stdio: 'pipe' });
  } catch (e) { say('THREW ' + i + ' ' + e.message); return next(); }
  let buf = '';
  c.stdout.on('data', d => buf += d);
  c.on('close', code => {
    const lines = buf.split('\n').filter(Boolean);
    say('close ' + i + ' code=' + code + ' lines=' + lines.length + ' last=' + lines[lines.length - 1]);
    next();
  });
}
let i = 0;
(function loop() {
  if (i === N) { say('DONE'); process.exit(0); }
  one(i++, loop);
})();
setTimeout(() => { say('HUNG at=' + i); process.exit(9); }, 120000);
`
	scriptPath := filepath.Join(dir, "sequential.js")
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join(dir, "report.txt")

	// The deadline lives out here: when the defect is present the ninth spawn
	// blocks synchronously inside libuv and the script's own timer never fires.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, nvxExe, "--strict", "shim", "node", scriptPath, reportPath)
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	report, _ := os.ReadFile(reportPath)
	got := string(report)
	if ctx.Err() != nil {
		t.Fatalf("ten sequential piped children never finished within 150s. The report shows how far "+
			"it got; a stop at the ninth is a recycled channel name pointing at a dead pipe:\n%s\nnvx said:\n%s",
			strings.TrimSpace(got), out)
	}
	if got == "" {
		failUnlessHostRefusedLaunch(t, string(out), fmt.Errorf("no report"), "the sequential children")
	}
	t.Logf("report:\n%s", strings.TrimSpace(got))

	if strings.Contains(got, "HUNG") || strings.Contains(got, "THREW") {
		t.Fatalf("a sequential piped child failed to start:\n%s", got)
	}
	if !strings.Contains(got, "DONE") {
		t.Fatalf("not every child closed:\n%s", got)
	}
	for i := 0; i < 10; i++ {
		want := fmt.Sprintf("close %d code=0 lines=50 last=c%d-49", i, i)
		if !strings.Contains(got, want) {
			t.Errorf("child %d: wanted %q in:\n%s", i, want, got)
		}
	}
	if strings.Contains(string(out), "more concurrent piped children") {
		t.Error("the fallback warning fired although at most one child was ever running: a recycled " +
			"channel was treated as exhausted instead of reused, so sequential children lose streaming " +
			"after the eighth")
	}
}

// A contained process's own piped child can stream ITS child.
//
// The channel names travel in the environment, so every node process in the
// tree receives the same list and each one used to treat the whole of it as
// its own. A nested process's first pick was the very channel carrying its own
// stdout, already connected; the open was refused as busy, and the preload
// fell through to the raw spawn that hangs. npm running a script that runs
// node that spawns anything -- `npm test` under most runners -- is this shape.
//
// The pipe itself is the arbiter now: a name that will not open is in use by
// some other process in the tree, and the preload moves to the next one.
func TestANestedContainedProcessCanStreamItsOwnChild(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (builds nvx and launches a real AppContainer)")
	}
	dir := tempDir(t)
	nvxExe := filepath.Join(dir, "nvx.exe")
	if out, err := exec.Command("go", "build", "-o", nvxExe, ".").CombinedOutput(); err != nil {
		t.Skipf("cannot build nvx for this test: %v\n%s", err, out)
	}

	// The innermost process: prints a marker so the middle one has something
	// to stream.
	const inner = `for (let j = 0; j < 20; j++) console.log('inner-' + j);`
	// The middle process: a piped child of the outer one, which itself spawns
	// the inner one with pipes and relays what it streamed. Its own stdout is a
	// channel from the same pool it is about to pick from.
	const middle = `const { spawn } = require('child_process');
const c = spawn(process.execPath, ['-e', process.argv[1]], { stdio: 'pipe' });
let buf = '';
c.stdout.on('data', d => buf += d);
c.on('close', code => {
  const lines = buf.split('\n').filter(Boolean);
  console.log('middle-saw code=' + code + ' lines=' + lines.length + ' last=' + lines[lines.length - 1]);
});
`
	const outer = `const { spawn } = require('child_process');
const fs = require('fs');
const out = process.argv[2];
const say = m => { try { fs.appendFileSync(out, m + '\n'); } catch (e) {} };
const m = spawn(process.execPath, ['-e', process.argv[3], process.argv[4]], { stdio: 'pipe' });
let buf = '', err = '';
m.stdout.on('data', d => buf += d);
m.stderr.on('data', d => err += d);
m.on('close', code => {
  say('outer-saw code=' + code);
  say(buf.trim());
  if (err.trim()) say('middle-stderr: ' + err.trim());
  say('DONE');
  process.exit(0);
});
setTimeout(() => { say('HUNG'); process.exit(9); }, 120000);
`
	scriptPath := filepath.Join(dir, "outer.js")
	if err := os.WriteFile(scriptPath, []byte(outer), 0o600); err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join(dir, "report.txt")

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, nvxExe, "--strict", "shim", "node", scriptPath, reportPath, middle, inner)
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	report, _ := os.ReadFile(reportPath)
	got := string(report)
	if ctx.Err() != nil {
		t.Fatalf("the nested pipeline never finished within 150s. The middle process's spawn is the "+
			"one that blocks: its first pick from the pool is the channel carrying its own stdout.\n"+
			"report:\n%s\nnvx said:\n%s", strings.TrimSpace(got), out)
	}
	if got == "" {
		failUnlessHostRefusedLaunch(t, string(out), fmt.Errorf("no report"), "the nested pipeline")
	}
	t.Logf("report:\n%s", strings.TrimSpace(got))

	if strings.Contains(got, "HUNG") {
		t.Fatalf("the nested pipeline hung:\n%s", got)
	}
	if !strings.Contains(got, "DONE") {
		t.Fatalf("the outer process did not finish:\n%s", got)
	}
	for _, want := range []string{
		"outer-saw code=0",
		"middle-saw code=0 lines=20 last=inner-19",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("wanted %q in the report:\n%s", want, got)
		}
	}
	if strings.Contains(string(out), "more concurrent piped children") {
		t.Error("the fallback warning fired for a tree of three processes using two channel pairs; " +
			"a busy channel was treated as an exhausted pool")
	}
}
