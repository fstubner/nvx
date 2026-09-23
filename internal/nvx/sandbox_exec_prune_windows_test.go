//go:build windows

package nvx

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// stageFixture stages a copy of a one-executable source directory the way a
// contained launch does, and returns the command path and the staged directory.
func stageFixture(t *testing.T, nvxHome string) (cmdPath, stagedDir string) {
	t.Helper()
	src := tempDir(t)
	cmdPath = filepath.Join(src, "tool.exe")
	ping, err := os.ReadFile(filepath.Join(os.Getenv("SystemRoot"), "System32", "PING.EXE"))
	if err != nil {
		t.Skipf("no PING.EXE to use as a fixture: %v", err)
	}
	if err := os.WriteFile(cmdPath, ping, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "lib.js"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	staged, err := stageAppContainerExecutable(nvxHome, cmdPath)
	if err != nil {
		t.Fatal(err)
	}
	return cmdPath, filepath.Dir(staged)
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// A command outside ~/.nvx/versions is staged as a copy of its whole
// directory, keyed on the command's modification time. Every update left the
// previous copy behind for good.
func TestStaleCommandCopiesArePruned(t *testing.T) {
	nvxHome := tempDir(t)
	cmdPath, current := stageFixture(t, nvxHome)

	if n := pruneStaleCommandCopies(nvxHome, 0); n != 0 || !exists(current) {
		t.Fatalf("the copy the next launch will use was pruned (removed %d)", n)
	}

	// The command is updated: the next launch stages a new copy.
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(cmdPath, later, later); err != nil {
		t.Fatal(err)
	}
	staged, err := stageAppContainerExecutable(nvxHome, cmdPath)
	if err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Dir(staged)
	if fresh == current {
		t.Fatal("test setup: an updated command reused its old copy")
	}

	// And a copy from before markers existed, which nothing has used since.
	legacy := filepath.Join(nvxHome, "sandbox-exec", "0123456789abcdef0123456789abcdef")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	supervisor := filepath.Join(nvxHome, "sandbox-exec", "supervisor")
	if err := os.MkdirAll(supervisor, 0o700); err != nil {
		t.Fatal(err)
	}

	if n := pruneStaleCommandCopies(nvxHome, 0); n != 2 {
		t.Errorf("removed %d copies, want 2 (the superseded one and the unmarked one)", n)
	}
	if exists(current) || exists(current+stagedSourceSuffix) {
		t.Error("the superseded copy or its marker survived")
	}
	if exists(legacy) {
		t.Error("the unmarked copy survived")
	}
	if !exists(fresh) || !exists(supervisor) {
		t.Error("pruning removed the current copy or the supervisor directory")
	}
}

// A superseded copy can still be running: a long-lived server started before
// the update. Deleting its files under it would break it mid-run.
func TestARunningStaleCopyIsLeftAlone(t *testing.T) {
	nvxHome := tempDir(t)
	cmdPath, stale := stageFixture(t, nvxHome)

	proc := exec.Command(filepath.Join(stale, "tool.exe"), "-n", "30", "127.0.0.1")
	if err := proc.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proc.Process.Kill(); _, _ = proc.Process.Wait() })

	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(cmdPath, later, later); err != nil {
		t.Fatal(err)
	}
	if n := pruneStaleCommandCopies(nvxHome, 0); n != 0 {
		t.Errorf("removed %d copies while one was running", n)
	}
	if !exists(filepath.Join(stale, "lib.js")) {
		t.Error("files of a running copy were deleted")
	}

	_ = proc.Process.Kill()
	_, _ = proc.Process.Wait()
	if n := pruneStaleCommandCopies(nvxHome, 0); n != 1 || exists(stale) {
		t.Errorf("once it stopped, the stale copy was not removed (removed %d)", n)
	}
}

func TestCommandCopyPruningHonoursItsBudget(t *testing.T) {
	nvxHome := tempDir(t)
	for _, k := range []string{"0123456789abcdef0123456789abcde0", "0123456789abcdef0123456789abcde1"} {
		if err := os.MkdirAll(filepath.Join(nvxHome, "sandbox-exec", k), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if n := pruneStaleCommandCopies(nvxHome, 1); n != 1 {
		t.Errorf("budget 1 removed %d", n)
	}
}
