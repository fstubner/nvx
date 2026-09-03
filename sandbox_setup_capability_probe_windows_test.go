//go:build windows

package main

// Would running `nvx setup` actually help?
//
// setup is elevated, runs once, and grants drive-root stat access to the
// capability named by setupCapabilityName. Every launch appends that capability
// to its token (sandbox_native_windows.go). If those two ever named different
// things -- a renamed constant, a launch path that forgot to append it, a
// derivation that differs between the elevated process and the contained one --
// setup would grant access to an identity nothing holds, and contained `npx`
// would keep failing with the machine's owner having done exactly what they were
// told. The failure would look like "setup did not work" and be invisible from
// either side.
//
// Nothing asserted that end to end. TestCapabilitySidGatesFileAccess proves the
// general mechanism -- a custom capability ACE gates access -- but not that THIS
// capability is the one a real launch carries.
//
// Deliberately on a directory this test owns rather than on C:\ or C:\Users.
// Writing those needs elevation, which is the whole reason setup exists; the
// grant primitive is identical either way, so what is unverified after this is
// only whether an Administrator can write the drive root, not whether doing so
// would achieve anything. That is a permissions question, not a design one.

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestTheCapabilitySetupGrantsIsTheOneLaunchesCarry(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (creates a throwaway AppContainer profile and writes an ACL)")
	}
	if os.Getenv("NVX_SETUPCAP_CHILD") == "1" {
		if _, err := os.ReadFile(os.Getenv("NVX_PROBE_TARGET")); err != nil {
			os.Stdout.WriteString("READ=DENIED\n")
		} else {
			os.Stdout.WriteString("READ=OK\n")
		}
		os.Exit(0)
	}

	// The identity `nvx setup` writes ACEs for, derived exactly as setup derives
	// it. If this call and windows_setup_windows.go ever disagree, that is the bug
	// this test exists to catch.
	setupCap, err := deriveCapabilitySIDString(setupCapabilityName)
	if err != nil {
		t.Fatalf("cannot derive the setup capability %q: %v", setupCapabilityName, err)
	}

	const probeProfile = "nvx.sandbox.setupcap"
	sid, err := ensureAppContainerSID(probeProfile)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	defer deleteAppContainerProfile(probeProfile)

	guestHome := tempDir(t)
	workDir := tempDir(t)
	scopeCaps, _, err := prepareAppContainerFilesystem(sid, "", guestHome, workDir)
	if err != nil {
		t.Fatalf("filesystem prep: %v", err)
	}

	// A directory the sandbox has no reason to reach, holding a file it must not
	// read until the capability is granted.
	outside := tempDir(t)
	target := filepath.Join(outside, "drive-root-stand-in.txt")
	if err := os.WriteFile(target, []byte("SETUP-CAP-PROBE"), 0o600); err != nil {
		t.Fatal(err)
	}

	childExe := stageProbeChild(t, guestHome, "setupcap.exe")
	run := func() string {
		t.Helper()
		read, write := makeTestPipe(t)
		defer syscall.CloseHandle(read)
		prevOut, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
		const stdOutputHandle = uintptr(0xFFFFFFF5)
		procSetStdHandleTest.Call(stdOutputHandle, uintptr(write))

		env := append(scrubEnvironment(guestHome),
			"NVX_PROBE=1", "NVX_SETUPCAP_CHILD=1", "NVX_PROBE_TARGET="+target)
		// launchCapabilitySIDs, not a hand-written append of setupCap.
		//
		// The first version of this test passed setupCap to the launch itself, which
		// made it tautological: it granted an ACE to a capability and then handed the
		// launch that same capability, so of course they matched. Renaming the
		// constant under it still passed. Going through the real assembly is what
		// makes the ACE and the token independent -- the grant below names the setup
		// capability, and only this function decides whether the launch carries it.
		_, launchErr := launchAppContainerProcess(childExe,
			[]string{"-test.run=TestTheCapabilitySetupGrantsIsTheOneLaunchesCarry"},
			env, workDir, sid, 0, launchCapabilitySIDs(scopeCaps, nil))

		procSetStdHandleTest.Call(stdOutputHandle, uintptr(prevOut))
		syscall.CloseHandle(write)
		out := readWithTimeout(t, read)
		requireAppContainerLaunch(t, launchErr)
		return strings.TrimSpace(out)
	}

	// Negative control first: without the grant the file must be unreachable, or a
	// later READ=OK would prove nothing about the capability.
	if got := run(); !strings.Contains(got, "READ=DENIED") {
		t.Fatalf("the target was readable BEFORE any grant (%q); this probe cannot "+
			"distinguish the capability from ambient access", got)
	}

	// Now the grant setup makes, on a directory this test can write.
	if err := grantACL(outside, setupCap, aclMaskReadExec, nvxInheritFlags); err != nil {
		t.Fatalf("granting the setup capability read/execute: %v", err)
	}
	t.Cleanup(func() { _ = revokeACL(outside, setupCap) })

	if got := run(); !strings.Contains(got, "READ=OK") {
		t.Fatalf("a launch could NOT reach a directory granted to %s (%s): got %q.\n"+
			"That means `nvx setup` grants an identity contained launches do not carry, "+
			"so the documented repair for contained npx would not work and would give no sign of it.",
			setupCapabilityName, setupCap, got)
	}
}
