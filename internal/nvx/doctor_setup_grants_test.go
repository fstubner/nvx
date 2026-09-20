package nvx

import (
	"testing"
)

// Grants that `nvx setup` made and that are no longer there make doctor unhealthy.
//
// The wiring, for the same reason as TestASandboxThatCannotStartMakesDoctorUnhealthy:
// reading the machine's real ACLs needs a real machine, and one whose elevated
// grants are missing on demand cannot be arranged in a test. What is pinned is
// that the answer reaches the verdict.
//
// Worth pinning because the failure it reports is silent from both ends. Setup
// reports success because it wrote the ACE; the launch reports "Access is denied"
// naming no path, or the package manager reports its own EPERM and never mentions
// nvx. Measured 2026-08-30 on an account whose windows-setup.json recorded an
// older identity: `npx` died inside npm's dependency walker and nvx said nothing.
func TestMissingSetupGrantsMakeDoctorUnhealthy(t *testing.T) {
	home := t.TempDir()
	seedDoctorShims(t, home)

	restore := reportSetupGrantsFn
	t.Cleanup(func() { reportSetupGrantsFn = restore })

	reportSetupGrantsFn = func(string) bool { return true }
	healthy := runDoctorQuietly(t, home)

	reportSetupGrantsFn = func(string) bool { return false }
	broken := runDoctorQuietly(t, home)

	if broken == healthy {
		t.Fatalf("doctor returned %d whether setup's grants were in place or not; a machine "+
			"whose contained commands all fail on a missing traverse grant would be told "+
			"everything is correct", broken)
	}
	if broken == 0 {
		t.Fatal("doctor exited 0 with setup's grants missing; commands under those paths " +
			"cannot start and the exit code says otherwise")
	}
}
