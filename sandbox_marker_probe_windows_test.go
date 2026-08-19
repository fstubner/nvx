//go:build windows

package main

// Opt-in probe (NVX_PROBE=1) for the other half of the F25 check.
//
// The unit tests prove containmentDisproved() catches a forged marker OUTSIDE a
// sandbox. This proves the far more dangerous direction: that it stays silent
// INSIDE one. If it reported "not contained" from within a real AppContainer, every
// nested nvx -- npm running node running an nvx shim -- would try to sandbox itself
// again, which is a functional break rather than a security one.
//
// A unit test cannot settle it: it depends on the AppContainer's actual ACLs, and on
// realHomeDir resolving the real profile rather than the redirected guest home.

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestContainmentNotDisprovedInsideRealAppContainer(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (creates a throwaway AppContainer profile)")
	}

	// Child role: report what the check says from inside the container.
	if os.Getenv("NVX_MARKER_CHECK_CHILD") == "1" {
		if containmentDisproved() {
			os.Stdout.WriteString("containment=DISPROVED\n")
		} else {
			os.Stdout.WriteString("containment=NOT_DISPROVED\n")
		}
		os.Exit(0)
	}

	const probeProfile = "nvx.sandbox.markerprobe"
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

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	childExe := filepath.Join(guestHome, "markerprobe.exe")
	if err := os.WriteFile(childExe, data, 0o700); err != nil {
		t.Fatal(err)
	}

	read, write := makeTestPipe(t)
	defer syscall.CloseHandle(read)
	prevOut, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	const stdOutputHandle = uintptr(0xFFFFFFF5)
	procSetStdHandleTest.Call(stdOutputHandle, uintptr(write))

	// scrubEnvironment redirects HOME/USERPROFILE to the guest home, exactly as a
	// real sandboxed launch does -- which is the condition realHomeDir must survive.
	env := append(scrubEnvironment(guestHome),
		"NVX_PROBE=1",
		"NVX_MARKER_CHECK_CHILD=1",
	)
	exitCode, launchErr := launchAppContainerProcess(
		childExe,
		[]string{"-test.run=TestContainmentNotDisprovedInsideRealAppContainer"},
		env, workDir, sid, 0,
		scopeCaps,
	)

	procSetStdHandleTest.Call(stdOutputHandle, uintptr(prevOut))
	syscall.CloseHandle(write)
	out := readWithTimeout(t, read)

	t.Logf("child exit=%d err=%v output=%q", exitCode, launchErr, out)
	switch {
	case launchErr != nil:
		t.Skipf("could not launch the contained probe: %v", launchErr)
	case contains(out, "containment=NOT_DISPROVED"):
		t.Log("correct: inside a real AppContainer the check does not claim to disprove containment, so a legitimate nested nvx still skips re-sandboxing")
	case contains(out, "containment=DISPROVED"):
		t.Error("the check disproved containment from INSIDE a real AppContainer; every nested nvx would try to sandbox itself again")
	default:
		t.Errorf("inconclusive child output %q -- the probe must produce a verdict", out)
	}
}
