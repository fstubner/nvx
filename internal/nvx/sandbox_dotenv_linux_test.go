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

// A contained process cannot read the project's dotenv files, cannot move them
// to a name nothing covers, and cannot undo the mount that hides them, while
// templates and the rest of the project stay readable and writable.
//
// Like the .git tests, this runs the real supervisor in the namespaces
// platformLaunchNative creates, with this test binary as the target. It skips
// where the mount namespace is refused, and CI runs it again in the privileged
// step, where a skip fails the job.

const dotenvProbeRoleEnv = "NVX_TEST_DOTENV_ROLE"

const dotenvProbeSecret = "API_KEY=DOTENV-SECRET-DO-NOT-LEAK\n"

func TestContainedProcessCannotReadDotenvFiles(t *testing.T) {
	if runDotenvProbeRole("TestContainedProcessCannotReadDotenvFiles") {
		return
	}
	work := tempDir(t)
	for name, body := range map[string]string{
		".env":                dotenvProbeSecret,
		"sub/.env.local":      dotenvProbeSecret,
		"linked/.env.prod":    dotenvProbeSecret,
		".env.example":        "API_KEY=\n",
		"package.json":        `{"name":"fixture"}` + "\n",
		"node_modules/p/.env": "PACKAGE=1\n",
	} {
		p := filepath.Join(work, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A link named .env leads to a file named otherwise. The file it names is
	// the one that holds the secret, so that is the one covered.
	if err := os.Rename(filepath.Join(work, "linked", ".env.prod"), filepath.Join(work, "linked", "values")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("values", filepath.Join(work, "linked", ".env")); err != nil {
		t.Fatal(err)
	}

	got := runContainedDotenvProbe(t, "TestContainedProcessCannotReadDotenvFiles", work)
	requireGitProbe(t, got, map[string]string{
		"read_root":        "denied",
		"read_sub":         "denied",
		"read_link":        "denied",
		"read_link_target": "denied",
		"write_root":       "denied",
		"chmod_root":       "denied",
		"rename_root":      "denied",
		"link_root":        "denied",
		"umount_root":      "denied",
		"setattr_root":     "denied",
		"read_after_undo":  "denied",
		"read_template":    "ok",
		"read_package":     "ok",
		"read_nm":          "ok",
		"write_package":    "ok",
		"create_new":       "ok",
	})
	for _, name := range []string{".env", "sub/.env.local", "linked/values"} {
		if b, err := os.ReadFile(filepath.Join(work, filepath.FromSlash(name))); err != nil || string(b) != dotenvProbeSecret {
			t.Errorf("%s changed or vanished outside the sandbox (err %v): %q", name, err, b)
		}
	}
	if _, err := os.Stat(filepath.Join(work, "moved.txt")); err == nil {
		t.Error("moved.txt exists: a contained process renamed .env")
	}
}

func runContainedDotenvProbe(t *testing.T, name, work string) map[string]string {
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
		dotenvProbeRoleEnv+"=supervisor",
		"NVX_TEST_DOTENV_WORK="+work,
		"NVX_TEST_DOTENV_GUEST="+tempDir(t),
		"NVX_TEST_DOTENV_NVXHOME="+tempDir(t),
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

func runDotenvProbeRole(name string) bool {
	switch os.Getenv(dotenvProbeRoleEnv) {
	case "supervisor":
		_ = os.Setenv(dotenvProbeRoleEnv, "target")
		os.Exit(runLandlockExecChild(supervisorExecArgs{
			GuestHome:     os.Getenv("NVX_TEST_DOTENV_GUEST"),
			WorkDir:       os.Getenv("NVX_TEST_DOTENV_WORK"),
			NvxHome:       os.Getenv("NVX_TEST_DOTENV_NVXHOME"),
			NetworkMode:   "open",
			ReadExecRoots: []string{filepath.Dir(os.Args[0])},
			CmdPath:       os.Args[0],
			CmdArgs:       []string{"-test.run=^" + name + "$"},
		}))
	case "target":
		runDotenvProbeTarget(os.Getenv("NVX_TEST_DOTENV_WORK"))
		os.Exit(0)
	}
	return false
}

func runDotenvProbeTarget(work string) {
	at := func(rel string) string { return filepath.Join(work, filepath.FromSlash(rel)) }
	report := func(key string, err error) {
		if err != nil {
			fmt.Printf("%s=denied\n%s.err=%v\n", key, key, err)
			return
		}
		fmt.Printf("%s=ok\n", key)
	}
	// A read that succeeds on a mask is still a refusal of the secret, but not
	// the one wanted: it means the mask's mode did not hold.
	read := func(key, rel string) {
		b, err := os.ReadFile(at(rel))
		switch {
		case err != nil:
			report(key, err)
		case strings.Contains(string(b), "DOTENV-SECRET"):
			fmt.Printf("%s=secret\n", key)
		default:
			fmt.Printf("%s=ok\n", key)
		}
	}

	read("read_root", ".env")
	read("read_sub", "sub/.env.local")
	read("read_link", "linked/.env")
	read("read_link_target", "linked/values")
	read("read_template", ".env.example")
	read("read_package", "package.json")
	read("read_nm", "node_modules/p/.env")

	f, err := os.OpenFile(at(".env"), os.O_WRONLY|os.O_APPEND, 0)
	if err == nil {
		_, err = f.WriteString("INJECTED=1\n")
		f.Close()
	}
	report("write_root", err)
	report("chmod_root", os.Chmod(at(".env"), 0o644))
	report("rename_root", os.Rename(at(".env"), at("moved.txt")))
	report("link_root", os.Link(at(".env"), at("linked.txt")))
	report("umount_root", syscall.Unmount(at(".env"), syscall.MNT_DETACH))
	report("setattr_root", clearMountReadOnly(at(".env")))
	read("read_after_undo", ".env")

	report("write_package", os.WriteFile(at("package.json"), []byte(`{"name":"rewritten"}`+"\n"), 0o644))
	// Not covered: it did not exist at launch. Pinned so the documents that say
	// so change with it.
	err = os.MkdirAll(at("fresh"), 0o755)
	if err == nil {
		err = os.WriteFile(at("fresh/.env"), []byte("X=1\n"), 0o644)
	}
	report("create_new", err)
}
