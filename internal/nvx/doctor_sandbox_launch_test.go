package nvx

import (
	"os"
	"path/filepath"
	"testing"
)

// A sandbox that cannot start makes doctor unhealthy.
//
// This is the wiring, and the wiring is the whole point. Measured 2026-09-20
// on a host that refused every AppContainer launch: doctor checked the shims
// and PATH, found them correct, printed "nvx is intercepting commands
// correctly" and exited 0, while every contained install died on
// "CreateProcess(AppContainer): Access is denied". Interception was genuinely
// fine. Nothing doctor looked at was about whether the sandbox could run.
//
// The launch itself needs a real host and is not faked here. What is pinned is
// that its answer reaches the verdict: a false must cost the exit code, or the
// check is a line of output nobody acts on.
func TestASandboxThatCannotStartMakesDoctorUnhealthy(t *testing.T) {
	home := t.TempDir()
	seedDoctorShims(t, home)

	restore := reportSandboxLaunchFn
	t.Cleanup(func() { reportSandboxLaunchFn = restore })

	reportSandboxLaunchFn = func(string) bool { return true }
	healthy := runDoctorQuietly(t, home)

	reportSandboxLaunchFn = func(string) bool { return false }
	broken := runDoctorQuietly(t, home)

	if broken == healthy {
		t.Fatalf("doctor returned %d whether the sandbox could start or not; a host whose sandbox "+
			"cannot launch would be told everything is correct", broken)
	}
	if broken == 0 {
		t.Fatalf("doctor exited 0 with a sandbox that cannot start; every contained command on " +
			"that host fails and the exit code says otherwise")
	}
}

// runDoctorQuietly runs doctor with stdout discarded and returns its exit code.
func runDoctorQuietly(t *testing.T, home string) int {
	t.Helper()
	realStdout := os.Stdout
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	os.Stdout = devNull
	defer func() {
		os.Stdout = realStdout
		devNull.Close()
	}()
	return runDoctor(home, false)
}

// seedDoctorShims puts the shim files doctor looks for in place, so the run
// under test turns on the sandbox answer rather than on a missing shim.
func seedDoctorShims(t *testing.T, home string) {
	t.Helper()
	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range coreShimCommands() {
		for _, name := range []string{cmd, cmd + ".exe"} {
			if err := os.WriteFile(filepath.Join(binDir, name), []byte("shim"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
