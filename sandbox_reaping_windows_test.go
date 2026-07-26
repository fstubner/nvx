//go:build windows

package main

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// A process assigned to a reaping job must be killed automatically once the
// job's last handle closes. That is exactly what happens when nvx itself is
// killed (e.g. by an MCP client giving up on a slow AppContainer setup)
// before it ever gets to WaitForSingleObject on a child it already launched.
// Without this, the abandoned child runs forever -- which is the incident
// this fixes: dozens of orphaned node processes accumulating over time.
func TestReapingJobKillsProcessOnClose(t *testing.T) {
	cmd := exec.Command("powershell", "-NoProfile", "-Command", "Start-Sleep -Seconds 120")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start test child: %v", err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		_ = cmd.Process.Kill() // safety net if the job somehow failed to reap it
	})

	h, err := openProcessForJob(uint32(pid))
	if err != nil {
		t.Fatalf("openProcessForJob: %v", err)
	}
	defer syscall.CloseHandle(h)

	job, err := createReapingJob()
	if err != nil {
		t.Fatalf("createReapingJob: %v", err)
	}
	if err := assignToReapingJob(job, h); err != nil {
		t.Fatalf("assignToReapingJob: %v", err)
	}

	if !processIsRunning(pid) {
		t.Fatal("child should still be running before the job closes")
	}

	// Simulate the launcher (nvx) disappearing: its last handle to the job closes.
	if err := syscall.CloseHandle(job); err != nil {
		t.Fatalf("CloseHandle(job): %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !processIsRunning(pid) {
			return // reaped
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("expected child pid %d to be killed once the job closed, but it is still running", pid)
}

// A job with no processes assigned must be safe to close: creating the job
// always succeeds up front, before we know whether CreateProcess itself will.
func TestReapingJobCloseWithNoProcessAssigned(t *testing.T) {
	job, err := createReapingJob()
	if err != nil {
		t.Fatalf("createReapingJob: %v", err)
	}
	if err := syscall.CloseHandle(job); err != nil {
		t.Fatalf("CloseHandle on an empty job should succeed, got: %v", err)
	}
}
