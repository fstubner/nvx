package nvx

import (
	"fmt"
	"os"
	"testing"
)

// TestMain exists to drop the uninstrumented probe child built for a -race run.
//
// It is deliberately the only thing here. Tests in this package re-run the test
// binary as a contained child, so anything done before m.Run() runs again inside
// every AppContainer probe -- which is how a global setup step becomes a
// per-probe one nobody meant to write.
func TestMain(m *testing.M) {
	// The host-capability control starts this binary inside an AppContainer to
	// learn whether CreateProcess can start a process there at all. It has to do
	// that silently.
	//
	// The child's stdout and stderr are this test process's own, and `go test`
	// reads them: a child that reached the testing package printed "testing:
	// warning: no tests to run" and a bare "PASS", and cmd/go tagged the whole
	// package "[no tests to run]" on the gate's summary line -- the one line a
	// maintainer reads, made indistinguishable from a run in which nothing
	// executed. Exiting here produces no output at all, and still proves the only
	// thing the control asks: that the process started and ran our code.
	if os.Getenv("NVX_HOST_CONTROL_CHILD") == "1" {
		os.Exit(0)
	}
	// A test that plants a stand-in system tool on PATH points it at this
	// binary. Asked to be that tool, it prints what the test wants and leaves,
	// before the testing package can see the tool's argv as flags.
	if out := os.Getenv("NVX_TEST_FAKE_TOOL_OUTPUT"); out != "" {
		fmt.Print(out)
		os.Exit(0)
	}
	// A test that installs this binary as a stand-in runtime asks it to print
	// the path it was started from. That path names the version the shim chose.
	if os.Getenv("NVX_TEST_FAKE_RUNTIME") == "1" {
		exe, _ := os.Executable()
		fmt.Println(exe)
		os.Exit(0)
	}
	// doctor's sandbox-launch check is off for the suite, and each test that
	// wants an answer supplies its own.
	//
	// The check starts a real process inside a real AppContainer, and doctor runs
	// it on every invocation -- so every test that calls runDoctor to ask about
	// PATH or shims began creating an AppContainer profile, granting ACLs on the
	// staged supervisor and NVX_HOME, launching, and deleting the profile again.
	// Measured here: 7 launches across the doctor tests, none of which is about
	// containment. On the Windows CI runner that suite then failed with a child
	// process panicking on "Failed to load iphlpapi.dll", twice, with no test
	// marked FAIL.
	//
	// The real function is not left unexercised by this: the probe gate runs the
	// same control launch directly (probe_appcontainer_capability_windows_test.go),
	// which is where a test that needs a real AppContainer belongs.
	reportSandboxLaunchFn = func(string) bool { return true }
	// Same reasoning for the elevated-grant check: it reads the machine's real
	// ACLs, so a test that calls runDoctor about PATH would otherwise turn on
	// whatever the last `nvx setup` left on the host running the suite.
	reportSetupGrantsFn = func(string) bool { return true }

	code := m.Run()
	cleanupProbeChildBinary()
	os.Exit(code)
}
