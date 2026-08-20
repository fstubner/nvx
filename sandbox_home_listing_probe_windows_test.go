//go:build windows

package main

// Probe (NVX_PROBE=1) for F77: a contained process could enumerate the names in
// %USERPROFILE%.
//
// The ancestor walk grants traverse rights so tools can stat their way up to a
// project root. It granted `(RX)`, which on Windows includes RD -- list folder /
// read data -- so for a project inside the user profile the sandbox could list
// `.ssh`, `.aws`, `.1password` and the rest. Contents stayed denied, but the
// listing alone tells an attacker which credential stores exist, and
// docs/enforcement-matrix.md claimed the sandbox walks through a parent "without
// reading what else is inside it".
//
// Both halves are asserted together on purpose: narrowing to `(X,RA)` is only
// correct if statting an ancestor still works, which is what the grant exists for.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestContainedProcessCannotListTheHomeDirectory(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run")
	}

	if os.Getenv("NVX_LISTING_CHILD") == "1" {
		home := os.Getenv("NVX_PROBE_HOME")
		if entries, err := os.ReadDir(home); err != nil {
			fmt.Printf("LIST_HOME=DENIED\n")
		} else {
			fmt.Printf("LIST_HOME=OK:%d\n", len(entries))
		}
		anc := os.Getenv("NVX_PROBE_ANCESTOR")
		if entries, err := os.ReadDir(anc); err != nil {
			fmt.Printf("LIST_ANCESTOR=DENIED\n")
		} else {
			fmt.Printf("LIST_ANCESTOR=OK:%d\n", len(entries))
		}
		// The grant exists so tools can stat their way up to a project root. If
		// this breaks, the narrowing went too far.
		if _, err := os.Stat(home); err != nil {
			fmt.Printf("STAT=DENIED\n")
		} else {
			fmt.Printf("STAT=OK\n")
		}
		if _, err := os.Stat(os.Getenv("NVX_PROBE_WORKDIR")); err != nil {
			fmt.Printf("STAT_WORKDIR=DENIED\n")
		} else {
			fmt.Printf("STAT_WORKDIR=OK\n")
		}
		os.Exit(0)
	}

	const probeProfile = "nvx.sandbox.listprobe"
	sid, err := ensureAppContainerSID(probeProfile)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	defer deleteAppContainerProfile(probeProfile)

	// A project nested a few levels under the user profile, so the ancestor walk
	// actually runs over profile-owned directories -- the shape that leaked.
	home := os.Getenv("USERPROFILE")
	nested, err := os.MkdirTemp(home, "nvx-listing")
	if err != nil {
		t.Skipf("cannot create a directory under the user profile: %v", err)
	}
	defer os.RemoveAll(nested)
	workDir := filepath.Join(nested, "project")
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatal(err)
	}

	guestHome, err := os.MkdirTemp("", "nvxg")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(guestHome)
	scopeCaps, err := prepareAppContainerFilesystem(sid, "", guestHome, workDir)
	if err != nil {
		t.Fatalf("filesystem prep: %v", err)
	}

	childExe := stageProbeChild(t, guestHome, "listprobe.exe")

	read, write := makeTestPipe(t)
	defer syscall.CloseHandle(read)
	prevOut, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	const stdOutputHandle = uintptr(0xFFFFFFF5)
	procSetStdHandleTest.Call(stdOutputHandle, uintptr(write))

	env := append(scrubEnvironment(guestHome),
		"NVX_PROBE=1", "NVX_LISTING_CHILD=1",
		"NVX_PROBE_HOME="+home,
		"NVX_PROBE_WORKDIR="+workDir,
		"NVX_PROBE_ANCESTOR="+nested,
	)
	_, launchErr := launchAppContainerProcess(childExe,
		[]string{"-test.run=TestContainedProcessCannotListTheHomeDirectory"},
		env, workDir, sid, 0, scopeCaps)

	procSetStdHandleTest.Call(stdOutputHandle, uintptr(prevOut))
	syscall.CloseHandle(write)
	got := readProbeOutput(t, read)

	requireAppContainerLaunch(t, launchErr)
	t.Logf("child output:\n%s", got)

	// The half nvx controls: a directory it granted for traverse must not be
	// listable. (RX) used to give the listing away with it.
	if strings.Contains(got, "LIST_ANCESTOR=OK") {
		t.Errorf("a contained process listed a directory nvx granted for traverse.\n"+
			"The ancestor grant must be (X,RA), not (RX): RX includes list-folder.\n%s", got)
	} else if !strings.Contains(got, "LIST_ANCESTOR=DENIED") {
		t.Errorf("inconclusive ancestor listing result:\n%s", got)
	}

	// %USERPROFILE% itself is a different matter and NOT something nvx grants: the
	// ancestor walk deliberately stops below it. Windows ships an ACE for ALL
	// APPLICATION PACKAGES on the profile, and every AppContainer is in that group,
	// so the listing is the platform's rather than nvx's. Deny ACEs were already
	// measured not to override it (see the secret-mask probe). Recorded here so the
	// documentation describes it accurately instead of implying nvx closed it.
	if strings.Contains(got, "LIST_HOME=DENIED") {
		t.Log("note: the user profile is no longer listable from the sandbox -- if that reproduces, " +
			"README.md and docs/enforcement-matrix.md can be tightened")
	}

	// The narrowing must not break what the grant is for.
	if !strings.Contains(got, "STAT=OK") {
		t.Errorf("the sandbox can no longer stat an ancestor; tools walking up to find a "+
			"project root would fail:\n%s", got)
	}
	if !strings.Contains(got, "STAT_WORKDIR=OK") {
		t.Errorf("the sandbox can no longer stat its own working directory:\n%s", got)
	}
}
