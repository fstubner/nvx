//go:build windows

package main

// Adversarial probe (NVX_PROBE=1) against the README's headline claim:
// "What it cannot reach: ... every other project on disk."
//
// prepareAppContainerFilesystem grants the working directory (OI)(CI)(M) to the
// AppContainer SID, that SID is stable across every session, and nothing ever
// revokes the ACE. So the grant nvx adds while installing in project A is still
// present, and still satisfied by the same SID, when it later runs in project B.
//
// If that reads as written, running nvx once in a project opens it permanently to
// every future sandboxed command -- read AND write, since the grant is (M). The
// earlier containment probe only covered the real home directory, so this half of
// the claim was never exercised.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestSandboxCannotReachOtherProjects(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run")
	}

	if os.Getenv("NVX_OTHER_PROJECT_CHILD") == "1" {
		target := os.Getenv("NVX_PROBE_TARGET")
		if b, err := os.ReadFile(target); err != nil {
			fmt.Printf("READ=DENIED\n")
		} else {
			fmt.Printf("READ=OK:%s\n", strings.TrimSpace(string(b)))
		}
		if err := os.WriteFile(target+".tampered", []byte("x"), 0o600); err != nil {
			fmt.Printf("WRITE=DENIED\n")
		} else {
			fmt.Printf("WRITE=OK\n")
		}
		os.Exit(0)
	}

	const probeProfile = "nvx.sandbox.otherproject"
	sid, err := ensureAppContainerSID(probeProfile)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	defer deleteAppContainerProfile(probeProfile)

	// Project A: nvx ran here once, at some point in the past.
	//
	// A real project root, not a bare temp dir: the scope nvx derives comes from
	// findProjectRoot walking up, so without a manifest here both projects below
	// resolve to whatever package.json happens to exist above %TEMP% and share one
	// identity. See sandbox_fixture_project_windows_test.go.
	projectA := fixtureProjectDir(t)
	secretA := filepath.Join(projectA, "src-and-secrets.txt")
	if err := os.WriteFile(secretA, []byte("OTHER-PROJECT-SOURCE"), 0o600); err != nil {
		t.Fatal(err)
	}
	homeA, err := os.MkdirTemp("", "nvxa")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(homeA)
	scopeCaps, _, err := prepareAppContainerFilesystem(sid, "", homeA, projectA)
	if err != nil {
		t.Fatalf("project A session: %v", err)
	}

	// Project B: an unrelated project, where the user now runs `npm install`.
	projectB := fixtureProjectDir(t)
	homeB, err := os.MkdirTemp("", "nvxb")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(homeB)
	scopeCaps, _, err = prepareAppContainerFilesystem(sid, "", homeB, projectB)
	if err != nil {
		t.Fatalf("project B session: %v", err)
	}

	childExe := stageProbeChild(t, homeB, "probe.exe")

	read, write := makeTestPipe(t)
	defer syscall.CloseHandle(read)
	prevOut, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	const stdOutputHandle = uintptr(0xFFFFFFF5)
	procSetStdHandleTest.Call(stdOutputHandle, uintptr(write))

	env := append(scrubEnvironment(homeB),
		"NVX_PROBE=1", "NVX_OTHER_PROJECT_CHILD=1",
		"NVX_PROBE_TARGET="+secretA,
	)
	_, launchErr := launchAppContainerProcess(childExe,
		[]string{"-test.run=TestSandboxCannotReachOtherProjects"},
		env, projectB, sid, 0, scopeCaps)

	procSetStdHandleTest.Call(stdOutputHandle, uintptr(prevOut))
	syscall.CloseHandle(write)
	got := readProbeOutput(t, read)

	requireAppContainerLaunch(t, launchErr)
	t.Logf("child output:\n%s", got)

	// Measured 2026-08-18 BEFORE the per-project identity landed: both READ and
	// WRITE succeeded. The working directory was granted to the shared package SID,
	// which every later session also held, so one run in a project opened it to all
	// of them. It is now granted to a capability derived from the project, and a
	// session elsewhere does not hold it.
	if strings.Contains(got, "READ=OK") {
		t.Errorf("an install in project B read project A's files, contradicting README.md.\n"+
			"The working directory must be granted to this project's own capability, not to an "+
			"identity every session shares.\n%s", got)
	}
	if strings.Contains(got, "WRITE=OK") {
		t.Errorf("an install in project B WROTE into project A.\n%s", got)
	}
	if !strings.Contains(got, "READ=DENIED") || !strings.Contains(got, "WRITE=DENIED") {
		t.Errorf("inconclusive result:\n%s", got)
	}
}
