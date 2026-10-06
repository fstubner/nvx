//go:build windows

package nvx

// A drive-root entry an older `nvx setup` left admits no launch.
//
// Setup no longer grants anything, and launches stopped carrying the capability
// named by setupCapabilityName, so an entry left for it applies to nothing
// whether or not `nvx setup` has removed it. This writes such an entry the way
// the old setup did and checks that a real launch is still refused. A launch
// that carried the capability again would read the file, and nothing else in the
// suite would notice.
//
// TestCapabilitySidGatesFileAccess proves the general mechanism -- a custom
// capability ACE gates access -- which is what makes the refusal here mean
// something.
//
// Deliberately on a directory this test owns rather than on C:\ or C:\Users,
// which need elevation to write. The write primitive is identical either way.

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestALeftoverSetupGrantAdmitsNoLaunch(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (creates a throwaway AppContainer profile and writes an ACL)")
	}
	if os.Getenv("NVX_SETUPCAP_CHILD") == "1" {
		if _, err := os.ReadFile(os.Getenv("NVX_PROBE_TARGET")); err != nil {
			os.Stdout.WriteString("READ=DENIED\n")
		} else {
			os.Stdout.WriteString("READ=OK\n")
		}
		if _, err := os.Stat(os.Getenv("NVX_PROBE_STAT_TARGET")); err != nil {
			os.Stdout.WriteString("STAT=DENIED\n")
		} else {
			os.Stdout.WriteString("STAT=OK\n")
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
	// A stand-in for a drive root, granted below through setup's own write: the
	// this-folder entry, written without the walk beneath it.
	statTarget := tempDir(t)

	childExe := stageProbeChild(t, guestHome, "setupcap.exe")
	run := func() string {
		t.Helper()
		read, write := makeTestPipe(t)
		defer syscall.CloseHandle(read)
		prevOut, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
		const stdOutputHandle = uintptr(0xFFFFFFF5)
		procSetStdHandleTest.Call(stdOutputHandle, uintptr(write))

		env := append(scrubEnvironment(guestHome),
			"NVX_PROBE=1", "NVX_SETUPCAP_CHILD=1", "NVX_PROBE_TARGET="+target,
			"NVX_PROBE_STAT_TARGET="+statTarget)
		// launchCapabilitySIDs, not a hand-written list. Going through the real
		// assembly is what makes the ACE and the token independent -- the entry
		// below names the setup capability, and only this function decides whether
		// the launch carries it.
		_, launchErr := launchAppContainerProcess(childExe,
			[]string{"-test.run=TestALeftoverSetupGrantAdmitsNoLaunch"},
			env, workDir, sid, 0, launchCapabilitySIDs(scopeCaps, nil))

		procSetStdHandleTest.Call(stdOutputHandle, uintptr(prevOut))
		syscall.CloseHandle(write)
		out := readWithTimeout(t, read)
		requireAppContainerLaunch(t, launchErr)
		return strings.TrimSpace(out)
	}

	// Negative control first: without the entry the file must be unreachable, or a
	// later READ=DENIED would prove nothing about the launch.
	got := run()
	if !strings.Contains(got, "READ=DENIED") {
		t.Fatalf("the target was readable BEFORE any grant (%q); this probe cannot "+
			"distinguish the capability from ambient access", got)
	}
	if !strings.Contains(got, "STAT=DENIED") {
		t.Fatalf("the stat target was statable BEFORE any entry (%q); this probe cannot "+
			"tell the entry from ambient access", got)
	}

	// Now the entry an older setup made, on a directory this test can write.
	if err := grantACL(outside, setupCap, aclMaskReadExec, nvxInheritFlags); err != nil {
		t.Fatalf("granting the setup capability read/execute: %v", err)
	}
	t.Cleanup(func() { _ = revokeACL(outside, setupCap) })
	if err := setupACLWrite(statTarget, setupCap, aclMaskReadExec); err != nil {
		t.Fatalf("writing the entry: %v", err)
	}
	t.Cleanup(func() { _ = revokeSidGrant(setupCap, statTarget) })

	got = run()
	if !strings.Contains(got, "STAT=DENIED") || !strings.Contains(got, "READ=DENIED") {
		t.Fatalf("a launch reached a directory granted only to %s (%s): got %q.\n"+
			"Launches must not carry the setup capability, or an entry an older setup left "+
			"on a drive root would keep applying.", setupCapabilityName, setupCap, got)
	}
}
