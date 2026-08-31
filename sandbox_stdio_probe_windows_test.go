//go:build windows

package main

// Opt-in end-to-end probe (NVX_PROBE=1): does piped stdout actually reach a real
// AppContainer child through launchAppContainerProcess?
//
// The table test in sandbox_stdio_windows_test.go proves the CreateProcess
// semantics, but nvx launches with EXTENDED_STARTUPINFO_PRESENT and a
// PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITIES attribute list, and handle
// inheritance interacting with that combination is exactly the sort of thing that
// looks right and isn't. This drives the real launcher.
//
// Gated because it creates and deletes an AppContainer profile and mutates ACLs.

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

var procSetStdHandleTest = modKernel32.NewProc("SetStdHandle")

func TestPipedStdioReachesRealAppContainerChild(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (creates a throwaway AppContainer profile)")
	}

	const probeProfile = "nvx.sandbox.stdioprobe"
	sid, err := ensureAppContainerSID(probeProfile)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	defer deleteAppContainerProfile(probeProfile)

	guestHome := tempDir(t)
	workDir := tempDir(t)
	scopeCaps, err := prepareAppContainerFilesystem(sid, "", guestHome, workDir)
	if err != nil {
		t.Fatalf("filesystem prep: %v", err)
	}

	sysRoot := os.Getenv("SystemRoot")
	if sysRoot == "" {
		sysRoot = `C:\Windows`
	}
	cmdExe := sysRoot + `\System32\cmd.exe`
	if err := grantAppContainerPathReadExec(sid, cmdExe); err != nil {
		t.Skipf("cannot grant the container access to cmd.exe: %v", err)
	}

	read, write := makeTestPipe(t)
	defer syscall.CloseHandle(read)

	// launchAppContainerProcess reads the process's own standard handles, so
	// redirect this process's stdout at the Win32 level for the duration.
	prevOut, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	if err != nil {
		t.Fatalf("GetStdHandle: %v", err)
	}
	// STD_OUTPUT_HANDLE is -11; the Win32 API takes it as a DWORD.
	const stdOutputHandle = uintptr(0xFFFFFFF5)
	setStd := func(h syscall.Handle) {
		procSetStdHandleTest.Call(stdOutputHandle, uintptr(h))
	}
	setStd(write)

	exitCode, launchErr := launchAppContainerProcess(
		cmdExe,
		[]string{"/c", "echo", "F46_E2E_MARKER"},
		scrubEnvironment(guestHome),
		workDir,
		sid,
		0,
		append(scopeCaps, capabilityInternetClientSID),
	)

	setStd(prevOut)
	syscall.CloseHandle(write)

	got := readWithTimeout(t, read)

	requireAppContainerLaunch(t, launchErr)
	if exitCode != 0 {
		t.Errorf("child exit code = %d, want 0", exitCode)
	}
	if !strings.Contains(got, "F46_E2E_MARKER") {
		t.Errorf("piped stdout did not reach the AppContainer child; got %q", got)
	} else {
		t.Logf("marker received through the pipe: %q", strings.TrimSpace(got))
	}

}

// stageProbeChild copies the test binary into the guest home so the container can
// execute it without a grant on the Go build cache, and returns its path.
//
// Every AppContainer probe needs this and each had its own three-line copy that
// called t.Fatal on failure. That turned a runner defect into a red build: on
// GitHub-hosted Windows runners, reading the test binary intermittently fails with
// "The handle is invalid" -- four times in two days, on four different tests, each
// clearing on a plain rerun with no code change. Staging the child is setup, not
// the thing under test, so a failure here means the probe could not run, which is
// a skip. The same judgement requireAppContainerLaunch already applies to a host
// that refuses to create AppContainers.
//
// Deliberately narrow: only the copy is forgiven. Anything the probe then asserts
// still fails loudly, because a probe that skips its way past a real regression is
// worse than no probe.
func stageProbeChild(t *testing.T, guestHome, name string) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Skipf("cannot locate the test binary to stage as the contained child: %v", err)
	}
	// Retried before giving up. This read fails intermittently with "The handle is
	// invalid" -- on hosted runners, and on a developer machine under the load of
	// the full probe suite -- and skipping on the first failure means a SECURITY
	// probe quietly does not run while the gate still reports success. Measured on
	// two separate acceptance passes, each time a different test: once the
	// deny-ACE probe, once the cross-session one, and each time the run reported
	// "0 failures" with a containment assertion that had never executed.
	//
	// Copying the binary is not the thing under test, so a transient failure to do
	// it is worth retrying. Everything the probe then asserts still fails loudly:
	// only the copy is forgiven, and only briefly.
	var data []byte
	for attempt := 0; attempt < 5; attempt++ {
		if data, err = os.ReadFile(self); err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		t.Skipf("cannot read the test binary to stage as the contained child after 5 attempts (%v); "+
			"a containment assertion is NOT being checked in this run", err)
	}
	childExe := filepath.Join(guestHome, name)
	if err := os.WriteFile(childExe, data, 0o700); err != nil {
		t.Skipf("cannot write the contained child into the guest home: %v", err)
	}
	return childExe
}

// requireAppContainerLaunch decides whether a failed AppContainer launch is this
// environment's limitation or a real defect.
//
// GitHub-hosted Windows runners cannot create AppContainer children at all:
// CreateProcess returns "Access is denied" for every executable, including
// C:\Windows\System32\cmd.exe. That was long asserted in the smoke scripts as a
// blanket `exit 0` on CI; running these probes there in CI run 32077425413
// confirmed it, so it is now measured rather than assumed.
//
// A skip is therefore correct on such a host -- but only for THAT error. Skipping on
// any launch failure, which two of these probes previously did, would silently
// swallow a genuine regression in the launcher. Anything else is a failure.
func requireAppContainerLaunch(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	// Two refusal shapes, not one. A host that cannot create AppContainer
	// children reports either "Access is denied" or "The system cannot find the
	// file specified" -- the second seen on GitHub-hosted runners launching
	// C:\Windows\System32\cmd.exe, a file that self-evidently exists, and also
	// locally as one of the reverse-relay prototype's two flaky failure modes.
	// Matching only the first left eight probes failing where they should have
	// skipped, and kept Windows CI red.
	//
	// Narrow on purpose: this is a launch error from CreateProcess for an
	// AppContainer, where a missing-file result cannot be about the executable
	// path. If a probe ever stages an executable that genuinely is not there,
	// this would hide it -- so the skip message says which shape it matched,
	// rather than reporting a uniform "cannot run here".
	msg := err.Error()
	if strings.Contains(msg, "Access is denied") ||
		strings.Contains(msg, "The system cannot find the file specified") {
		t.Skipf("this host cannot create AppContainer children (%v); GitHub-hosted Windows runners are known to refuse, so the probe cannot run here", err)
	}
	t.Fatalf("AppContainer launch failed for a reason other than the host refusing it: %v", err)
}
