//go:build linux

package nvx

import (
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// The contained process must not outlive the nvx that started it.
//
// Measured on WSL Ubuntu 24.04 before the fix: `kill <nvx>` and `kill -9 <nvx>`
// both left the contained node running, re-parented to init, and the next nvx
// run deleted its guest home from under it. The parent used cmd.Run with no
// parent-death signal and no signal forwarding, and the session marker named only
// the dead parent.
//
// Three roles share this one test function, because a test binary is the only
// program available to stand in for nvx and the supervisor:
//
//	(unset)     the test itself
//	parent      stands in for nvx: records a session, runs the supervisor
//	supervisor  stands in for __landlock-exec: PID 1 of a namespace, with a
//	            heartbeating descendant
//	direct      stands in for nvx on the uncontained path: runs a heartbeating
//	            child through runDirectChild
const lifetimeRoleEnv = "NVX_TEST_LIFETIME_ROLE"

func TestNvxTerminationTakesTheChildTreeWithIt(t *testing.T) {
	switch os.Getenv(lifetimeRoleEnv) {
	case "parent":
		lifetimeParent()
	case "supervisor":
		lifetimeSupervisor()
	case "direct":
		lifetimeDirectParent()
	}

	for _, tc := range []struct {
		name string
		role string
		sig  syscall.Signal
	}{
		{"contained, nvx SIGKILLed", "parent", syscall.SIGKILL},
		{"contained, nvx SIGTERMed", "parent", syscall.SIGTERM},
		{"uncontained, nvx SIGKILLed", "direct", syscall.SIGKILL},
		{"uncontained, nvx SIGTERMed", "direct", syscall.SIGTERM},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.role == "parent" {
				requireNamespaceSupport(t, supervisorSysProcAttr("proxy"))
			}
			dir := tempDir(t)
			hb := filepath.Join(dir, "heartbeat")
			guest := filepath.Join(dir, "guest")
			if err := os.MkdirAll(guest, 0o700); err != nil {
				t.Fatal(err)
			}

			parent := exec.Command(os.Args[0], "-test.run=^TestNvxTerminationTakesTheChildTreeWithIt$")
			parent.Env = append(os.Environ(),
				lifetimeRoleEnv+"="+tc.role,
				"NVX_TEST_HEARTBEAT="+hb,
				"NVX_TEST_GUEST="+guest,
			)
			if err := parent.Start(); err != nil {
				t.Fatalf("start the stand-in for nvx: %v", err)
			}
			defer func() { _ = parent.Process.Kill(); _, _ = parent.Process.Wait() }()

			waitForFile(t, hb, "the contained descendant never started heartbeating")
			if tc.role == "parent" {
				// The session record must name the supervisor as well as nvx.
				deadline := time.Now().Add(10 * time.Second)
				for {
					if o, ok := readSessionOwner(guest); ok && o.SupervisorPID > 0 {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("the session marker never recorded the supervisor's pid")
					}
					time.Sleep(20 * time.Millisecond)
				}
			}

			if err := parent.Process.Signal(tc.sig); err != nil {
				t.Fatalf("signal the stand-in for nvx: %v", err)
			}
			_ = parent.Wait()

			if !heartbeatStops(hb, 10*time.Second) {
				t.Errorf("the contained process was still running after nvx received %v", tc.sig)
			}
		})
	}
}

func waitForFile(t *testing.T, path, failure string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal(failure)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// heartbeatStops reports whether the heartbeat file stops changing within limit.
// A live descendant rewrites it every 50ms, so 300ms without a change means gone.
func heartbeatStops(path string, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		first, err := os.Stat(path)
		if err != nil {
			return false
		}
		time.Sleep(300 * time.Millisecond)
		second, err := os.Stat(path)
		if err == nil && first.ModTime().Equal(second.ModTime()) {
			return true
		}
	}
	return false
}

func heartbeatCommand() *exec.Cmd {
	return exec.Command("/bin/sh", "-c",
		"while true; do date +%s%N > "+os.Getenv("NVX_TEST_HEARTBEAT")+"; sleep 0.05; done")
}

// lifetimeParent is nvx as platformLaunchNative runs it: a recorded session and a
// supervisor started through runSupervisor. Never returns.
func lifetimeParent() {
	guest := os.Getenv("NVX_TEST_GUEST")
	writeSessionOwner(guest, time.Now())
	cmd := exec.Command(os.Args[0], "-test.run=^TestNvxTerminationTakesTheChildTreeWithIt$")
	cmd.Env = append(os.Environ(), lifetimeRoleEnv+"=supervisor")
	cmd.SysProcAttr = supervisorSysProcAttr("proxy")
	if err := runSupervisor(cmd, guest); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

// lifetimeSupervisor is PID 1 of its namespace. Like the real supervisor it
// takes SIGTERM and ends its tree on its own terms.
func lifetimeSupervisor() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM)
	inner := heartbeatCommand()
	if err := inner.Start(); err != nil {
		os.Exit(1)
	}
	select {
	case <-sigs:
		_ = inner.Process.Kill()
		os.Exit(0)
	case <-time.After(60 * time.Second):
		os.Exit(0)
	}
}

// lifetimeDirectParent is nvx on the uncontained path. Never returns.
func lifetimeDirectParent() {
	if err := runDirectChild(heartbeatCommand()); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
