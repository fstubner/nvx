//go:build windows

package nvx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// TestIntegrityLabelHidesSecretFromAppContainer (NVX_PROBE=1) asks whether a
// mandatory integrity label can do what the deny ACEs in
// TestDenyACEHidesSecretFromAppContainer could not. An AppContainer process
// runs at low integrity, and a medium label with the no-read-up policy refuses
// reads from below medium. The label is set on the file's SACL, which the user
// may change on a file they own, and the access control list stays as it was.
//
// This pins the current, unprotected state. Measured 2026-10-06 on Windows 11
// 26300 and on the CI Windows runner: with Medium Mandatory Level:(NW,NR) on
// .env, read back with icacls, a contained process still read it, as it did
// with no label. If this starts
// failing because the read is refused, the label works: update the known
// limitations with it.
//
// Why, measured 2026-10-06 (TestWindowsDotenvProtectionExperiments has the full
// record). The child is an AppContainer process at Low integrity (S-1-16-4096) with
// token mandatory policy 1, and the file's owner is the child's own user SID. A
// plain Low integrity process, not an AppContainer, started from a copy of the same
// user's token was refused by the same label. So the label works, and Windows does
// not apply it to an AppContainer process. The child also appended to an unlabelled
// package.json, which a plain Low integrity process could not.
//
// NVX_PROBE_LABEL overrides the SDDL; "none" applies no label, as the control.
func TestIntegrityLabelHidesSecretFromAppContainer(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (creates a throwaway AppContainer profile)")
	}
	const probeProfile = "nvx.sandbox.labelprobe"
	sid, err := ensureAppContainerSID(probeProfile)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	defer deleteAppContainerProfile(probeProfile)

	guestHome := tempDir(t)
	workDir := tempDir(t)
	secret := filepath.Join(workDir, ".env")
	normal := filepath.Join(workDir, "package.json")
	if err := os.WriteFile(secret, []byte("API_KEY=super-secret-value-12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(normal, []byte(`{"name":"victim"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	scopeCaps, _, err := prepareAppContainerFilesystem(sid, "", guestHome, workDir)
	if err != nil {
		t.Fatalf("filesystem prep: %v", err)
	}

	label := os.Getenv("NVX_PROBE_LABEL")
	if label == "" {
		label = "S:(ML;;NWNR;;;ME)"
	}
	if label != "none" {
		if err := setFileLabelSDDL(secret, label); err != nil {
			t.Fatalf("set label %s: %v", label, err)
		}
	}
	// What the label actually is, read back, so a label that did not apply
	// cannot pass for one that did not work.
	if out, err := runWinCmd(20*time.Second, "icacls", secret); err == nil {
		t.Logf("icacls .env:\n%s", strings.TrimSpace(string(out)))
	}
	if b, err := os.ReadFile(secret); err != nil || !strings.Contains(string(b), "super-secret") {
		t.Fatalf("the user can no longer read .env after the label (err %v)", err)
	}

	childExe := stageProbeChild(t, guestHome, "labelprobe.exe")
	read, write := makeTestPipe(t)
	defer syscall.CloseHandle(read)
	prevOut, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	const stdOutputHandle = uintptr(0xFFFFFFF5)
	procSetStdHandleTest.Call(stdOutputHandle, uintptr(write))
	// The child half of TestDenyACEHidesSecretFromAppContainer does the reads.
	env := append(scrubEnvironment(guestHome),
		"NVX_PROBE=1",
		"NVX_SECRET_PROBE_CHILD=1",
		"NVX_PROBE_SECRET="+secret,
		"NVX_PROBE_NORMAL="+normal,
		"NVX_PROBE_WRITE="+filepath.Join(workDir, "node_modules_marker"),
	)
	exitCode, launchErr := launchAppContainerProcess(childExe,
		[]string{"-test.run=TestDenyACEHidesSecretFromAppContainer"},
		env, workDir, sid, 0, scopeCaps)
	procSetStdHandleTest.Call(stdOutputHandle, uintptr(prevOut))
	syscall.CloseHandle(write)
	got := readAllWithTimeout(t, read)
	requireAppContainerLaunch(t, launchErr)
	t.Logf("label %q: child exit=%d output=%q", label, exitCode, got)
	logProbeEvidence(t, secret, got)

	switch {
	case contains(got, "SECRET=DENIED") && label != "none":
		t.Errorf("with label %s a contained process can no longer read .env: the label works, so update "+
			"site/src/content/docs/docs/limitations.md, SECURITY.md and this test", label)
	case contains(got, "SECRET=DENIED"):
		t.Errorf("with no label a contained process could not read .env, so this measured nothing")
	case contains(got, "SECRET=READ:"):
		t.Logf("RESULT: with label %s a contained process reads .env", label)
	default:
		t.Errorf("inconclusive secret result in %q", got)
	}
	if !contains(got, "NORMAL=READ:") || !contains(got, "WRITE=OK") {
		t.Errorf("the rest of the project stopped working: %q", got)
	}
}

// setFileLabelSDDL replaces path's mandatory label with the one in sddl's SACL.
func setFileLabelSDDL(path, sddl string) error {
	sd, err := securityDescriptorFromSDDL(sddl)
	if err != nil {
		return err
	}
	defer syscall.LocalFree(syscall.Handle(sd))
	var present, defaulted int32
	var sacl uintptr
	if r, _, e := procGetSecurityDescriptorSacl.Call(sd, uintptr(unsafe.Pointer(&present)),
		uintptr(unsafe.Pointer(&sacl)), uintptr(unsafe.Pointer(&defaulted))); r == 0 {
		return fmt.Errorf("GetSecurityDescriptorSacl: %v", e)
	}
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	const seFileObject = 1
	const labelSecurityInformation = 0x10
	if r, _, _ := procSetNamedSecurityInfoW.Call(uintptr(unsafe.Pointer(p)), seFileObject,
		labelSecurityInformation, 0, 0, 0, sacl); r != 0 {
		return fmt.Errorf("SetNamedSecurityInfoW: error %d", r)
	}
	return nil
}

var procGetSecurityDescriptorSacl = modAdvapi32.NewProc("GetSecurityDescriptorSacl")
