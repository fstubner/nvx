//go:build windows

package nvx

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

// Every shape of stdio a contained process can give its child works, through
// spawn and through spawnSync.
//
// A pipe in any slot is a named pipe libuv creates inside the container, and
// the container may not create one. The preload substituted stdout and stderr,
// and stdin only alongside them, so a pipe in stdin with nothing piped after it
// reached the real call. libuv retries a refused pipe name forever, so the whole
// process spun on one core and no timer fired again. Measured 2026-10-07 with
// spawn(node, args, {stdio: ['pipe', 'inherit', 'inherit']}), the shape
// @prisma/client's postinstall uses: the contained `npm install` of a Prisma 6
// project never returned. spawnSync with ['pipe', 'inherit', 'inherit'] or
// ['pipe', 'ignore', 'ignore'] hung the same way.
//
// The 27 shapes of 'pipe', 'inherit' and 'ignore', each one through both APIs.
// A child given a piped stdin echoes what it read, so the stdin channel is
// checked to deliver as well as to start.
func TestEveryStdioShapeCompletesInsideTheSandbox(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (builds nvx and launches a real AppContainer)")
	}
	dir := tempDir(t)
	nvxExe := filepath.Join(dir, "nvx.exe")
	if out, err := exec.Command("go", "build", "-o", nvxExe, "github.com/fstubner/nvx/cmd/nvx").CombinedOutput(); err != nil {
		t.Skipf("cannot build nvx for this test: %v\n%s", err, out)
	}

	const script = `const cp = require('child_process');
const fs = require('fs');
const out = process.argv[2];
const say = m => { try { fs.appendFileSync(out, m + '\n'); } catch (e) {} };
const kinds = ['pipe', 'inherit', 'ignore'];
const shapes = [];
for (const a of kinds) for (const b of kinds) for (const c of kinds) shapes.push([a, b, c]);
const child = 'const tag = process.argv[1];' +
  'const done = s => { process.stdout.write("OUT " + tag + " in=" + s + "\\n"); process.stderr.write("ERR " + tag + "\\n"); };' +
  'if (process.argv[2] !== "1") done("-");' +
  'else { let s = ""; process.stdin.on("data", d => s += d).on("end", () => done(s)); }';
const text = b => (b === null || b === undefined) ? '-' : String(b).trim().replace(/\r?\n/g, '|');
function viaSpawn(i, next) {
  if (i === shapes.length) return next();
  const shape = shapes[i];
  const tag = 'a-' + shape.join('-');
  say('start ' + tag);
  let c;
  try {
    c = cp.spawn(process.execPath, ['-e', child, tag, shape[0] === 'pipe' ? '1' : '0'], { stdio: shape });
  } catch (e) { say('THREW ' + tag + ' ' + e.message); return viaSpawn(i + 1, next); }
  let o = '', e = '';
  if (c.stdout) c.stdout.on('data', d => o += d);
  if (c.stderr) c.stderr.on('data', d => e += d);
  if (shape[0] === 'pipe' && c.stdin) c.stdin.end('IN-' + tag);
  c.on('error', err => say('ERROR ' + tag + ' ' + err.message));
  c.on('close', code => {
    say('close ' + tag + ' code=' + code + ' out=' + (c.stdout ? text(o) : '-') + ' err=' + (c.stderr ? text(e) : '-'));
    viaSpawn(i + 1, next);
  });
}
function viaSpawnSync() {
  for (const shape of shapes) {
    const tag = 's-' + shape.join('-');
    say('start ' + tag);
    try {
      const opts = { stdio: shape };
      if (shape[0] === 'pipe') opts.input = 'IN-' + tag;
      const r = cp.spawnSync(process.execPath, ['-e', child, tag, shape[0] === 'pipe' ? '1' : '0'], opts);
      say('close ' + tag + ' code=' + r.status + ' out=' + text(r.stdout) + ' err=' + text(r.stderr));
    } catch (e) { say('THREW ' + tag + ' ' + e.message); }
  }
}
viaSpawn(0, () => { viaSpawnSync(); say('DONE'); process.exit(0); });
`
	scriptPath := filepath.Join(dir, "shapes.js")
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join(dir, "report.txt")

	// The deadline is out here because the defect stops the script's own timers.
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, nvxExe, "--strict", "shim", "node", scriptPath, reportPath)
	cmd.Dir = dir
	cmd.WaitDelay = 10 * time.Second
	outBytes, _ := cmd.CombinedOutput()
	out := string(outBytes)
	report, _ := os.ReadFile(reportPath)
	got := string(report)
	if ctx.Err() != nil {
		lines := strings.Split(strings.TrimSpace(got), "\n")
		t.Fatalf("the contained process never finished within 120s. The last shape it started is the one "+
			"that wedged it: %s\nnvx said:\n%s", lines[len(lines)-1], out)
	}
	if got == "" {
		failUnlessHostRefusedLaunch(t, out, fmt.Errorf("no report"), "the stdio shapes")
	}
	if !strings.Contains(got, "DONE") {
		t.Fatalf("not every shape finished:\n%s\nnvx said:\n%s", got, out)
	}

	kinds := []string{"pipe", "inherit", "ignore"}
	for _, api := range []string{"a", "s"} {
		for _, in := range kinds {
			for _, o := range kinds {
				for _, e := range kinds {
					tag := strings.Join([]string{api, in, o, e}, "-")
					checkStdioShape(t, tag, in, o, e, got, out)
				}
			}
		}
	}
}

// checkStdioShape asserts one shape's result: the child exited 0, and each of
// its streams went where the shape sent it and nowhere else.
func checkStdioShape(t *testing.T, tag, in, o, e, report, nvxOut string) {
	t.Helper()
	var line string
	for _, l := range strings.Split(report, "\n") {
		if strings.HasPrefix(l, "close "+tag+" ") {
			line = strings.TrimSpace(l)
		}
	}
	if line == "" {
		t.Errorf("%s: no close line in the report", tag)
		return
	}
	if !strings.Contains(line, " code=0 ") {
		t.Errorf("%s: %s", tag, line)
	}
	read := "-"
	if in == "pipe" {
		read = "IN-" + tag
	}
	for _, s := range []struct{ slot, field, marker string }{
		{o, "out=", "OUT " + tag + " in=" + read},
		{e, "err=", "ERR " + tag},
	} {
		inReport := strings.Contains(line, s.field+s.marker)
		inTerminal := strings.Contains(nvxOut, s.marker+"\n") || strings.Contains(nvxOut, s.marker+"\r\n")
		switch s.slot {
		case "pipe":
			if !inReport {
				t.Errorf("%s: the parent did not read %q from its piped child: %s", tag, s.marker, line)
			}
		case "inherit":
			if !inTerminal {
				t.Errorf("%s: %q did not reach the terminal it was inherited from", tag, s.marker)
			}
		case "ignore":
			if inReport || inTerminal {
				t.Errorf("%s: %q was ignored and still arrived", tag, s.marker)
			}
		}
	}
}
