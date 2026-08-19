//go:build windows

package main

// The measurement F76's fix turns on: is the ancestor grant needed at all?
//
// grantWorkdirAncestors costs the whole 3s budget on every launch for a project
// under AppData, because the icacls write there is killed at the per-path timeout
// every time and the has-grant read answers false forever. Before removing it,
// find out what it buys -- the comment says tools stat their way up to a project
// root, and if the container can already do that, the grant is pure cost.
//
// This grants the guest home and working directory exactly as a real launch does,
// then deliberately SKIPS the ancestor walk, and asks a contained child what it
// can still do. Launching at all is itself part of the answer: the child
// executable lives in the guest home, so CreateProcess has to traverse the same
// chain to read it.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestAncestorGrantsAreNotNeededForContainment(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run")
	}

	if os.Getenv("NVX_ANCESTOR_CHILD") == "1" {
		workDir := os.Getenv("NVX_PROBE_WORKDIR")
		if _, err := os.Stat(workDir); err != nil {
			fmt.Printf("STAT_WORKDIR=DENIED\n")
		} else {
			fmt.Printf("STAT_WORKDIR=OK\n")
		}
		if err := os.WriteFile(filepath.Join(workDir, "written.txt"), []byte("ok"), 0o600); err != nil {
			fmt.Printf("WRITE_WORKDIR=DENIED\n")
		} else {
			fmt.Printf("WRITE_WORKDIR=OK\n")
		}
		// Walking up is what the grant is described as enabling: npm looks for a
		// project root by statting each parent in turn.
		for i, anc := range strings.Split(os.Getenv("NVX_PROBE_ANCESTORS"), "|") {
			if anc == "" {
				continue
			}
			if _, err := os.Stat(anc); err != nil {
				fmt.Printf("STAT_ANC%d=DENIED\n", i)
			} else {
				fmt.Printf("STAT_ANC%d=OK\n", i)
			}
		}
		os.Exit(0)
	}

	const probeProfile = "nvx.sandbox.ancneed"
	sid, err := ensureAppContainerSID(probeProfile)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	defer deleteAppContainerProfile(probeProfile)

	// Both under %TEMP%, i.e. the AppData chain whose grants always time out.
	guestHome, err := os.MkdirTemp("", "nvxg")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(guestHome)
	workDir, err := os.MkdirTemp("", "nvxw")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workDir)

	capSID, err := scopeCapabilitySID(sandboxScopeForWorkDir(workDir))
	if err != nil {
		t.Fatal(err)
	}
	// Everything prepareAppContainerFilesystem does EXCEPT the ancestor walk.
	if err := grantSandboxModify(capSID, guestHome); err != nil {
		t.Fatalf("grant guest home: %v", err)
	}
	if err := labelLowIntegrity(guestHome); err != nil {
		t.Fatalf("label: %v", err)
	}
	if err := grantSandboxModify(capSID, workDir); err != nil {
		t.Fatalf("grant workdir: %v", err)
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	childExe := filepath.Join(guestHome, "ancprobe.exe")
	if err := os.WriteFile(childExe, data, 0o700); err != nil {
		t.Fatal(err)
	}

	ancestors := ancestorGrantPaths(workDir, os.Getenv("USERPROFILE"))
	t.Logf("ancestors deliberately NOT granted: %v", ancestors)

	read, write := makeTestPipe(t)
	defer syscall.CloseHandle(read)
	prevOut, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	const stdOutputHandle = uintptr(0xFFFFFFF5)
	procSetStdHandleTest.Call(stdOutputHandle, uintptr(write))

	env := append(scrubEnvironment(guestHome),
		"NVX_PROBE=1", "NVX_ANCESTOR_CHILD=1",
		"NVX_PROBE_WORKDIR="+workDir,
		"NVX_PROBE_ANCESTORS="+strings.Join(ancestors, "|"),
	)
	_, launchErr := launchAppContainerProcess(childExe,
		[]string{"-test.run=TestAncestorGrantsAreNotNeededForContainment"},
		env, workDir, sid, 0, []string{capSID})

	procSetStdHandleTest.Call(stdOutputHandle, uintptr(prevOut))
	syscall.CloseHandle(write)
	got := readProbeOutput(t, read)

	requireAppContainerLaunch(t, launchErr)
	t.Logf("child output:\n%s", got)

	// The launch itself had to read an executable inside the guest home, so
	// reaching this point at all already says the chain is traversable.
	if !strings.Contains(got, "STAT_WORKDIR=OK") {
		t.Errorf("without ancestor grants the sandbox cannot stat its own working directory, "+
			"so the grants are load-bearing and must stay:\n%s", got)
	}
	if !strings.Contains(got, "WRITE_WORKDIR=OK") {
		t.Errorf("without ancestor grants the sandbox cannot write its working directory:\n%s", got)
	}
	for i := range ancestors {
		if strings.Contains(got, fmt.Sprintf("STAT_ANC%d=DENIED", i)) {
			t.Logf("ancestor %d (%s) is NOT statable without the grant -- a tool walking up to "+
				"find a project root would fail there", i, ancestors[i])
		}
	}
}
