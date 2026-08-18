//go:build windows

package main

// Control probe (NVX_PROBE=1) for the cross-project finding: is a directory nvx
// has NEVER granted reachable from inside the sandbox anyway?
//
// This has to be settled before anything is designed around it. The cross-project
// probe used t.TempDir(), which lives under %LOCALAPPDATA%\Temp, and the deny-ACE
// probe already established that parts of the user profile tree carry an ACE for
// ALL APPLICATION PACKAGES (S-1-15-2-1) -- a group every AppContainer process is
// in. If that ACE is what granted the access, then the cause is not nvx's own
// never-revoked grants at all, per-session identities would fix nothing, and the
// honest limitation is much broader.
//
// So: create directories nvx never touches, in both locations a real project
// plausibly lives, and see what a contained process can do with them.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestUngrantedDirectoriesAreUnreachable(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run")
	}

	if os.Getenv("NVX_UNGRANTED_CHILD") == "1" {
		for _, spec := range strings.Split(os.Getenv("NVX_PROBE_TARGETS"), "|") {
			parts := strings.SplitN(spec, "=", 2)
			if len(parts) != 2 {
				continue
			}
			label, path := parts[0], parts[1]
			if _, err := os.ReadFile(path); err != nil {
				fmt.Printf("%s_READ=DENIED\n", label)
			} else {
				fmt.Printf("%s_READ=OK\n", label)
			}
			if err := os.WriteFile(path+".tampered", []byte("x"), 0o600); err != nil {
				fmt.Printf("%s_WRITE=DENIED\n", label)
			} else {
				fmt.Printf("%s_WRITE=OK\n", label)
			}
		}
		os.Exit(0)
	}

	const probeProfile = "nvx.sandbox.ungranted"
	sid, err := ensureAppContainerSID(probeProfile)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	defer deleteAppContainerProfile(probeProfile)

	// Two locations a project realistically lives in, neither ever granted.
	underTemp := t.TempDir() // %LOCALAPPDATA%\Temp\...
	underProfile, err := os.MkdirTemp(os.Getenv("USERPROFILE"), "nvx-ungranted")
	if err != nil {
		t.Skipf("cannot create a directory under the user profile: %v", err)
	}
	defer os.RemoveAll(underProfile)

	targets := ""
	for label, dir := range map[string]string{"TEMP": underTemp, "PROFILE": underProfile} {
		f := filepath.Join(dir, "never-granted.txt")
		if err := os.WriteFile(f, []byte("UNGRANTED-CONTENT"), 0o600); err != nil {
			t.Fatal(err)
		}
		if targets != "" {
			targets += "|"
		}
		targets += label + "=" + f
	}

	// Only the guest home and the (unrelated) working directory are granted --
	// exactly what a normal session does. Neither target above is touched.
	guestHome, err := os.MkdirTemp("", "nvxg")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(guestHome)
	workDir := t.TempDir()
	scopeCaps, err := prepareAppContainerFilesystem(sid, guestHome, workDir)
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
	childExe := filepath.Join(guestHome, "probe.exe")
	if err := os.WriteFile(childExe, data, 0o700); err != nil {
		t.Fatal(err)
	}

	read, write := makeTestPipe(t)
	defer syscall.CloseHandle(read)
	prevOut, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	const stdOutputHandle = uintptr(0xFFFFFFF5)
	procSetStdHandleTest.Call(stdOutputHandle, uintptr(write))

	env := append(scrubEnvironment(guestHome),
		"NVX_PROBE=1", "NVX_UNGRANTED_CHILD=1",
		"NVX_PROBE_TARGETS="+targets,
	)
	_, launchErr := launchAppContainerProcess(childExe,
		[]string{"-test.run=TestUngrantedDirectoriesAreUnreachable"},
		env, workDir, sid, 0, scopeCaps)

	procSetStdHandleTest.Call(stdOutputHandle, uintptr(prevOut))
	syscall.CloseHandle(write)
	got := readProbeOutput(t, read)

	requireAppContainerLaunch(t, launchErr)
	t.Logf("child output:\n%s", got)

	// If an ungranted directory is reachable, nvx's grants are not what opened the
	// other project, and no per-session identity would close it.
	for _, label := range []string{"TEMP", "PROFILE"} {
		if strings.Contains(got, label+"_READ=OK") {
			t.Errorf("%s: a directory nvx never granted is READABLE from inside the sandbox. "+
				"The cross-project access is then inherited from the user profile's "+
				"ALL APPLICATION PACKAGES ACE, not from nvx's own grants.\n%s", label, got)
		}
		if strings.Contains(got, label+"_WRITE=OK") {
			t.Errorf("%s: a directory nvx never granted is WRITABLE from inside the sandbox.\n%s", label, got)
		}
		if !strings.Contains(got, label+"_READ=") {
			t.Errorf("inconclusive %s result:\n%s", label, got)
		}
	}
}
