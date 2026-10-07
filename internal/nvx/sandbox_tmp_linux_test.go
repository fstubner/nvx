//go:build linux

package nvx

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A contained process has a /tmp of its own, and it is the directory $TMPDIR
// names.
//
// The sandbox's root holds only what it is granted, and /tmp was never granted, so
// it did not exist. Anything that hard-codes it failed on its first write. Measured
// on Linux 6.18 with pnpm 12, which makes its store lock directory under /tmp:
// mkdir("/tmp") came back EACCES and the install stopped with
// ERR_PNPM_STORE_DIR_OPEN_OPERATION_LOCK. pnpm 9 to 11 never looked there.
//
// This runs the real supervisor with a guest home that has the tmp directory a
// real one has, and the contained target is this test binary. Each claim is
// asserted from inside and, for the file the target leaves behind, from outside.
//
// Unprivileged on Ubuntu 24.04 the mount namespace is refused and this skips. CI
// runs it again under sudo in the privileged step, where a skip fails the job.

const (
	tmpProbeRoleEnv   = "NVX_TEST_TMPPROBE_ROLE"
	tmpProbeGuestEnv  = "NVX_TEST_TMPPROBE_GUEST"
	tmpProbeWorkEnv   = "NVX_TEST_TMPPROBE_WORK"
	tmpProbeHomeEnv   = "NVX_TEST_TMPPROBE_NVXHOME"
	tmpProbeMarkerEnv = "NVX_TEST_TMPPROBE_MARKER"
	tmpProbeModeEnv   = "NVX_TEST_TMPPROBE_MODE"
	tmpProbeVictimEnv = "NVX_TEST_TMPPROBE_VICTIM"
	tmpProbeTestName  = "^TestContainedProcessHasAPrivateTmpThatTMPDIRNames$"
)

func TestContainedProcessHasAPrivateTmpThatTMPDIRNames(t *testing.T) {
	switch os.Getenv(tmpProbeRoleEnv) {
	case "supervisor":
		// The target starts with this process's environment, so the role changes here.
		_ = os.Setenv(tmpProbeRoleEnv, "target")
		roots := []string{filepath.Dir(os.Args[0])}
		if victim := os.Getenv(tmpProbeVictimEnv); victim != "" {
			// A directory the sandbox may read and not write, like nvx's runtimes.
			roots = append(roots, victim)
		}
		os.Exit(runLandlockExecChild(supervisorExecArgs{
			GuestHome:     os.Getenv(tmpProbeGuestEnv),
			WorkDir:       os.Getenv(tmpProbeWorkEnv),
			NvxHome:       os.Getenv(tmpProbeHomeEnv),
			NetworkMode:   "open",
			ReadExecRoots: roots,
			CmdPath:       os.Args[0],
			CmdArgs:       []string{"-test.run=" + tmpProbeTestName},
		}))
	case "target":
		if os.Getenv(tmpProbeModeEnv) == "symlink" {
			runTmpSymlinkProbe()
		} else {
			runTmpProbe()
		}
		os.Exit(0)
	}

	if fd, err := landlockCreateRuleset(landlockHandledAccess()); err != nil {
		t.Skipf("landlock unavailable on this kernel: %v", err)
	} else {
		_ = syscall.Close(fd)
	}
	attr := supervisorSysProcAttr("open")
	requireNamespaceSupport(t, attr)

	guest := tempDir(t)
	guestTmp := filepath.Join(guest, "tmp")
	if err := os.MkdirAll(guestTmp, 0o700); err != nil {
		t.Fatal(err)
	}
	// The host's own /tmp, which the sandbox must not show.
	marker, err := os.CreateTemp("/tmp", "nvx-host-marker-")
	if err != nil {
		t.Skipf("no writable /tmp on this host: %v", err)
	}
	_ = marker.Close()
	defer os.Remove(marker.Name())

	cmd := exec.Command(os.Args[0], "-test.run="+tmpProbeTestName)
	cmd.Env = append(os.Environ(),
		tmpProbeRoleEnv+"=supervisor",
		tmpProbeGuestEnv+"="+guest,
		tmpProbeWorkEnv+"="+tempDir(t),
		tmpProbeHomeEnv+"="+tempDir(t),
		tmpProbeMarkerEnv+"="+filepath.Base(marker.Name()),
		// What scrubEnvironmentAllowing sets for a contained run.
		"TMPDIR="+guestTmp,
	)
	cmd.SysProcAttr = attr
	out, err := cmd.CombinedOutput()
	if strings.Contains(string(out), "unshare mount namespace") && strings.Contains(string(out), "operation not permitted") {
		t.Skipf("this host refuses a mount namespace inside an unprivileged user namespace "+
			"(Ubuntu 24.04 AppArmor), and the privileged CI step covers it:\n%s", out)
	}
	if err != nil {
		t.Fatalf("contained probe failed: %v\noutput:\n%s", err, out)
	}
	got := parseProbeResults(string(out))

	for _, want := range []struct{ key, val, why string }{
		{"tmp_write", "ok", "a file under /tmp must be writable, or a tool that hard-codes /tmp fails on its first write"},
		{"tmp_mkdir", "ok", "nested directories under /tmp must be creatable, as pnpm 12 makes its lock directory there"},
		{"tmp_same_dir", "true", "a file written under $TMPDIR must appear under /tmp, so the two paths are one place"},
		{"host_marker_visible", "false", "the sandbox's /tmp must not be the host's"},
	} {
		if got[want.key] != want.val {
			t.Errorf("%s = %q, want %q: %s\nfull output:\n%s", want.key, got[want.key], want.val, want.why, out)
		}
	}

	// From outside, what the target wrote under /tmp is in the guest home, and the
	// host's /tmp has none of it.
	name := tmpProbeName(marker.Name())
	if _, err := os.Stat(filepath.Join(guestTmp, name)); err != nil {
		t.Errorf("the file written to /tmp is not in the guest home's tmp directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join("/tmp", name)); err == nil {
		_ = os.Remove(filepath.Join("/tmp", name))
		t.Errorf("the file written to /tmp reached the host's /tmp")
	}
}

// runTmpProbe runs as the contained target.
func runTmpProbe() {
	name := tmpProbeName(os.Getenv(tmpProbeMarkerEnv))
	report := func(key string, err error) {
		if err != nil {
			fmt.Printf("%s=%v\n", key, err)
			return
		}
		fmt.Printf("%s=ok\n", key)
	}
	report("tmp_write", os.WriteFile("/tmp/"+name, []byte("tmp"), 0o600))
	report("tmp_mkdir", os.MkdirAll("/tmp/a/b", 0o755))

	// Written under $TMPDIR and read back under /tmp.
	envErr := os.WriteFile(filepath.Join(os.Getenv("TMPDIR"), name+"-env"), []byte("env"), 0o600)
	b, readErr := os.ReadFile("/tmp/" + name + "-env")
	fmt.Printf("tmp_same_dir=%v\n", envErr == nil && readErr == nil && string(b) == "env")

	_, err := os.Stat(filepath.Join("/tmp", os.Getenv(tmpProbeMarkerEnv)))
	fmt.Printf("host_marker_visible=%v\n", err == nil)
}

// tmpProbeName is the file the target writes under /tmp. It is named after the
// host marker, which is unique to this run, so a leftover from another run cannot
// pass for it.
func tmpProbeName(marker string) string {
	return filepath.Base(marker) + "-probe"
}

// A guest home whose tmp directory is a symbolic link does not hand the link's
// target to the contained process.
//
// A trusted tool keeps its guest home between runs, and the contained process
// can write all of it. One run could replace tmp with a link to a directory the
// sandbox may read and not write, such as nvx's runtimes, and the next run would
// grant that directory in full, because nvx opens the path with its own rights.
// The directory has to be the guest home's own, so a link is refused.
func TestContainedProcessDoesNotGetASymlinkedTmpTarget(t *testing.T) {
	if fd, err := landlockCreateRuleset(landlockHandledAccess()); err != nil {
		t.Skipf("landlock unavailable on this kernel: %v", err)
	} else {
		_ = syscall.Close(fd)
	}
	attr := supervisorSysProcAttr("open")
	requireNamespaceSupport(t, attr)

	guest, victim := tempDir(t), tempDir(t)
	if err := os.Symlink(victim, filepath.Join(guest, "tmp")); err != nil {
		t.Fatal(err)
	}
	planted := filepath.Join(victim, "planted")

	cmd := exec.Command(os.Args[0], "-test.run="+tmpProbeTestName)
	cmd.Env = append(os.Environ(),
		tmpProbeRoleEnv+"=supervisor",
		tmpProbeModeEnv+"=symlink",
		tmpProbeGuestEnv+"="+guest,
		tmpProbeWorkEnv+"="+tempDir(t),
		tmpProbeHomeEnv+"="+tempDir(t),
		tmpProbeVictimEnv+"="+victim,
	)
	cmd.SysProcAttr = attr
	out, err := cmd.CombinedOutput()
	if strings.Contains(string(out), "unshare mount namespace") && strings.Contains(string(out), "operation not permitted") {
		t.Skipf("this host refuses a mount namespace inside an unprivileged user namespace "+
			"(Ubuntu 24.04 AppArmor), and the privileged CI step covers it:\n%s", out)
	}
	if err != nil {
		t.Fatalf("contained probe failed: %v\noutput:\n%s", err, out)
	}
	if _, err := os.Stat(planted); err == nil {
		t.Errorf("the contained process wrote into %s through a symbolic link at the guest home's tmp\noutput:\n%s", victim, out)
	}
}

// runTmpSymlinkProbe tries to write to the directory the link names, by its own
// path and through /tmp. The sandbox may read it and not write it, and with the
// link refused both writes fail.
func runTmpSymlinkProbe() {
	err := os.WriteFile(filepath.Join(os.Getenv(tmpProbeVictimEnv), "planted"), []byte("x"), 0o600)
	fmt.Printf("victim_write=%v\n", err)
	err = os.WriteFile("/tmp/planted", []byte("x"), 0o600)
	fmt.Printf("tmp_write=%v\n", err)
}
