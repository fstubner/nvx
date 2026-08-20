//go:build windows

package main

// Probe (NVX_PROBE=1) for the mechanism a per-project containment identity would
// rest on: does a file ACE naming a CUSTOM capability SID grant access to an
// AppContainer that holds that capability, and deny one that does not?
//
// Why capabilities rather than a profile per project: the AppContainer profile is
// deliberately stable so `nvx setup`'s elevated drive-root grants have a durable
// target. Splitting the profile per project would silently break those. Capability
// SIDs are a second, independent axis carried in the same token, so the package
// SID can stay stable for setup while the per-project identity rides alongside.
//
// A per-project identity also has to be idempotent to be affordable: the same
// project must derive the same SID every time, so the icacls write happens once
// and `appContainerHasGrant` skips it thereafter. A per-SESSION identity would
// pay that write on every launch and leave a dead ACE on the project directory
// after each one.
//
// If capability ACEs do not work this way, this whole direction is wrong and the
// answer has to come from somewhere else -- which is why it is measured before
// anything is built on it.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCapabilitySidGatesFileAccess(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run")
	}

	if os.Getenv("NVX_CAPSID_CHILD") == "1" {
		target := os.Getenv("NVX_PROBE_TARGET")
		if _, err := os.ReadFile(target); err != nil {
			fmt.Printf("READ=DENIED\n")
		} else {
			fmt.Printf("READ=OK\n")
		}
		os.Exit(0)
	}

	capSID, err := deriveCapabilitySIDString("nvx.project.probe.one")
	if err != nil {
		t.Skipf("cannot derive a custom capability SID on this host: %v", err)
	}
	otherSID, err := deriveCapabilitySIDString("nvx.project.probe.two")
	if err != nil {
		t.Fatalf("derive second capability SID: %v", err)
	}
	if capSID == otherSID {
		t.Fatalf("two different capability names derived the same SID (%s); they cannot isolate anything", capSID)
	}
	t.Logf("capability SIDs: %s / %s", capSID, otherSID)

	const probeProfile = "nvx.sandbox.capprobe"
	sid, err := ensureAppContainerSID(probeProfile)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	defer deleteAppContainerProfile(probeProfile)

	guestHome, err := os.MkdirTemp("", "nvxg")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(guestHome)
	workDir := t.TempDir()
	scopeCaps, err := prepareAppContainerFilesystem(sid, "", guestHome, workDir)
	if err != nil {
		t.Fatalf("filesystem prep: %v", err)
	}

	// A "project" granted ONLY to the first capability, never to the package SID.
	project := t.TempDir()
	secret := filepath.Join(project, "project-file.txt")
	if err := os.WriteFile(secret, []byte("PROJECT-CONTENT"), 0o600); err != nil {
		t.Fatal(err)
	}
	grantArg := fmt.Sprintf("*%s:(OI)(CI)(RX)", capSID)
	if out, gerr := runWinCmd(30*time.Second, "icacls", project, "/grant", grantArg, "/c", "/q"); gerr != nil {
		t.Skipf("cannot grant a capability SID with icacls: %v (%s)", gerr, strings.TrimSpace(string(out)))
	}

	childExe := stageProbeChild(t, guestHome, "probe.exe")

	run := func(caps []string) string {
		read, write := makeTestPipe(t)
		defer syscall.CloseHandle(read)
		prevOut, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
		const stdOutputHandle = uintptr(0xFFFFFFF5)
		procSetStdHandleTest.Call(stdOutputHandle, uintptr(write))

		env := append(scrubEnvironment(guestHome),
			"NVX_PROBE=1", "NVX_CAPSID_CHILD=1", "NVX_PROBE_TARGET="+secret,
		)
		_, launchErr := launchAppContainerProcess(childExe,
			[]string{"-test.run=TestCapabilitySidGatesFileAccess"},
			env, workDir, sid, 0, append(scopeCaps, caps...))

		procSetStdHandleTest.Call(stdOutputHandle, uintptr(prevOut))
		syscall.CloseHandle(write)
		out := readProbeOutput(t, read)
		requireAppContainerLaunch(t, launchErr)
		return out
	}

	withCap := run([]string{capSID})
	t.Logf("holding the matching capability: %q", strings.TrimSpace(withCap))
	withoutCap := run(nil)
	t.Logf("holding no capability: %q", strings.TrimSpace(withoutCap))
	wrongCap := run([]string{otherSID})
	t.Logf("holding a different capability: %q", strings.TrimSpace(wrongCap))

	if !strings.Contains(withCap, "READ=OK") {
		t.Errorf("a process holding the matching capability could NOT read a file granted to it (%q). "+
			"Capability ACEs do not gate file access this way, so a per-project capability is not a viable identity.", strings.TrimSpace(withCap))
	}
	if !strings.Contains(withoutCap, "READ=DENIED") {
		t.Errorf("a process holding NO capability read the file (%q); the ACE is not gating anything.", strings.TrimSpace(withoutCap))
	}
	if !strings.Contains(wrongCap, "READ=DENIED") {
		t.Errorf("a process holding a DIFFERENT capability read the file (%q); capabilities do not isolate from each other.", strings.TrimSpace(wrongCap))
	}
}
