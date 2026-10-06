//go:build linux

package nvx

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
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
	if runDotenvProbeRole("TestContainedProcessCannotReadDotenvFiles", runDotenvProbeTarget) {
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
	out, err := dotenvProbeCommand(t, name, work).CombinedOutput()
	skipWithoutMountNamespace(t, out)
	if err != nil {
		t.Fatalf("contained probe failed: %v\noutput:\n%s", err, out)
	}
	t.Logf("contained probe output:\n%s", out)
	return parseProbeResults(string(out))
}

// dotenvProbeCommand is the supervisor half of a probe, ready to start.
func dotenvProbeCommand(t *testing.T, name, work string, env ...string) *exec.Cmd {
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
	cmd.Env = append(cmd.Env, env...)
	cmd.SysProcAttr = attr
	return cmd
}

func skipWithoutMountNamespace(t *testing.T, out []byte) {
	t.Helper()
	if strings.Contains(string(out), "unshare mount namespace") && strings.Contains(string(out), "operation not permitted") {
		t.Skipf("this host refuses a mount namespace inside an unprivileged user namespace "+
			"(Ubuntu 24.04 AppArmor); the privileged CI step covers it:\n%s", out)
	}
}

func runDotenvProbeRole(name string, target func(work string)) bool {
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
		target(os.Getenv("NVX_TEST_DOTENV_WORK"))
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
	// Creating one is allowed. The watcher covers it once it exists, which
	// TestContainedProcessCannotReadDotenvFilesCreatedDuringRun checks.
	err = os.MkdirAll(at("fresh"), 0o755)
	if err == nil {
		err = os.WriteFile(at("fresh/.env"), []byte("X=1\n"), 0o644)
	}
	report("create_new", err)
}

// A dotenv file that appears while the target runs is covered too: one created
// from outside, a covered .env that an editor replaces by renaming a new file
// over it, and one inside a directory moved into the project. The target is
// already running for each step. It reads the file at both paths the sandbox
// shows it at: under the link it was started in, and under the directory the
// link names, which sandboxBindPlan binds separately.
//
// It runs twice. The second project has no dotenv file at launch, so step 2
// creates .env by the rename. The masks only hold if the capabilities that
// read past their mode are gone, and a launch with nothing to cover once kept
// them.
func TestContainedProcessCannotReadDotenvFilesCreatedDuringRun(t *testing.T) {
	const name = "TestContainedProcessCannotReadDotenvFilesCreatedDuringRun"
	if runDotenvProbeRole(name, runDotenvWatchProbeTarget) {
		return
	}
	for _, atLaunch := range []bool{true, false} {
		t.Logf("dotenv file at launch: %t", atLaunch)
		runDotenvWatchProbe(t, name, atLaunch)
	}
}

func runDotenvWatchProbe(t *testing.T, name string, atLaunch bool) {
	base := tempDir(t)
	proj := filepath.Join(base, "proj")
	incoming := filepath.Join(base, "incoming")
	for _, d := range []string{proj, incoming} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if atLaunch {
		if err := os.WriteFile(filepath.Join(proj, ".env"), []byte(dotenvProbeSecret), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(incoming, ".env"), []byte(dotenvStepSecret(3)), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(proj, alias); err != nil {
		t.Fatal(err)
	}

	cmd := dotenvProbeCommand(t, name, alias, "NVX_TEST_DOTENV_REAL="+proj)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	// Called only once the probe has exited, so out is complete.
	failed := func(format string, args ...any) {
		t.Helper()
		skipWithoutMountNamespace(t, out.Bytes())
		t.Fatalf(format+"\noutput:\n%s", append(args, out.String())...)
	}
	kill := func() {
		_ = cmd.Process.Kill()
		<-exited
	}

	steps := []func() error{
		func() error {
			return os.WriteFile(filepath.Join(proj, ".env.local"), []byte(dotenvStepSecret(1)), 0o600)
		},
		func() error {
			tmp := filepath.Join(proj, "env.tmp")
			if err := os.WriteFile(tmp, []byte(dotenvStepSecret(2)), 0o600); err != nil {
				return err
			}
			return os.Rename(tmp, filepath.Join(proj, ".env"))
		},
		func() error { return os.Rename(incoming, filepath.Join(proj, "incoming")) },
	}
	began := make([]time.Time, len(steps)+1)
	for i, step := range steps {
		n := i + 1
		ready := filepath.Join(proj, fmt.Sprintf("ready-%d", n))
		for deadline := time.Now().Add(30 * time.Second); ; {
			if _, err := os.Stat(ready); err == nil {
				break
			}
			select {
			case err := <-exited:
				failed("the probe exited before step %d: %v", n, err)
			default:
			}
			if time.Now().After(deadline) {
				kill()
				failed("the probe did not reach step %d within 30s", n)
			}
			time.Sleep(5 * time.Millisecond)
		}
		began[n] = time.Now()
		if err := step(); err != nil {
			kill()
			t.Fatalf("step %d: %v", n, err)
		}
		if err := os.WriteFile(filepath.Join(proj, fmt.Sprintf("done-%d", n)), nil, 0o644); err != nil {
			kill()
			t.Fatal(err)
		}
	}
	select {
	case err := <-exited:
		if err != nil {
			failed("contained probe failed: %v", err)
		}
	case <-time.After(60 * time.Second):
		kill()
		failed("the probe did not finish within 60s")
	}
	t.Logf("contained probe output:\n%s", out.String())

	got := parseProbeResults(out.String())
	for n := 1; n <= len(steps); n++ {
		key := fmt.Sprintf("step%d", n)
		if got[key] != "denied" {
			t.Errorf("%s = %q, want %q", key, got[key], "denied")
			continue
		}
		// The time from the change outside to the first refused read. When the
		// target never read the new file, the mask landed before it looked, and
		// this is an upper bound.
		if at, err := strconv.ParseInt(got[key+".denied_at"], 10, 64); err == nil {
			t.Logf("%s: covered %v after the change (target read it first: %s)",
				key, time.Duration(at-began[n].UnixNano()), got[key+".seen"])
		}
	}
}

func dotenvStepSecret(n int) string {
	return fmt.Sprintf("API_KEY=STEP%d-SECRET\n", n)
}

// runDotenvWatchProbeTarget reads each step's file until every view of it is
// refused, for up to 5s. It writes ready-N before a step and the test writes
// done-N after it, so a refusal before the change (step 2's .env is covered
// from launch) does not count.
func runDotenvWatchProbeTarget(work string) {
	linked := os.Getenv("NVX_TEST_DOTENV_REAL")
	for i, rel := range []string{".env.local", ".env", "incoming/.env"} {
		n := i + 1
		views := []string{filepath.Join(work, rel), filepath.Join(linked, rel)}
		marker := fmt.Sprintf("STEP%d-SECRET", n)
		done := filepath.Join(work, fmt.Sprintf("done-%d", n))
		if err := os.WriteFile(filepath.Join(work, fmt.Sprintf("ready-%d", n)), nil, 0o644); err != nil {
			fmt.Printf("step%d=error\nstep%d.err=%v\n", n, n, err)
			return
		}
		result, seen := "timeout", false
		var deniedAt int64
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
			denied := 0
			for _, v := range views {
				b, err := os.ReadFile(v)
				if err == nil && strings.Contains(string(b), marker) {
					seen = true
				}
				if errors.Is(err, fs.ErrPermission) {
					denied++
				}
			}
			if denied < len(views) {
				continue
			}
			if _, err := os.Stat(done); seen || err == nil {
				result, deniedAt = "denied", time.Now().UnixNano()
				break
			}
		}
		if result == "timeout" && seen {
			result = "secret"
		}
		fmt.Printf("step%d=%s\nstep%d.seen=%t\nstep%d.denied_at=%d\n", n, result, n, seen, n, deniedAt)
	}
}
