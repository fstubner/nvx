//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A contained process can write to a child's stdin, in a real AppContainer.
//
// It could not until 2026-09-04. An AppContainer refuses CreateNamedPipeW, and
// Windows builds piped child stdio out of named pipes, so nvx substituted an
// empty file for slot 0: the child read EOF instead of the process hanging, and
// child.stdin was null. That was documented as "a tool that feeds its child
// input needs --no-sandbox", which read like a corner case.
//
// It is not a corner case. esbuild's service is a child driven over stdin, vite
// runs on esbuild, and vitest runs on vite -- so `npx vitest run` against an
// already-installed binary hung indefinitely, measured at over 120s against 4.1s
// uncontained, with nothing printed to explain it.
//
// Asserted through a real contained run rather than a unit test, because every
// part of this is a Windows access check: whether the pipe can be opened, whether
// the handle survives being handed to a grandchild, and whether closing one end
// is seen as EOF at the other. None of that is observable from Go alone.
func TestAContainedProcessCanWriteToItsChildStdin(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (builds nvx and launches a real AppContainer)")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed; the contained program needs it")
	}

	proj := tempDir(t)
	nvxExe := filepath.Join(tempDir(t), "nvx.exe")
	if out, err := exec.Command("go", "build", "-o", nvxExe, ".").CombinedOutput(); err != nil {
		t.Fatalf("build nvx: %v\n%s", err, out)
	}

	// The child echoes stdin back to stdout, so one run proves the whole path:
	// the write reaches the child, and the reply comes back up the output
	// channel. A test that only checked child.stdin was non-null would pass on a
	// stream that quietly discarded everything -- which is the failure mode the
	// old comment explicitly preferred null to.
	probe := `
const cp = require('node:child_process');
const child = cp.spawn(process.execPath, ['-e', 'process.stdin.pipe(process.stdout)'],
  { stdio: ['pipe', 'pipe', 'inherit'] });
if (child.stdin === null) { console.log('RESULT stdin-null'); process.exit(1); }
let got = '';
child.stdout.on('data', d => { got += d; });
child.stdout.on('end', () => { console.log('RESULT ' + got.trim()); process.exit(0); });
child.stdin.end('round-trip-ok\n');
setTimeout(() => { console.log('RESULT timed-out'); process.exit(1); }, 30000);
`
	if err := os.WriteFile(filepath.Join(proj, "probe.cjs"), []byte(probe), 0o600); err != nil {
		t.Fatal(err)
	}

	// --strict so a plain node script is contained; without it this runs as "your
	// own code" and proves nothing about the sandbox.
	cmd := exec.Command(nvxExe, "--strict", "shim", "node", "probe.cjs")
	cmd.Dir = proj
	cmd.Env = append(os.Environ(), "NVX_TRACE=")
	out, err := cmd.CombinedOutput()
	got := string(out)
	requireContainedRunLaunched(t, got)

	switch {
	case strings.Contains(got, "RESULT round-trip-ok"):
		// what we want
	case strings.Contains(got, "RESULT stdin-null"):
		t.Fatalf("child.stdin was null in a contained process: the reverse channel was not used.\n%s", got)
	case strings.Contains(got, "RESULT timed-out"):
		t.Fatalf("the write reached nothing and the child never answered -- a hang, which is worse than "+
			"the null it replaced.\n%s", got)
	default:
		t.Fatalf("the probe produced no result (%v):\n%s", err, got)
	}
}

// A fork fails immediately and says what to do, instead of hanging forever.
//
// An IPC channel is a named pipe libuv creates inside the container, and an
// AppContainer refuses to create one. Unlike the other three streams it cannot
// be handed over ready-made, so the limitation stands -- but what it used to do
// was hang inside fork() before the child existed and before anything printed.
// Measured with file markers: the line before fork() is written and the line
// after it never is.
//
// The session that reported this watched `npx vitest run` sit for over five
// minutes, twice, with nothing on screen. The timing assertion is the point of
// this test: a correct message that took five minutes to arrive would still be
// the bug.
func TestAContainedForkFailsFastInsteadOfHanging(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (builds nvx and launches a real AppContainer)")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed; the contained program needs it")
	}

	proj := tempDir(t)
	nvxExe := filepath.Join(tempDir(t), "nvx.exe")
	if out, err := exec.Command("go", "build", "-o", nvxExe, ".").CombinedOutput(); err != nil {
		t.Fatalf("build nvx: %v\n%s", err, out)
	}

	probe := `
const cp = require('node:child_process');
try {
  cp.fork(__filename, [], { stdio: ['ignore','ignore','ignore','ipc'] });
  console.log('RESULT forked-without-error');
} catch (e) {
  console.log('RESULT threw ' + (e.code || '') + ' :: ' + e.message);
}
`
	if err := os.WriteFile(filepath.Join(proj, "forkprobe.cjs"), []byte(probe), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(nvxExe, "--strict", "shim", "node", "forkprobe.cjs")
	cmd.Dir = proj
	cmd.Env = append(os.Environ(), "NVX_TRACE=")

	start := time.Now()
	out, _ := cmd.CombinedOutput()
	elapsed := time.Since(start)
	got := string(out)
	requireContainedRunLaunched(t, got)

	if !strings.Contains(got, "RESULT threw") {
		t.Fatalf("a contained fork did not fail with an explanation after %s:\n%s", elapsed, got)
	}
	// The message has to name the way out. "Not supported" that does not say
	// what to run instead sends the reader back to guessing, which is where
	// the reporting session already was.
	if !strings.Contains(got, "--no-sandbox") {
		t.Errorf("the failure does not name the flag that works:\n%s", got)
	}
	// Generous, because it shares a machine with the rest of the suite. The
	// behaviour being pinned is "fails" rather than "hangs", and the old one
	// never returned at all.
	if elapsed > 60*time.Second {
		t.Errorf("the contained fork took %s to fail; it is supposed to fail immediately", elapsed)
	}
}
