//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

// TestLandlockSandboxCanStartACommand is the end-to-end form of F50: it applies
// the real sandbox (including landlock_restrict_self) and then execs a command.
// This is the assertion the project never had -- no test called any launch path,
// which is why a defect that killed every Linux launch shipped undetected.
//
// Runs in a subprocess because landlock_restrict_self is irreversible for the
// calling thread.
func TestLandlockSandboxCanStartACommand(t *testing.T) {
	if os.Getenv("NVX_TEST_LANDLOCK_CHILD") == "1" {
		if err := applyLandlockSandbox(
			os.Getenv("NVX_TEST_GUEST"), os.Getenv("NVX_TEST_WORK"), os.Getenv("NVX_TEST_NVXHOME"),
		); err != nil {
			fmt.Fprintf(os.Stderr, "SANDBOX_SETUP_FAILED: %v\n", err)
			os.Exit(2)
		}
		// A contained process must still be able to read the device files the
		// ruleset grants...
		f, err := os.Open("/dev/null")
		if err != nil {
			fmt.Fprintf(os.Stderr, "DEVNULL_READ_FAILED: %v\n", err)
			os.Exit(3)
		}
		_ = f.Close()
		// ...and actually execute a command.
		out, err := exec.Command("/bin/echo", "CONTAINED_OK").Output()
		if err != nil {
			fmt.Fprintf(os.Stderr, "EXEC_FAILED: %v\n", err)
			os.Exit(4)
		}
		fmt.Print(string(out))
		os.Exit(0)
	}

	if fd, err := landlockCreateRuleset(landlockAccessFull); err != nil {
		t.Skipf("landlock unavailable on this kernel: %v", err)
	} else {
		_ = syscall.Close(fd)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestLandlockSandboxCanStartACommand")
	cmd.Env = append(os.Environ(),
		"NVX_TEST_LANDLOCK_CHILD=1",
		"NVX_TEST_GUEST="+tempDir(t),
		"NVX_TEST_WORK="+tempDir(t),
		"NVX_TEST_NVXHOME="+tempDir(t),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("contained command failed: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(string(out), "CONTAINED_OK") {
		t.Fatalf("expected the contained command to run; output:\n%s", out)
	}
}

// TestLandlockAcceptsEveryConfiguredReadRoot pins F50: the sandbox adds
// /dev/null, /dev/urandom, /dev/random and /dev/zero to its read-only root set,
// and Landlock rejects a directory-only right (LANDLOCK_ACCESS_FS_READ_DIR) on a
// character device with EINVAL. applyLandlockSandbox treats that as fatal, so
// every Linux sandbox launch died on a path present on every Linux system.
//
// The assertion is made against the real kernel via a real ruleset fd, but
// deliberately never calls landlock_restrict_self -- doing so would irreversibly
// restrict the test process itself.
func TestLandlockAcceptsEveryConfiguredReadRoot(t *testing.T) {
	fd, err := landlockCreateRuleset(landlockAccessFull)
	if err != nil {
		t.Skipf("landlock unavailable on this kernel: %v", err)
	}
	defer syscall.Close(fd)

	guestHome := tempDir(t)
	workDir := tempDir(t)
	nvxHome := tempDir(t)

	for _, rule := range landlockReadOnlyRules(nvxHome) {
		if err := landlockAddRule(fd, rule.access, rule.path); err != nil {
			t.Errorf("kernel rejected read rule for %q (access %#x): %v", rule.path, rule.access, err)
		}
	}

	// The writable roots must remain acceptable too -- both are directories, so
	// the full access mask is valid for them.
	for _, p := range []string{guestHome, workDir} {
		if err := landlockAddRule(fd, landlockAccessFull, p); err != nil {
			t.Errorf("kernel rejected write rule for %q: %v", p, err)
		}
	}
}

// TestLandlockReadOnlyRulesDropDirRightsOnDeviceFiles states the specific
// invariant behind the fix: a rule for a non-directory must not carry
// READ_DIR, because Landlock validates rights against the inode type.
func TestLandlockReadOnlyRulesDropDirRightsOnDeviceFiles(t *testing.T) {
	for _, rule := range landlockReadOnlyRules("") {
		if rule.path != "/dev/null" {
			continue
		}
		if rule.access&landlockAccessFSReadDir != 0 {
			t.Fatalf("/dev/null rule carries READ_DIR (access %#x); Landlock rejects dir rights on a character device", rule.access)
		}
		if rule.access&landlockAccessFSReadFile == 0 {
			t.Fatalf("/dev/null rule must still grant READ_FILE, got %#x", rule.access)
		}
		return
	}
	t.Fatal("/dev/null missing from the read-only root set")
}
