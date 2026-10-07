//go:build linux

package nvx

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The contained process runs as the user who started nvx and holds no
// capabilities, and it can still listen on a port below 1024 in its own network
// namespace, as it could when it ran as root there.
//
// Until 2026-10-07 the supervisor mapped the user to root and the target ran as
// that root, holding every capability but the four the supervisor dropped. A
// tar library restores an archive's owners when it runs as root, and with one
// id mapped every other owner is EINVAL. That is how a contained `npm install
// sqlite3` failed, on its prebuilt binary's archive owned by 1001.
//
// The supervisor is the real one, with this test binary as its target. CI runs
// it again as root in the privileged step, where the user is root in the
// namespace too, and the capability sets still have to be empty.
func TestContainedProcessRunsAsTheUserWithNoCapabilities(t *testing.T) {
	const name = "TestContainedProcessRunsAsTheUserWithNoCapabilities"
	switch {
	case os.Getenv("NVX_TEST_IDENTITY_TARGET") == "1":
		runIdentityProbe()
		return
	case os.Getenv("NVX_TEST_IDENTITY_SUPERVISOR") == "1":
		exe, err := os.Executable()
		if err != nil {
			fmt.Printf("setup_failed=%v\n", err)
			os.Exit(1)
		}
		// Inherited by the target, which is how it knows to probe.
		_ = os.Setenv("NVX_TEST_IDENTITY_TARGET", "1")
		os.Exit(runLandlockExecChild(supervisorExecArgs{
			GuestHome:     os.Getenv("NVX_TEST_GUEST"),
			WorkDir:       os.Getenv("NVX_TEST_WORK"),
			NvxHome:       os.Getenv("NVX_TEST_NVXHOME"),
			NetworkMode:   "proxy",
			ReadExecRoots: []string{filepath.Dir(exe)},
			CmdPath:       exe,
			CmdArgs:       []string{"-test.run=^" + name + "$"},
		}))
	}

	if _, err := exec.LookPath("ip"); err != nil {
		t.Skip("iproute2 not installed; bringUpLoopback needs `ip`")
	}
	if fd, err := landlockCreateRuleset(landlockHandledAccess()); err != nil {
		t.Skipf("landlock unavailable on this kernel: %v", err)
	} else {
		_ = syscall.Close(fd)
	}
	attr := supervisorSysProcAttr("proxy")
	requireLoopbackControl(t, attr)

	cmd := exec.Command(os.Args[0], "-test.run=^"+name+"$")
	cmd.Env = append(os.Environ(),
		"NVX_TEST_IDENTITY_SUPERVISOR=1",
		"NVX_TEST_GUEST="+tempDir(t),
		"NVX_TEST_WORK="+tempDir(t),
		"NVX_TEST_NVXHOME="+tempDir(t),
	)
	cmd.SysProcAttr = attr
	out, err := cmd.CombinedOutput()
	skipWithoutMountNamespace(t, out)
	if err != nil {
		t.Fatalf("supervisor failed: %v\noutput:\n%s", err, out)
	}
	got := parseProbeResults(string(out))

	const none = "0000000000000000"
	for _, want := range []struct{ key, val string }{
		{"uid", strconv.Itoa(os.Getuid())},
		{"gid", strconv.Itoa(os.Getgid())},
		{"CapInh", none},
		{"CapPrm", none},
		{"CapEff", none},
		{"CapBnd", none},
		{"CapAmb", none},
		{"low_port", "ok"},
	} {
		if got[want.key] != want.val {
			t.Errorf("%s = %q, want %q\noutput:\n%s", want.key, got[want.key], want.val, out)
		}
	}
}

// runIdentityProbe runs as the contained target. It reports its ids, its
// capability sets and whether it can listen on port 80.
func runIdentityProbe() {
	fmt.Printf("uid=%d\ngid=%d\n", os.Getuid(), os.Getgid())
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		fmt.Printf("status_error=%v\n", err)
	}
	for _, line := range strings.Split(string(status), "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.HasPrefix(k, "Cap") {
			fmt.Printf("%s=%s\n", k, strings.TrimSpace(v))
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:80")
	if err != nil {
		fmt.Printf("low_port=%v\n", err)
		return
	}
	_ = ln.Close()
	fmt.Println("low_port=ok")
}

// A contained process cannot create a user namespace of its own, so it cannot
// use a nested user+mount namespace to escape the run-time .env watcher.
//
// Without denyNestedUserNamespaces a contained process clones a child into a new
// user+mount namespace, which freezes a private copy of the mount tree. A .env
// created during the run is masked only in the original namespace, so it is read
// in the clear through the copy. The launch's masks still hold because they
// predate the copy, which is why this uses a .env created after launch. The
// namespace is made with clone (a fresh process), not an in-place
// unshare(CLONE_NEWUSER), which a multithreaded program such as this one, Node
// or Chromium's own sandbox cannot do.
//
// Like the other dotenv probes this runs the real supervisor with the test
// binary as the target. It skips where the mount namespace is refused, and the
// privileged CI step runs it again, where a skip fails the job.
func TestContainedProcessCannotCreateUserNamespace(t *testing.T) {
	const name = "TestContainedProcessCannotCreateUserNamespace"
	if runDotenvProbeRole(name, runUsernsProbeTarget) {
		return
	}

	base := tempDir(t)
	proj := filepath.Join(base, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := dotenvProbeCommand(t, name, proj)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	// Create the runtime .env only after the probe has tried to make its nested
	// namespace, so this is the "created during the run" case the bypass targets.
	awaitProbeReady(t, proj, 1, exited, &out)
	writeOrKill(t, cmd, exited, filepath.Join(proj, ".env"), dotenvProbeSecret)
	writeOrKill(t, cmd, exited, filepath.Join(proj, "done-1"), "")

	awaitProbeExit(t, cmd, exited, &out)
	t.Logf("contained probe output:\n%s", out.String())

	got := parseProbeResults(out.String())
	if got["userns"] != "denied" {
		t.Errorf("userns = %q, want %q: the contained process created a user namespace", got["userns"], "denied")
	}
	if got["nested_read"] != "denied" {
		t.Errorf("nested_read = %q, want %q: a .env created during the run was read through a nested user+mount namespace", got["nested_read"], "denied")
	}
}

// usernsChildEnv marks the grandchild: the process the target clones into a new
// user+mount namespace.
const usernsChildEnv = "NVX_TEST_USERNS_CHILD"

// runUsernsProbeTarget is the contained side. It clones a grandchild into a
// nested user+mount namespace before the .env exists; with the fix the clone is
// refused and the target itself confirms the watcher masks the file.
func runUsernsProbeTarget(work string) {
	if os.Getenv(usernsChildEnv) != "" {
		// The grandchild, already in the nested user+mount namespace. Its mount
		// tree was copied before the .env and its mask existed, so the watcher's
		// later mask cannot reach it.
		fmt.Println("userns=ok")
		fmt.Printf("nested_read=%s\n", usernsHandshakeAndRead(work))
		return
	}

	// Clone a fresh process into a new user+mount namespace. clone, not an
	// in-place unshare(CLONE_NEWUSER), because a multithreaded process cannot
	// unshare a user namespace, while clone (and Chromium's sandbox) can. No uid
	// map is set: writing one needs /proc, which Landlock keeps read-only, and the
	// read works without it anyway (the file's owner is the namespace's creator).
	child := exec.Command(os.Args[0], "-test.run=^TestContainedProcessCannotCreateUserNamespace$")
	child.Env = append(os.Environ(), usernsChildEnv+"=1")
	child.Dir = work
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	child.SysProcAttr = &syscall.SysProcAttr{Cloneflags: syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS}
	if err := child.Start(); err != nil {
		// The namespace was refused, so there is no copy to escape into. Confirm
		// the file is masked in our own namespace, where the watcher covers it.
		fmt.Println("userns=denied")
		fmt.Printf("nested_read=%s\n", usernsHandshakeAndRead(work))
		return
	}
	_ = child.Wait()
}

// usernsHandshakeAndRead signals the driver to create the .env, waits for it, and
// reads it until the read is refused or the deadline passes. In a frozen copy the
// watcher's mask is absent and the secret stays readable; in the original
// namespace the mask lands and the read is denied.
func usernsHandshakeAndRead(work string) string {
	at := func(rel string) string { return filepath.Join(work, filepath.FromSlash(rel)) }
	_ = os.WriteFile(at("ready-1"), nil, 0o644)
	awaitFileExists(at("done-1"), 30*time.Second)

	const marker = "DOTENV-SECRET"
	result := "timeout"
	for deadline := time.Now().Add(6 * time.Second); time.Now().Before(deadline); {
		b, err := os.ReadFile(at(".env"))
		if errors.Is(err, fs.ErrPermission) {
			return "denied"
		}
		if err == nil && strings.Contains(string(b), marker) {
			result = "secret"
		}
		time.Sleep(5 * time.Millisecond)
	}
	return result
}

// awaitProbeReady waits for the probe to create proj/ready-N, skipping on a host
// that refuses the mount namespace and failing if the probe exits first.
func awaitProbeReady(t *testing.T, proj string, n int, exited <-chan error, out *bytes.Buffer) {
	t.Helper()
	ready := filepath.Join(proj, fmt.Sprintf("ready-%d", n))
	for deadline := time.Now().Add(30 * time.Second); ; {
		if _, err := os.Stat(ready); err == nil {
			return
		}
		select {
		case err := <-exited:
			skipWithoutMountNamespace(t, out.Bytes())
			t.Fatalf("the probe exited before it was ready: %v\noutput:\n%s", err, out.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("the probe was not ready within 30s\noutput:\n%s", out.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// awaitProbeExit waits for the probe to finish, skipping on a refused mount
// namespace and failing on a non-zero exit or a hang.
func awaitProbeExit(t *testing.T, cmd *exec.Cmd, exited <-chan error, out *bytes.Buffer) {
	t.Helper()
	select {
	case err := <-exited:
		if err != nil {
			skipWithoutMountNamespace(t, out.Bytes())
			t.Fatalf("contained probe failed: %v\noutput:\n%s", err, out.String())
		}
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill()
		<-exited
		t.Fatalf("the probe did not finish within 60s\noutput:\n%s", out.String())
	}
}

// writeOrKill writes body to path, killing the probe and failing the test if it
// cannot, so a setup error does not leave the supervisor running.
func writeOrKill(t *testing.T, cmd *exec.Cmd, exited <-chan error, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		_ = cmd.Process.Kill()
		<-exited
		t.Fatal(err)
	}
}

// awaitFileExists blocks until path exists or timeout elapses. Used by the
// contained side, which has no testing.T.
func awaitFileExists(path string, timeout time.Duration) {
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}
