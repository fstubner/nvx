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
	projectA := t.TempDir()
	secretA := filepath.Join(projectA, "src-and-secrets.txt")
	if err := os.WriteFile(secretA, []byte("OTHER-PROJECT-SOURCE"), 0o600); err != nil {
		t.Fatal(err)
	}
	homeA, err := os.MkdirTemp("", "nvxa")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(homeA)
	if err := prepareAppContainerFilesystem(sid, homeA, projectA); err != nil {
		t.Fatalf("project A session: %v", err)
	}

	// Project B: an unrelated project, where the user now runs `npm install`.
	projectB := t.TempDir()
	homeB, err := os.MkdirTemp("", "nvxb")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(homeB)
	if err := prepareAppContainerFilesystem(sid, homeB, projectB); err != nil {
		t.Fatalf("project B session: %v", err)
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	childExe := filepath.Join(homeB, "probe.exe")
	if err := os.WriteFile(childExe, data, 0o700); err != nil {
		t.Fatal(err)
	}

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
		env, projectB, sid, 0, nil)

	procSetStdHandleTest.Call(stdOutputHandle, uintptr(prevOut))
	syscall.CloseHandle(write)
	got := readProbeOutput(t, read)

	requireAppContainerLaunch(t, launchErr)
	t.Logf("child output:\n%s", got)

	// This pins the CURRENT, UNWANTED state rather than the state we want.
	//
	// Measured 2026-08-18: an install in project B both read and wrote project A.
	// The cause is structural, not a slip -- `prepareAppContainerFilesystem` grants
	// (M) on the working directory, the AppContainer profile is stable by design so
	// every session is the same SID, and nothing revokes the ACE. Closing it needs a
	// different containment identity per session, which is a design change rather
	// than a patch, and each obvious variant has its own cost (a per-session SID
	// leaves a dead ACE on the project directory after every run).
	//
	// README.md has been corrected to say so. If this test ever fails because the
	// access is gone, that is good news: update README.md, SECURITY.md,
	// docs/enforcement-matrix.md and this test together.
	if !strings.Contains(got, "READ=OK") {
		t.Error("project A is no longer readable from a project B session -- the documented limitation " +
			"no longer holds, so update README.md, SECURITY.md, docs/enforcement-matrix.md and this test")
	}
	if !strings.Contains(got, "WRITE=OK") {
		t.Error("project A is no longer writable from a project B session -- update the documents named above and this test")
	}
	if !strings.Contains(got, "READ=") || !strings.Contains(got, "WRITE=") {
		t.Errorf("inconclusive result:\n%s", got)
	}
	t.Log("CONFIRMED (unwanted): a sandboxed install reads and writes other projects nvx has previously run in")
}
