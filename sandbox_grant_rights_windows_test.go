//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"unsafe"
)

// The two rights must not share a cache slot, or removing one ACE by hand leaves
// the cache still claiming the other is in place -- which is how a hand-repaired
// directory ended up with no grant at all and could not even be entered.
func TestTheGrantCacheKeepsTheTwoRightsApart(t *testing.T) {
	const sid = "S-1-15-3-1024-2222"
	if grantIdentityFor(sid, grantModify) == grantIdentityFor(sid, grantReadExec) {
		t.Fatal("modify and read/execute share a cache key; one would mask the other")
	}
}

// The --connect peer check is decided against this run's job object. If the
// launch never publishes it, every legitimate connection is refused and the
// feature is dead -- silently, because it fails closed. Deleting that one line
// used to leave the entire suite green while the built binary refused everything.
//
// It supervises a CHILD, never this process. A first version passed a handle to
// the test process itself; the job kills its members when the last handle closes,
// so cleanup killed the test binary mid-run, and `go test` read the zero exit as
// success. It reported "ok" with the assertions never reached, and passed just as
// happily with the line under test deleted.
func TestSupervisingTheProcessTreePublishesTheJobForThePeerCheck(t *testing.T) {
	if sessionJob.Load() != 0 {
		t.Fatal("precondition: a session job is already published")
	}
	child := exec.Command("cmd.exe", "/c", "ping -n 10 127.0.0.1 > nul")
	if err := child.Start(); err != nil {
		t.Skipf("could not start a child process to supervise: %v", err)
	}
	defer func() { _ = child.Process.Kill() }()

	h, err := openProcessForJob(uint32(child.Process.Pid))
	if err != nil {
		t.Skipf("cannot open the child for job assignment: %v", err)
	}
	defer syscall.CloseHandle(h)

	cleanup := superviseProcessTree(h)
	published := sessionJob.Load()
	if published == 0 {
		cleanup()
		t.Fatal("the job was not published; --connect would refuse every connection it was given")
	}

	// The peer check must be able to use it while the run is live. A separate
	// handle with query rights, as peerBelongsToThisSandbox opens: the assignment
	// handle carries SET_QUOTA|TERMINATE and IsProcessInJob refuses it.
	q, _, qerr := procOpenProcessForQuery.Call(uintptr(processQueryLimitedInfo), 0, uintptr(child.Process.Pid))
	if q == 0 {
		cleanup()
		t.Fatalf("could not open the child for querying: %v", qerr)
	}
	defer syscall.CloseHandle(syscall.Handle(q))

	var inJob int32
	ret, _, jerr := procIsProcessInJob.Call(q, published, uintptr(unsafe.Pointer(&inJob)))
	if ret == 0 {
		cleanup()
		t.Fatalf("the published job could not be queried: %v", jerr)
	}
	if inJob == 0 {
		cleanup()
		t.Fatal("the supervised process is not in the published job; every peer would be judged against the wrong job")
	}

	cleanup()
	if sessionJob.Load() != 0 {
		t.Fatal("the job was still published after cleanup; a later run would check against a closed handle")
	}
}

// The rights must be read from the entry, never from the whole icacls line: the
// line starts with the directory's own path, and a path containing "(M)" was read
// as modify access from an entry granting only (OI)(CI)(RX). It fails toward
// "already granted", so the modify grant is skipped and the wrong answer cached --
// and the sandbox then cannot write a directory it was supposed to own.
//
// This goes through appContainerHasGrantFor against real icacls output, not
// through satisfies() directly. A first version called rightsAfterSID itself and
// then asserted on satisfies, which tested neither the call site nor the wiring:
// restoring the whole-line judgement left it passing.
func TestRightsAreReadFromTheEntryNotThePath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "d(M)x")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create a directory whose path contains (M): %v", err)
	}
	sid, err := scopeCapabilitySID(dir)
	if err != nil {
		t.Skipf("cannot derive a capability SID here: %v", err)
	}
	t.Cleanup(func() { _ = revokeSandboxReadExec(sid, dir) })

	wrote, err := grantSandboxReadExec(sid, dir)
	if err != nil {
		t.Skipf("cannot write an ACL in the test environment: %v", err)
	}
	if !wrote {
		t.Fatal("no read/execute entry was written, so there is nothing to interpret")
	}

	if appContainerHasGrantFor(sid, dir, grantModify) {
		t.Error("a (M) in the directory's own path was read as modify access; the modify grant would be skipped and writes would fail")
	}
	if !appContainerHasGrantFor(sid, dir, grantReadExec) {
		t.Error("the entry's real (RX) was not recognised, so it would be re-granted on every launch")
	}
}

// grantSandboxReadExec must report whether it actually wrote an entry, so the
// caller records only what nvx can meaningfully take back.
//
// Withdrawing is not selective -- icacls removes an identity's whole granted
// entry on a path, not one right from it. Recording a grant that was SKIPPED,
// because a modify entry already covered read and execute, meant a later
// withdrawal deleted the write access that entry existed for: the project's own
// directory became unusable with "chdir: Access is denied", measured against the
// built binary before this fix.
//
// A real directory and a real capability SID, because the thing under test is how
// icacls output is interpreted.
func TestAReadExecGrantAlreadyCoveredByModifyIsNotWrittenAgain(t *testing.T) {
	dir := t.TempDir()
	sid, err := scopeCapabilitySID(dir)
	if err != nil {
		t.Skipf("cannot derive a capability SID here: %v", err)
	}
	t.Cleanup(func() { _ = revokeSandboxReadExec(sid, dir) })

	if err := grantSandboxModify(sid, dir); err != nil {
		t.Skipf("cannot write an ACL in the test environment: %v", err)
	}

	wrote, err := grantSandboxReadExec(sid, dir)
	if err != nil {
		t.Fatalf("grantSandboxReadExec: %v", err)
	}
	if wrote {
		t.Fatal("an entry was written over one that already granted modify; recording it would let a later withdrawal remove the write access")
	}

	// On a directory with no entry at all, it must write one and say so -- or the
	// feature would grant nothing and record nothing.
	fresh := t.TempDir()
	freshSID, err := scopeCapabilitySID(fresh)
	if err != nil {
		t.Skipf("cannot derive a capability SID here: %v", err)
	}
	t.Cleanup(func() { _ = revokeSandboxReadExec(freshSID, fresh) })
	wrote, err = grantSandboxReadExec(freshSID, fresh)
	if err != nil {
		t.Fatalf("grantSandboxReadExec on a fresh directory: %v", err)
	}
	if !wrote {
		t.Fatal("no entry was written on a directory that had none, and none was recorded")
	}
}
