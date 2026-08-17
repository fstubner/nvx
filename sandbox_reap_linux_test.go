//go:build linux

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestReapUntilChildExitsReturnsTrackedStatusWhileReapingOthers pins the reaping
// half of F58. Once the sandbox supervisor is PID 1 of a PID namespace, every
// orphaned descendant reparents to it, and nothing else will ever reap them --
// the spec called a wait4 loop "required" for exactly this and it was never
// implemented.
//
// The supervisor therefore cannot use cmd.Wait(): a concurrent wait4(-1) races
// with os/exec for the target's exit status. It has to own all waiting, which
// means correctly picking the tracked child's status out of a stream of
// arbitrary exits. A naive implementation returning on the first wait4 would
// report the wrong exit code, which is what this asserts against.
func TestReapUntilChildExitsReturnsTrackedStatusWhileReapingOthers(t *testing.T) {
	// An unrelated child that exits first, standing in for an orphan.
	orphan := exec.Command("/bin/sh", "-c", "exit 0")
	if err := orphan.Start(); err != nil {
		t.Fatalf("start orphan: %v", err)
	}
	orphanPid := orphan.Process.Pid

	// The tracked child exits later, with a distinctive status.
	tracked := exec.Command("/bin/sh", "-c", "sleep 0.3; exit 7")
	if err := tracked.Start(); err != nil {
		t.Fatalf("start tracked: %v", err)
	}

	code := reapUntilChildExits(tracked.Process.Pid)
	if code != 7 {
		t.Fatalf("reapUntilChildExits = %d, want 7 (the tracked child's status, not whichever child exited first)", code)
	}

	// The orphan must have been reaped, not left as a zombie. Wait4 on it now
	// should report ECHILD because its status was already consumed.
	var ws syscall.WaitStatus
	if _, err := syscall.Wait4(orphanPid, &ws, syscall.WNOHANG, nil); err != syscall.ECHILD {
		t.Errorf("orphan pid %d not reaped: Wait4 err = %v, want ECHILD", orphanPid, err)
	}
}

// TestReapUntilChildExitsPropagatesSignalDeaths keeps exit-code reporting honest
// for a killed target, which is the common case when a client gives up.
func TestReapUntilChildExitsPropagatesSignalDeaths(t *testing.T) {
	victim := exec.Command("/bin/sh", "-c", "sleep 30")
	if err := victim.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := victim.Process.Pid
	go func() {
		_ = victim.Process.Signal(syscall.SIGKILL)
	}()

	code := reapUntilChildExits(pid)
	if code == 0 {
		t.Errorf("a SIGKILLed child must not report success, got %d", code)
	}
}

// TestSupervisorCloneFlagsCarryPidAndNetNamespaces pins the placement half of
// F58. The PID namespace must be created for the SUPERVISOR, not for the target:
// the design this implements relies on "when PID 1 of the namespace dies the
// kernel tears down everything in it", and that only holds if nvx's own
// supervisor is PID 1. Previously CLONE_NEWPID was set on the target instead, so
// killing nvx left the target running as PID 1 of a live namespace -- the Linux
// analogue of the Windows orphan pile-up (F1).
func TestSupervisorCloneFlagsCarryPidAndNetNamespaces(t *testing.T) {
	flags := supervisorCloneFlags("proxy")
	for _, tc := range []struct {
		name string
		flag uintptr
	}{
		{"CLONE_NEWNET", syscall.CLONE_NEWNET},
		{"CLONE_NEWPID", syscall.CLONE_NEWPID},
	} {
		if flags&tc.flag == 0 {
			t.Errorf("supervisor clone flags missing %s (got %#x)", tc.name, flags)
		}
	}

	// network.mode=open needs no network namespace, but the PID namespace is about
	// process-tree teardown and still applies.
	open := supervisorCloneFlags("open")
	if open&syscall.CLONE_NEWNET != 0 {
		t.Errorf("open mode must not create a network namespace (got %#x)", open)
	}
	if open&syscall.CLONE_NEWPID == 0 {
		t.Errorf("open mode should still get a PID namespace for teardown (got %#x)", open)
	}
}

// TestTargetNamespacesNoLongerCreatePidNamespace is the other side of the same
// invariant: if the target also created a PID namespace it would become PID 1 of
// its own, and the supervisor's teardown guarantee would not reach it.
func TestTargetNamespacesNoLongerCreatePidNamespace(t *testing.T) {
	cmd := exec.Command("/bin/true")
	applyLinuxNamespaces(cmd, t.TempDir())
	if cmd.SysProcAttr == nil {
		t.Fatal("applyLinuxNamespaces set no SysProcAttr")
	}
	if cmd.SysProcAttr.Cloneflags&syscall.CLONE_NEWPID != 0 {
		t.Errorf("target must not create its own PID namespace (got %#x)", cmd.SysProcAttr.Cloneflags)
	}
}

// TestSupervisorDeathTearsDownDescendants is the behavioural claim behind moving
// CLONE_NEWPID to the supervisor: killing nvx must take the whole contained tree
// with it. Previously the target was PID 1 of its own namespace, so killing nvx
// left it running -- exactly the orphan accumulation that forced nvx off the
// maintainer's machine on Windows.
//
// A namespace-local pid is meaningless outside, so the descendant proves it is
// alive by touching a heartbeat file; after PID 1 is killed the heartbeat must
// stop advancing.
func TestSupervisorDeathTearsDownDescendants(t *testing.T) {
	if os.Getenv("NVX_TEST_REAP_CHILD") == "1" {
		// PID 1 of a fresh PID namespace: spawn a heartbeating descendant, then idle.
		hb := os.Getenv("NVX_TEST_HEARTBEAT")
		inner := exec.Command("/bin/sh", "-c",
			"while true; do date +%s%N > "+hb+"; sleep 0.05; done")
		if err := inner.Start(); err != nil {
			return
		}
		time.Sleep(30 * time.Second)
		return
	}
	if os.Geteuid() != 0 {
		t.Skip("creating a PID namespace needs root")
	}

	hb := filepath.Join(t.TempDir(), "heartbeat")
	cmd := exec.Command(os.Args[0], "-test.run=TestSupervisorDeathTearsDownDescendants")
	cmd.Env = append(os.Environ(), "NVX_TEST_REAP_CHILD=1", "NVX_TEST_HEARTBEAT="+hb)
	cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: supervisorCloneFlags("proxy")}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start supervisor: %v", err)
	}

	// Wait for the heartbeat to prove the descendant is alive.
	var alive bool
	for i := 0; i < 60; i++ {
		if _, err := os.Stat(hb); err == nil {
			alive = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !alive {
		_ = cmd.Process.Kill()
		t.Skip("descendant never started heartbeating")
	}

	// Kill PID 1 of the namespace and confirm the descendant stops.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill supervisor: %v", err)
	}
	_, _ = cmd.Process.Wait()

	time.Sleep(400 * time.Millisecond)
	first, err := os.Stat(hb)
	if err != nil {
		t.Fatalf("stat heartbeat: %v", err)
	}
	time.Sleep(600 * time.Millisecond)
	second, err := os.Stat(hb)
	if err != nil {
		t.Fatalf("stat heartbeat: %v", err)
	}
	if !first.ModTime().Equal(second.ModTime()) {
		t.Errorf("descendant still alive after the supervisor was killed: heartbeat advanced %v -> %v",
			first.ModTime(), second.ModTime())
	}
}
