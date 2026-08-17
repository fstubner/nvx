//go:build linux

package main

import "syscall"

// supervisorCloneFlags returns the namespace flags for the sandbox supervisor
// process (__landlock-exec), which the parent creates.
//
// CLONE_NEWPID goes HERE, on the supervisor, not on the target. The teardown
// guarantee this design relies on is a kernel property of PID 1: when PID 1 of a
// PID namespace dies, everything else in that namespace is SIGKILLed and the
// namespace is reclaimed. That only helps if nvx's own supervisor is PID 1. With
// the flag on the target instead, killing nvx left the target running as PID 1 of
// a still-live namespace -- the Linux analogue of the Windows orphan pile-up that
// the job object fixed.
//
// It also makes the supervisor the reaper of last resort for orphaned
// descendants; see reapUntilChildExits.
//
// CLONE_NEWNET is conditional because network.mode=open deliberately keeps host
// networking, whereas process-tree teardown is always wanted.
func supervisorCloneFlags(networkMode string) uintptr {
	flags := uintptr(syscall.CLONE_NEWPID)
	if networkModeRequiresNamespace(networkMode) {
		flags |= syscall.CLONE_NEWNET
	}
	return flags
}

// reapUntilChildExits waits for the process trackedPid, reaping every other child
// that exits in the meantime, and returns trackedPid's exit code.
//
// The supervisor is PID 1 of a PID namespace, so orphaned descendants reparent to
// it and nothing else will ever reap them: without this they accumulate as
// zombies for the life of the session, which for a long-running daemon is
// unbounded. The spec called a wait4 loop required and it was never written.
//
// This deliberately replaces cmd.Wait(). A concurrent wait4(-1) races os/exec for
// the target's status and would make Wait fail with ECHILD, so the supervisor has
// to own all waiting in one place -- which means picking the tracked child's
// status out of a stream of arbitrary exits rather than returning on the first.
func reapUntilChildExits(trackedPid int) int {
	for {
		var ws syscall.WaitStatus
		wpid, err := syscall.Wait4(-1, &ws, 0, nil)
		switch err {
		case nil:
			// keep going
		case syscall.EINTR:
			continue
		default:
			// ECHILD means no children remain; the tracked one is already gone and
			// its status was consumed by an earlier iteration or never existed.
			return 1
		}

		if wpid != trackedPid {
			continue // an orphan; reaping it is the whole point
		}

		// The target is done. Drain any already-exited stragglers so they are not
		// left as zombies for the brief remainder of this process's life, then stop.
		for {
			var extra syscall.WaitStatus
			p, err := syscall.Wait4(-1, &extra, syscall.WNOHANG, nil)
			if err != nil || p <= 0 {
				break
			}
		}
		return waitStatusExitCode(ws)
	}
}

// waitStatusExitCode maps a wait status to a process exit code, reporting a
// signal death as 128+signal the way a shell does rather than as success.
func waitStatusExitCode(ws syscall.WaitStatus) int {
	if ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ws.ExitStatus()
}
