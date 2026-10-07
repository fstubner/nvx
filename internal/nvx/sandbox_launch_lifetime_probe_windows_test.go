//go:build windows

package nvx

import (
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"unsafe"
)

// Everything the launch hands CreateProcess by address is still there when
// CreateProcess reads it.
//
// The attribute list holds the security capabilities, the capability SIDs and
// the handle list as plain integers, and the attribute list itself is reached
// the same way. The garbage collector follows none of them. The code meant to
// keep them alive was a blank assignment, which Go drops, so a collection in
// the gap could free them and the next allocation could reuse the memory.
// Measured 2026-10-07 with collections forced throughout: of 1000 launches, 9
// failed with "The parameter is incorrect" and 7 started the child with a
// token that was not an AppContainer token at all.
//
// A collection is run here at the worst moment, just before CreateProcess, and
// the freed memory is handed out again at once. The child reports whether its
// token is an AppContainer token.
func TestALaunchSurvivesACollectionJustBeforeCreateProcess(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (launches a real AppContainer)")
	}
	const profile = "nvx.sandbox.lifetimeprobe"
	sid, err := ensureAppContainerSID(profile)
	if err != nil {
		t.Skipf("cannot create an AppContainer profile here: %v", err)
	}
	defer deleteAppContainerProfile(profile)
	defer syscall.LocalFree(syscall.Handle(sid))

	guestHome := tempDir(t)
	workDir := tempDir(t)
	scopeCaps, _, err := prepareAppContainerFilesystem(sid, "", guestHome, workDir)
	if err != nil {
		t.Fatalf("prepare the sandbox filesystem: %v", err)
	}
	child, err := probeChildBinary()
	if err != nil {
		t.Fatalf("probe child: %v", err)
	}
	data, err := os.ReadFile(child)
	if err != nil {
		t.Fatal(err)
	}
	childExe := filepath.Join(guestHome, "lifetimeprobe.exe")
	if err := os.WriteFile(childExe, data, 0o700); err != nil {
		t.Fatal(err)
	}
	caps := launchCapabilitySIDs(scopeCaps, nil)
	env := append(scrubEnvironment(guestHome), "NVX_TOKEN_CHECK_CHILD=1")

	var reused [][]byte
	beforeCreateProcess = func() {
		runtime.GC()
		runtime.GC()
		// The allocator zeroes memory it hands out again, so a freed attribute
		// list comes back empty and freed capabilities come back null.
		for _, size := range []int{16, 24, 32, 48, 64, 80, 96, 112, 128} {
			for i := 0; i < 2000; i++ {
				reused = append(reused, make([]byte, size))
			}
		}
	}
	defer func() { beforeCreateProcess = nil }()

	for i := 0; i < 10; i++ {
		code, err := launchAppContainerProcess(childExe,
			[]string{"-test.run=^TestTokenCheckChild$"}, env, workDir, sid, 0, caps)
		requireAppContainerLaunch(t, err)
		switch code {
		case 0:
		case 3:
			t.Fatalf("launch %d started its child outside the AppContainer: memory the attribute list "+
				"points into was freed and handed out again before CreateProcess read it", i)
		default:
			t.Fatalf("launch %d: the child exited %d", i, code)
		}
		reused = nil
	}
}

// TestTokenCheckChild is the contained half of the test above. It exits 0 when
// its token is an AppContainer token and 3 when it is not.
func TestTokenCheckChild(t *testing.T) {
	if os.Getenv("NVX_TOKEN_CHECK_CHILD") != "1" {
		t.Skip("child-side helper for TestALaunchSurvivesACollectionJustBeforeCreateProcess")
	}
	const tokenIsAppContainer = 29
	var token syscall.Token
	self, _ := syscall.GetCurrentProcess()
	if err := syscall.OpenProcessToken(self, syscall.TOKEN_QUERY, &token); err != nil {
		os.Exit(4)
	}
	var isAppContainer, n uint32
	if err := syscall.GetTokenInformation(token, tokenIsAppContainer,
		(*byte)(unsafe.Pointer(&isAppContainer)), 4, &n); err != nil {
		os.Exit(5)
	}
	if isAppContainer == 0 {
		os.Exit(3)
	}
	os.Exit(0)
}
