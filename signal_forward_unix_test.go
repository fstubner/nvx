//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A sandboxed child does not outlive an nvx that was asked to terminate.
//
// macOS has no kill-on-parent-death primitive, and nvx installed no signal
// handler anywhere, so `kill <nvx>` left the contained process running: a
// server kept its port, a long job kept running with nothing owning it. The
// design document for the sandbox called for exactly this handler on macOS and
// it was never written. The helper is not macOS-specific, so it is tested
// wherever the suite runs on a unix.
func TestTerminatingNvxTerminatesTheSandboxedChild(t *testing.T) {
	// The child announces itself by creating a file rather than through any
	// state this test shares with the helper: cmd.Process is written by Start,
	// so watching it from here would be a race, and was one.
	ready := filepath.Join(t.TempDir(), "started")
	cmd := exec.Command("/bin/sh", "-c", "touch "+ready+"; sleep 60")
	errs := make(chan error, 1)
	go func() { errs <- runChildForwardingSignals(cmd) }()

	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the child never started")
		}
		time.Sleep(5 * time.Millisecond)
	}

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("could not send SIGTERM to this process: %v", err)
	}

	select {
	case <-errs:
		// The child exited because the signal reached it. Which error it
		// carries depends on whether the child took SIGTERM or the escalation.
	case <-time.After(childTerminationGrace + 10*time.Second):
		t.Fatal("nvx was terminated and the sandboxed child was still running")
	}
}

// The child's exit status still comes back unchanged -- the forwarding wrapper
// stands in for cmd.Run, and a wrong exit code from a sandboxed command is a
// silent failure in any script that checks one.
func TestForwardingPreservesTheChildExitCode(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exit 7")
	err := runChildForwardingSignals(cmd)
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected an ExitError, got %v", err)
	}
	if exitErr.ExitCode() != 7 {
		t.Fatalf("exit code %d, want 7", exitErr.ExitCode())
	}
	if err := runChildForwardingSignals(exec.Command("/bin/sh", "-c", "exit 0")); err != nil {
		t.Fatalf("a successful child reported an error: %v", err)
	}
}
