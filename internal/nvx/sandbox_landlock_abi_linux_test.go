//go:build linux

package nvx

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

// The access-right constants are the kernel's, bit for bit.
//
// Source of truth: include/uapi/linux/landlock.h. The values are written out
// here rather than derived, because a test that computes them the same way the
// code does can only agree with it. Until this test existed the file had an
// invented LANDLOCK_ACCESS_FS_WRITE_DIR at bit 3 -- the kernel has no such
// right; bit 3 is READ_DIR -- and every right above it shifted up by one, so
// the name "READ_DIR" in this package denoted the kernel's REMOVE_DIR. That
// stayed invisible for as long as the tests were written in terms of the same
// misnamed constants, which is the failure mode this one closes.
func TestLandlockAccessConstantsMatchTheKernelHeader(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  uint64
		want uint64
	}{
		{"LANDLOCK_ACCESS_FS_EXECUTE", landlockAccessFSExecute, 1 << 0},
		{"LANDLOCK_ACCESS_FS_WRITE_FILE", landlockAccessFSWriteFile, 1 << 1},
		{"LANDLOCK_ACCESS_FS_READ_FILE", landlockAccessFSReadFile, 1 << 2},
		{"LANDLOCK_ACCESS_FS_READ_DIR", landlockAccessFSReadDir, 1 << 3},
		{"LANDLOCK_ACCESS_FS_REMOVE_DIR", landlockAccessFSRemoveDir, 1 << 4},
		{"LANDLOCK_ACCESS_FS_REMOVE_FILE", landlockAccessFSRemoveFile, 1 << 5},
		{"LANDLOCK_ACCESS_FS_MAKE_CHAR", landlockAccessFSMakeChar, 1 << 6},
		{"LANDLOCK_ACCESS_FS_MAKE_DIR", landlockAccessFSMakeDir, 1 << 7},
		{"LANDLOCK_ACCESS_FS_MAKE_REG", landlockAccessFSMakeReg, 1 << 8},
		{"LANDLOCK_ACCESS_FS_MAKE_SOCK", landlockAccessFSMakeSock, 1 << 9},
		{"LANDLOCK_ACCESS_FS_MAKE_FIFO", landlockAccessFSMakeFifo, 1 << 10},
		{"LANDLOCK_ACCESS_FS_MAKE_BLOCK", landlockAccessFSMakeBlock, 1 << 11},
		{"LANDLOCK_ACCESS_FS_MAKE_SYM", landlockAccessFSMakeSym, 1 << 12},
		{"LANDLOCK_ACCESS_FS_REFER", landlockAccessFSRefer, 1 << 13},
		{"LANDLOCK_ACCESS_FS_TRUNCATE", landlockAccessFSTruncate, 1 << 14},
		{"LANDLOCK_ACCESS_FS_IOCTL_DEV", landlockAccessFSIoctlDev, 1 << 15},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %#x, want %#x (include/uapi/linux/landlock.h)", tc.name, tc.got, tc.want)
		}
	}
}

// What a read-only root actually permits, on the real kernel.
//
// This is the consequence of the constants, measured rather than reasoned
// about. With bit 3 and bit 4 swapped, the "read/execute" mask granted the
// kernel's REMOVE_DIR to every read-only root and never granted READ_DIR to
// any of them. Two things followed, and both were misread as properties of
// Landlock rather than as a bug:
//
//   - directories under /usr, /etc and the nvx runtime tree could not be
//     listed, which README recorded as a known limitation;
//   - an empty directory under the nvx runtime tree COULD be removed by a
//     contained process. Nothing stops that but Landlock: the tree is owned
//     by the user, so DAC permits it.
//
// Runs in a subprocess because landlock_restrict_self is irreversible.
func TestLandlockReadOnlyRootsAreListableAndNotRemovable(t *testing.T) {
	probeReadOnlyRoots(t, "")
}

// The first-ABI ruleset -- what a Linux 5.13 kernel gets -- still contains.
//
// This is the older-kernel path made runnable. The cap in
// landlockHandledAccessForABI exists so kernels between 5.13 and 6.9 get a
// ruleset they accept instead of EINVAL; but a ruleset accepted is not a
// ruleset that contains, and no machine this project tests on runs a kernel
// that old. So the v1 ruleset is applied here on whatever kernel is present,
// through the same seam production uses, and the same probes run against it.
// Everything the v1 rights govern must still be refused; only the rights that
// arrived in later ABIs are outside its reach.
func TestLandlockAFirstABIRulesetStillContains(t *testing.T) {
	probeReadOnlyRoots(t, "1")
}

func probeReadOnlyRoots(t *testing.T, forceABI string) {
	t.Helper()
	if os.Getenv("NVX_TEST_LANDLOCK_ABI_CHILD") == "1" {
		runReadOnlyRootProbeChild()
		return
	}
	if fd, err := landlockCreateRuleset(landlockHandledAccess()); err != nil {
		t.Skipf("landlock unavailable on this kernel: %v", err)
	} else {
		_ = syscall.Close(fd)
	}

	nvxHome := tempDir(t)
	runtime := filepath.Join(nvxHome, "versions", "node", "v1")
	if err := os.MkdirAll(filepath.Join(runtime, "emptydir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtime, "file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestLandlockReadOnlyRootsAreListableAndNotRemovable")
	cmd.Env = append(os.Environ(),
		"NVX_TEST_LANDLOCK_ABI_CHILD=1",
		"NVX_TEST_LANDLOCK_FORCE_ABI="+forceABI,
		"NVX_TEST_NVXHOME="+nvxHome,
		"NVX_TEST_GUEST="+tempDir(t),
		"NVX_TEST_WORK="+tempDir(t),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("probe child failed: %v\noutput:\n%s", err, out)
	}
	got := parseProbeResults(string(out))
	if msg, bad := got["SETUP_FAILED"]; bad {
		t.Fatalf("sandbox setup failed in child: %s", msg)
	}

	for _, tc := range []struct{ key, want, why string }{
		{"list_usr", "allowed", "a read-only root must be listable; a tool that enumerates a directory to find its binaries needs this"},
		{"list_runtime", "allowed", "the runtime tree must be listable for the same reason"},
		{"read_runtime_file", "allowed", "control: reading under a read-only root has always worked"},
		{"rmdir_runtime", "denied", "a contained process must not be able to remove directories from the nvx runtime tree"},
		{"unlink_runtime", "denied", "control: REMOVE_FILE was never granted, and must stay that way"},
		{"write_runtime", "denied", "control: a read-only root is not writable"},
	} {
		if got[tc.key] != tc.want {
			t.Errorf("%s: got %q, want %q -- %s", tc.key, got[tc.key], tc.want, tc.why)
		}
	}
}

func runReadOnlyRootProbeChild() {
	nvxHome := os.Getenv("NVX_TEST_NVXHOME")
	abi := landlockABIVersion()
	if forced := os.Getenv("NVX_TEST_LANDLOCK_FORCE_ABI"); forced != "" {
		if _, err := fmt.Sscan(forced, &abi); err != nil {
			fmt.Printf("SETUP_FAILED=bad forced ABI %q: %v\n", forced, err)
			return
		}
	}
	if err := applyLandlockSandboxForABI(abi, os.Getenv("NVX_TEST_GUEST"), os.Getenv("NVX_TEST_WORK"), nvxHome, nil, false); err != nil {
		fmt.Printf("SETUP_FAILED=%v\n", err)
		return
	}
	fmt.Printf("applied_abi=%d\n", abi)
	runtime := filepath.Join(nvxHome, "versions", "node", "v1")
	verdict := func(err error) string {
		if err == nil {
			return "allowed"
		}
		return "denied"
	}
	_, err := os.ReadDir("/usr")
	fmt.Printf("list_usr=%s\n", verdict(err))
	_, err = os.ReadDir(filepath.Join(nvxHome, "versions"))
	fmt.Printf("list_runtime=%s\n", verdict(err))
	_, err = os.ReadFile(filepath.Join(runtime, "file"))
	fmt.Printf("read_runtime_file=%s\n", verdict(err))
	fmt.Printf("rmdir_runtime=%s\n", verdict(os.Remove(filepath.Join(runtime, "emptydir"))))
	fmt.Printf("unlink_runtime=%s\n", verdict(os.Remove(filepath.Join(runtime, "file"))))
	fmt.Printf("write_runtime=%s\n", verdict(os.WriteFile(filepath.Join(runtime, "newfile"), []byte("x"), 0o600)))
}

// The handled set is capped to what the running kernel supports.
//
// A ruleset asking the kernel to handle a right it does not know is refused
// outright with EINVAL, and the code reported that as "kernel 5.13+ required".
// The mask it passed included TRUNCATE (ABI v3, Linux 6.2) and IOCTL_DEV (ABI
// v5, Linux 6.10), so the real floor was 6.10 while the message named 5.13.
// Every kernel in between -- Debian 12 on 6.1, RHEL 9 on 5.14, Ubuntu 22.04
// on 5.15 -- failed closed with advice pointing at the wrong thing. CI never
// noticed because its runner kernel is new enough to accept the whole mask.
//
// Rights the kernel does not handle are simply not restricted, which is the
// documented Landlock behaviour on older ABIs and the only correct answer:
// refusing to run is not more secure than running with what the kernel offers.
func TestLandlockHandledAccessIsCappedToTheKernelABI(t *testing.T) {
	v1 := uint64(1<<13) - 1 // EXECUTE .. MAKE_SYM
	for _, tc := range []struct {
		abi  int
		want uint64
	}{
		{1, v1},
		{2, v1 | landlockAccessFSRefer},
		{3, v1 | landlockAccessFSRefer | landlockAccessFSTruncate},
		{4, v1 | landlockAccessFSRefer | landlockAccessFSTruncate}, // v4 added network rights only
		{5, v1 | landlockAccessFSRefer | landlockAccessFSTruncate | landlockAccessFSIoctlDev},
		{7, v1 | landlockAccessFSRefer | landlockAccessFSTruncate | landlockAccessFSIoctlDev},
		{9, v1 | landlockAccessFSRefer | landlockAccessFSTruncate | landlockAccessFSIoctlDev}, // RESOLVE_UNIX deliberately not handled: the egress relay dials a UNIX socket after restrict_self
	} {
		if got := landlockHandledAccessForABI(tc.abi); got != tc.want {
			t.Errorf("ABI v%d: handled = %#x, want %#x", tc.abi, got, tc.want)
		}
	}
	// A version the kernel reports that is below anything usable is not a mask
	// at all; the caller treats it as unsupported.
	if got := landlockHandledAccessForABI(0); got != 0 {
		t.Errorf("ABI v0: handled = %#x, want 0", got)
	}
}
