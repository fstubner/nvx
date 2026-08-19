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
	"strings"
	"syscall"
	"testing"
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

	guestHome := t.TempDir()
	workDir := t.TempDir()
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
	if strings.Contains(err.Error(), "Access is denied") {
		t.Skipf("this host cannot create AppContainer children (%v); GitHub-hosted Windows runners are known to refuse, so the probe cannot run here", err)
	}
	t.Fatalf("AppContainer launch failed for a reason other than the host refusing it: %v", err)
}
