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
	"unsafe"
)

// A contained process may read the repository's git metadata and may not write
// it, while the rest of the project stays writable.
//
// These run the real supervisor, runLandlockExecChild, in the namespaces
// platformLaunchNative creates for it, and the contained target is this test
// binary. So the mount, the capability drop and the Landlock ruleset are the
// ones a contained install gets, in the order it gets them.
//
// Unprivileged on Ubuntu 24.04 the mount namespace is refused (see
// TestContainedProcessReadsItsOwnProcAndNotTheHosts) and these skip. CI runs
// them again under sudo in the privileged step, where a skip fails the job.

const gitProbeRoleEnv = "NVX_TEST_GITRO_ROLE"

// TestGitMetadataReadOnlyDir: .git is a directory at the project root.
func TestGitMetadataReadOnlyDir(t *testing.T) {
	if runGitProbeRole("TestGitMetadataReadOnlyDir") {
		return
	}
	work := gitFixtureProject(t)
	got := runContainedGitProbe(t, "TestGitMetadataReadOnlyDir", work, "dir")
	requireGitProbe(t, got, map[string]string{
		"hook_create":   "denied",
		"hook_modify":   "denied",
		"config_write":  "denied",
		"gitdir_rename": "denied",
		"config_read":   "ok",
		"hooks_list":    "ok",
		"pkg_write":     "ok",
		"nm_write":      "ok",
	})
	requireGitUntouched(t, filepath.Join(work, ".git"))
	if b, err := os.ReadFile(filepath.Join(work, "package.json")); err != nil || !strings.Contains(string(b), "rewritten") {
		t.Errorf("package.json was not rewritten by the contained process (err %v): the positive control failed", err)
	}
}

// TestGitMetadataReadOnlyLinkedGitDir: .git is a file naming a git directory
// inside the project, as `git init --separate-git-dir` leaves it. Both the file
// and the directory it names must be read-only.
func TestGitMetadataReadOnlyLinkedGitDir(t *testing.T) {
	if runGitProbeRole("TestGitMetadataReadOnlyLinkedGitDir") {
		return
	}
	work := tempDir(t)
	meta := filepath.Join(work, "repo-meta")
	writeGitDir(t, meta)
	if err := os.WriteFile(filepath.Join(work, ".git"), []byte("gitdir: repo-meta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "package.json"), []byte(`{"name":"fixture"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := runContainedGitProbe(t, "TestGitMetadataReadOnlyLinkedGitDir", work, "linked")
	requireGitProbe(t, got, map[string]string{
		"dotgit_file_write": "denied",
		"hook_create":       "denied",
		"config_write":      "denied",
		"config_read":       "ok",
		"pkg_write":         "ok",
	})
	requireGitUntouched(t, meta)
	if b, _ := os.ReadFile(filepath.Join(work, ".git")); string(b) != "gitdir: repo-meta\n" {
		t.Errorf("the .git file was rewritten: %q", b)
	}
}

// TestGitMetadataReadOnlyCannotBeUndone: the contained process tries to make
// the read-only mount writable again before writing. Landlock refuses mount,
// umount and remount; mount_setattr is tried too, and CAP_SYS_ADMIN is dropped
// from the target so that none of them can succeed whatever Landlock covers.
func TestGitMetadataReadOnlyCannotBeUndone(t *testing.T) {
	if runGitProbeRole("TestGitMetadataReadOnlyCannotBeUndone") {
		return
	}
	work := gitFixtureProject(t)
	got := runContainedGitProbe(t, "TestGitMetadataReadOnlyCannotBeUndone", work, "undo")
	t.Logf("remount=%s umount=%s setattr=%s", got["remount"], got["umount"], got["setattr"])
	requireGitProbe(t, got, map[string]string{
		"config_after_undo": "denied",
		"hook_after_undo":   "denied",
		"pkg_write":         "ok",
	})
	requireGitUntouched(t, filepath.Join(work, ".git"))
}

const gitFixtureConfig = "[core]\n\trepositoryformatversion = 0\n"

func writeGitDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"config":                  gitFixtureConfig,
		"HEAD":                    "ref: refs/heads/main\n",
		"hooks/pre-commit.sample": "#!/bin/sh\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func gitFixtureProject(t *testing.T) string {
	t.Helper()
	work := tempDir(t)
	writeGitDir(t, filepath.Join(work, ".git"))
	if err := os.WriteFile(filepath.Join(work, "package.json"), []byte(`{"name":"fixture"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return work
}

// runContainedGitProbe launches the supervisor for work and returns what the
// contained target reported.
func runContainedGitProbe(t *testing.T, name, work, mode string) map[string]string {
	t.Helper()
	if fd, err := landlockCreateRuleset(landlockHandledAccess()); err != nil {
		t.Skipf("landlock unavailable on this kernel: %v", err)
	} else {
		_ = syscall.Close(fd)
	}
	attr := supervisorSysProcAttr("open")
	requireNamespaceSupport(t, attr)

	cmd := exec.Command(os.Args[0], "-test.run=^"+name+"$")
	cmd.Env = append(os.Environ(),
		gitProbeRoleEnv+"=supervisor",
		"NVX_TEST_GITRO_WORK="+work,
		"NVX_TEST_GITRO_GUEST="+tempDir(t),
		"NVX_TEST_GITRO_NVXHOME="+tempDir(t),
		"NVX_TEST_GITRO_MODE="+mode,
	)
	cmd.SysProcAttr = attr
	out, err := cmd.CombinedOutput()
	if strings.Contains(string(out), "unshare mount namespace") && strings.Contains(string(out), "operation not permitted") {
		t.Skipf("this host refuses a mount namespace inside an unprivileged user namespace "+
			"(Ubuntu 24.04 AppArmor); the privileged CI step covers it:\n%s", out)
	}
	if err != nil {
		t.Fatalf("contained probe failed: %v\noutput:\n%s", err, out)
	}
	t.Logf("contained probe output:\n%s", out)
	return parseProbeResults(string(out))
}

func requireGitProbe(t *testing.T, got map[string]string, want map[string]string) {
	t.Helper()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

// requireGitUntouched checks on disk, outside the sandbox, that nothing the
// target reported as denied landed anyway.
func requireGitUntouched(t *testing.T, gitDir string) {
	t.Helper()
	if b, err := os.ReadFile(filepath.Join(gitDir, "config")); err != nil || string(b) != gitFixtureConfig {
		t.Errorf("%s/config changed or vanished (err %v): %q", gitDir, err, b)
	}
	if _, err := os.Stat(filepath.Join(gitDir, "hooks", "pre-commit")); err == nil {
		t.Errorf("%s/hooks/pre-commit exists: a contained process created a hook", gitDir)
	}
}

// runGitProbeRole runs the supervisor or the target half of a probe when this
// process is one, and reports whether it was.
func runGitProbeRole(name string) bool {
	switch os.Getenv(gitProbeRoleEnv) {
	case "supervisor":
		// The supervisor execs the target with its own environment, so the role
		// changes here, before runLandlockExecChild reads it.
		_ = os.Setenv(gitProbeRoleEnv, "target")
		os.Exit(runLandlockExecChild(supervisorExecArgs{
			GuestHome:     os.Getenv("NVX_TEST_GITRO_GUEST"),
			WorkDir:       os.Getenv("NVX_TEST_GITRO_WORK"),
			NvxHome:       os.Getenv("NVX_TEST_GITRO_NVXHOME"),
			NetworkMode:   "open",
			ReadExecRoots: []string{filepath.Dir(os.Args[0])},
			CmdPath:       os.Args[0],
			CmdArgs:       []string{"-test.run=^" + name + "$"},
		}))
	case "target":
		runGitProbeTarget(os.Getenv("NVX_TEST_GITRO_WORK"), os.Getenv("NVX_TEST_GITRO_MODE"))
		os.Exit(0)
	}
	return false
}

func runGitProbeTarget(work, mode string) {
	report := func(key string, err error) {
		if err != nil {
			fmt.Printf("%s=denied\n", key)
			return
		}
		fmt.Printf("%s=ok\n", key)
	}
	write := func(p string) error { return os.WriteFile(p, []byte("rewritten\n"), 0o644) }
	appendTo := func(p string) error {
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = f.WriteString("[core]\n\thooksPath = elsewhere\n")
		return err
	}

	gitDir := filepath.Join(work, ".git")
	if mode == "linked" {
		report("dotgit_file_write", write(gitDir))
		gitDir = filepath.Join(work, "repo-meta")
	}

	if mode == "undo" {
		report("remount", syscall.Mount("", gitDir, "", syscall.MS_BIND|syscall.MS_REMOUNT, ""))
		report("umount", syscall.Unmount(gitDir, syscall.MNT_DETACH))
		report("setattr", clearMountReadOnly(gitDir))
		report("config_after_undo", appendTo(filepath.Join(gitDir, "config")))
		report("hook_after_undo", write(filepath.Join(gitDir, "hooks", "pre-commit")))
		report("pkg_write", write(filepath.Join(work, "package.json")))
		return
	}

	report("hook_create", write(filepath.Join(gitDir, "hooks", "pre-commit")))
	report("hook_modify", appendTo(filepath.Join(gitDir, "hooks", "pre-commit.sample")))
	report("config_write", appendTo(filepath.Join(gitDir, "config")))
	if mode == "dir" {
		report("gitdir_rename", os.Rename(gitDir, filepath.Join(work, ".git-moved")))
	}
	_, err := os.ReadFile(filepath.Join(gitDir, "config"))
	report("config_read", err)
	_, err = os.ReadDir(filepath.Join(gitDir, "hooks"))
	report("hooks_list", err)
	report("pkg_write", write(filepath.Join(work, "package.json")))
	nm := filepath.Join(work, "node_modules", "dep")
	err = os.MkdirAll(nm, 0o755)
	if err == nil {
		err = write(filepath.Join(nm, "index.js"))
	}
	report("nm_write", err)
}

// clearMountReadOnly asks mount_setattr(2) to clear MOUNT_ATTR_RDONLY on path.
// The syscall number is 442 on both amd64 and arm64.
func clearMountReadOnly(path string) error {
	type mountAttr struct {
		attrSet, attrClr, propagation, usernsFD uint64
	}
	const (
		sysMountSetattr = 442
		atFDCWD         = -100
		mountAttrRdonly = 0x1
	)
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return err
	}
	attr := mountAttr{attrClr: mountAttrRdonly}
	fd := atFDCWD
	_, _, errno := syscall.Syscall6(sysMountSetattr, uintptr(fd), uintptr(unsafe.Pointer(p)), 0,
		uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0)
	if errno != 0 {
		return errno
	}
	return nil
}
