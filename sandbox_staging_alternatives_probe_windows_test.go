//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Is copying a whole runtime into the sandbox actually necessary?
//
// When the runtime is not nvx-managed -- a system Node in `C:\Program Files
// \nodejs`, or nvm-for-windows -- nvx copies the entire distribution into
// `~/.nvx/sandbox-exec` before it can run contained. Measured at 45s to 3
// minutes, once per runtime version, and it is the slowest thing nvx does.
//
// Two cheaper shapes were proposed. Both are tested here rather than argued
// about, because each rests on a claim about NTFS or AppContainer behaviour that
// nobody has checked:
//
//  1. Do not copy at all: run the runtime from where it lives. This works only
//     if a contained process can already read and execute there. Windows ships
//     an ALL APPLICATION PACKAGES ACE on `C:\Program Files`, so it plausibly can.
//
//  2. Hard link instead of copying, which is near-instant and uses no extra
//     disk. This is only safe if nvx never has to change permissions on the
//     link -- an NTFS hard link is another name for the same file record, so a
//     security descriptor written through the link lands on the original. nvx
//     grants read+execute on the staged executable today, which through a link
//     would mean writing an ACE onto a file in `C:\Program Files`.
//
// Read-only: nothing here modifies any ACL outside a temp directory.
func TestWhetherRuntimeStagingCanBeAvoided(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (creates a throwaway AppContainer profile)")
	}
	if os.Getenv("NVX_STAGING_CHILD") == "1" {
		runStagingProbeChild()
		os.Exit(0)
	}

	systemNode := `C:\Program Files\nodejs\node.exe`
	if _, err := os.Stat(systemNode); err != nil {
		t.Skipf("no system Node at %s to probe against: %v", systemNode, err)
	}

	// --- Claim 2, first, because it needs no container ---------------------
	//
	// Whether a hard link shares its security descriptor is a property of NTFS,
	// so a pair of temp files answers it without touching a system path.
	t.Run("a hard link shares the original's permissions", func(t *testing.T) {
		dir := tempDir(t)
		original := filepath.Join(dir, "original.txt")
		if err := os.WriteFile(original, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "link.txt")
		if err := os.Link(original, link); err != nil {
			t.Skipf("this filesystem does not support hard links: %v", err)
		}

		before := icaclsOf(t, original)
		// Granting through the LINK, which is what a hard-linking staging step
		// would do.
		if out, err := runIcacls(link, "/grant", `*S-1-15-2-1:(RX)`); err != nil {
			t.Skipf("cannot grant on this host: %v (%s)", err, out)
		}
		after := icaclsOf(t, original)

		if before == after {
			t.Log("RESULT: the grant did NOT reach the original, so hard-link staging would be safe " +
				"-- surprising, and worth re-checking before relying on it")
			return
		}
		t.Logf("RESULT: granting through the link changed the ORIGINAL's permissions.\nbefore: %s\nafter:  %s",
			before, after)
		t.Log("So hard-link staging would write an AppContainer ACE onto the real runtime files. " +
			"That is only viable if nvx stops granting on the staged executable -- which depends " +
			"on the next subtest.")
	})

	// --- Claim 1: is the grant needed at all? ------------------------------
	t.Run("a contained process can read the system runtime in place", func(t *testing.T) {
		const probeProfile = "nvx.sandbox.stagingprobe"
		sid, err := ensureAppContainerSID(probeProfile)
		if err != nil {
			t.Fatalf("profile: %v", err)
		}
		defer syscall.LocalFree(syscall.Handle(sid))
		defer deleteAppContainerProfile(probeProfile)

		guestHome := tempDir(t)
		workDir := tempDir(t)
		scopeCaps, err := prepareAppContainerFilesystem(sid, "", guestHome, workDir)
		if err != nil {
			t.Fatalf("filesystem prep: %v", err)
		}

		childExe := stageProbeChild(t, guestHome, "stagingprobe.exe")
		read, write := makeTestPipe(t)
		defer syscall.CloseHandle(read)
		prevOut, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
		const stdOutputHandle = uintptr(0xFFFFFFF5)
		procSetStdHandleTest.Call(stdOutputHandle, uintptr(write))

		env := append(scrubEnvironment(guestHome),
			"NVX_PROBE=1", "NVX_STAGING_CHILD=1", "NVX_STAGING_TARGET="+systemNode)
		_, launchErr := launchAppContainerProcess(childExe,
			[]string{"-test.run=TestWhetherRuntimeStagingCanBeAvoided"},
			env, workDir, sid, 0, scopeCaps)

		procSetStdHandleTest.Call(stdOutputHandle, uintptr(prevOut))
		syscall.CloseHandle(write)
		out := readWithTimeout(t, read)
		requireAppContainerLaunch(t, launchErr)
		t.Logf("contained child reported: %s", strings.TrimSpace(out))

		switch {
		case strings.Contains(out, "read=OK"):
			t.Log("RESULT: a contained process can already read the system runtime where it lives, " +
				"with no grant from nvx. Copying the distribution -- 45s to 3 minutes -- may be " +
				"avoidable entirely, which is a better answer than making the copy faster.")
		default:
			t.Log("RESULT: a contained process cannot read the system runtime in place, so the copy " +
				"is doing real work and hard links would need nvx to grant through them -- which " +
				"the first subtest shows would alter the real files.")
		}
	})
}

func runStagingProbeChild() {
	target := os.Getenv("NVX_STAGING_TARGET")
	f, err := os.Open(target)
	if err != nil {
		fmt.Printf("read=DENIED %v\n", err)
		return
	}
	defer f.Close()
	buf := make([]byte, 2)
	if _, err := f.Read(buf); err != nil {
		fmt.Printf("read=OPENED-BUT-UNREADABLE %v\n", err)
		return
	}
	// A PE image starts "MZ"; reading it proves more than the open succeeding.
	fmt.Printf("read=OK header=%q\n", string(buf))
}

func icaclsOf(t *testing.T, path string) string {
	t.Helper()
	out, _ := runIcacls(path)
	return strings.Join(strings.Fields(out), " ")
}

// runIcacls reports a path's ACL, or applies one when given arguments.
func runIcacls(path string, args ...string) (string, error) {
	out, err := exec.Command("icacls", append([]string{path}, args...)...).CombinedOutput()
	return string(out), err
}
