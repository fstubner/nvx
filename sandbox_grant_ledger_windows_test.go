//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// An entry nvx wrote on an earlier run must still be recognised as its own.
//
// A record can be lost -- a corrupt ledger, a deleted grants directory, one
// failed save. If a later run does not re-record, the permission nvx granted can
// never be withdrawn by anything: measured, three runs in a row re-confirmed the
// entry, recorded nothing, and emptying the policy withdrew nothing.
func TestAnEntryFromAnEarlierRunIsStillRecognisedAsOurs(t *testing.T) {
	dir := t.TempDir()
	sid, err := scopeCapabilitySID(dir)
	if err != nil {
		t.Skipf("cannot derive a capability SID here: %v", err)
	}
	t.Cleanup(func() { _ = revokeSandboxReadExec(sid, dir) })

	first, err := grantSandboxReadExec(sid, dir)
	if err != nil {
		t.Skipf("cannot write an ACL in the test environment: %v", err)
	}
	if !first {
		t.Fatal("the first grant reported it wrote nothing")
	}

	// Exactly the state a lost record leaves behind: the entry is on disk, and
	// nothing records it. A second run must claim it.
	invalidateGrantCache()
	again, err := grantSandboxReadExec(sid, dir)
	if err != nil {
		t.Fatalf("second grantSandboxReadExec: %v", err)
	}
	if !again {
		t.Fatal("nvx did not recognise its own entry from an earlier run; a lost record would strand this permission for ever")
	}
}

// ...but a broader entry that merely happens to cover read and execute is not
// nvx's to take back: withdrawing removes the identity's whole entry, so
// recording it would delete the write access it exists for.
func TestABroaderEntryIsNotClaimedAsOurs(t *testing.T) {
	dir := t.TempDir()
	sid, err := scopeCapabilitySID(dir)
	if err != nil {
		t.Skipf("cannot derive a capability SID here: %v", err)
	}
	t.Cleanup(func() { _ = revokeSandboxReadExec(sid, dir) })

	if err := grantSandboxModify(sid, dir); err != nil {
		t.Skipf("cannot write an ACL in the test environment: %v", err)
	}
	invalidateGrantCache()

	ours, err := grantSandboxReadExec(sid, dir)
	if err != nil {
		t.Fatalf("grantSandboxReadExec: %v", err)
	}
	if ours {
		t.Fatal("a modify entry was claimed as this feature's own; withdrawing it would remove the write access it exists for")
	}
}

// Withdrawing an inheritable entry removes access for everything under it, so the
// cache must forget the subtree, not just the exact path.
//
// Checked against the MODIFY cache. An earlier version checked read/execute,
// which is no longer cached at all -- that question is asked afresh every time --
// so the test could not detect a cache failure, which is precisely what its name
// claimed. It passed with both forget calls deleted.
func TestWithdrawingAGrantForgetsTheWholeSubtree(t *testing.T) {
	parent := t.TempDir()
	child := filepath.Join(parent, "inner")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sid, err := scopeCapabilitySID(parent)
	if err != nil {
		t.Skipf("cannot derive a capability SID here: %v", err)
	}
	t.Cleanup(func() { _ = revokeACL(parent, sid) })

	if err := grantSandboxModify(sid, parent); err != nil {
		t.Skipf("cannot write an ACL in the test environment: %v", err)
	}
	// The child is covered by inheritance, and asking caches that answer.
	if !appContainerHasGrantFor(sid, child, grantModify) {
		t.Skip("the child did not inherit the entry in this environment")
	}
	if !grantCacheHas(grantIdentityFor(sid, grantModify), child) {
		t.Skip("the answer was not cached in this environment")
	}

	if err := revokeSandboxReadExec(sid, parent); err != nil && !errors.Is(err, errPermissionNotOurs) {
		t.Fatalf("revoke: %v", err)
	}
	// Whether or not the entry was nvx's to remove, a withdrawal must not leave the
	// subtree cached as granted: the next launch would skip a grant it needs.
	_ = revokeACL(parent, sid)
	grantCacheForgetUnder(grantIdentityFor(sid, grantModify), parent)

	if grantCacheHas(grantIdentityFor(sid, grantModify), child) {
		t.Fatal("the child is still cached as granted after the parent's entry was withdrawn; its grant would be skipped and the sandbox would get EPERM")
	}
}

// A record that cannot be read must be preserved, never silently dropped: it
// names permissions still on disk that nothing else can account for.
func TestAnUnreadableRecordIsKeptNotDiscarded(t *testing.T) {
	home := t.TempDir()
	scope := t.TempDir()
	if err := os.MkdirAll(grantsDir(home), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := grantsPath(home, scope)
	if err := os.WriteFile(path, []byte("{ this is not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	_ = loadProjectGrants(home, scope)

	if _, err := os.Stat(path); err == nil {
		t.Fatal("the unreadable record was left where a later save would overwrite it")
	}
	entries, _ := os.ReadDir(grantsDir(home))
	kept := false
	for _, e := range entries {
		if strings.Contains(e.Name(), ".unreadable") {
			kept = true
		}
	}
	if !kept {
		t.Fatal("the unreadable record was discarded; the permissions it named are now untraceable")
	}
}

// A second unreadable record must not destroy the first.
//
// Driven through loadProjectGrants, not by calling quarantinePath directly: a
// first version asserted on the helper and passed with the call site reverted to
// a fixed name -- the fourth test this session to check something other than the
// code it named.
func TestASecondUnreadableRecordDoesNotOverwriteTheFirst(t *testing.T) {
	home := t.TempDir()
	scope := t.TempDir()
	if err := os.MkdirAll(grantsDir(home), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := grantsPath(home, scope)

	for i, content := range []string{"{ first corrupt", "{ second corrupt"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		_ = loadProjectGrants(home, scope)
	}

	entries, err := os.ReadDir(grantsDir(home))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	kept := 0
	for _, e := range entries {
		if strings.Contains(e.Name(), ".unreadable") {
			kept++
		}
	}
	if kept < 2 {
		t.Fatalf("kept %d unreadable record(s), want 2: the second overwrote the first, destroying the only trace of what it named", kept)
	}
}

// The record is written atomically: a reader must never observe a partial file.
//
// This is what atomicity buys, and the only thing that distinguishes it from a
// plain write. An earlier version asserted that no temporary file was left behind
// and that the result parsed -- both true of a non-atomic write, so it passed
// with the rename removed.
//
// A plain write truncates the destination and fills it, so a concurrent reader
// can catch it empty or half-written. Writing to a temporary file and renaming
// means every reader sees one complete version or the other.
func TestTheRecordIsWrittenAtomically(t *testing.T) {
	home := t.TempDir()
	scope := t.TempDir()
	path := grantsPath(home, scope)

	// Enough entries that a non-atomic write has a window worth catching.
	grants := make([]readExecGrant, 0, 200)
	for i := 0; i < 200; i++ {
		grants = append(grants, readExecGrant{
			Path: filepath.Join(`C:\someeasonably\long\directory
ame	o\widen	he\window`, string(rune('a'+i%26))),
			SID: "S-1-15-3-1024-1111111111-2222222222-3333333333-4444444444-5555555555-6666666666-7777777777-8888888888",
		})
	}
	g := projectGrants{ProjectPath: scope, ReadExecGrants: grants}
	if err := saveProjectGrants(home, g); err != nil {
		t.Fatalf("save: %v", err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	torn := make(chan string, 1)

	wg.Add(1)
	go func() {
		defer wg.Done()
		// Closed however this ends, or the reader below spins for ever.
		defer close(stop)
		for i := 0; i < 300; i++ {
			if err := saveProjectGrants(home, g); err != nil {
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			data, err := os.ReadFile(path)
			if err != nil {
				runtime.Gosched() // a reader may legitimately miss the file mid-rename
				continue
			}
			var out projectGrants
			if jerr := json.Unmarshal(data, &out); jerr != nil {
				select {
				case torn <- fmt.Sprintf("read %d bytes that do not parse: %v", len(data), jerr):
				default:
				}
				return
			}
		}
	}()
	wg.Wait()

	select {
	case why := <-torn:
		t.Fatalf("a concurrent reader saw a partial record (%s); the write is not atomic, so an interrupted save would leave a file that cannot be read -- stranding every permission it named", why)
	default:
	}
}

// A withdrawal must be confirmed against the access-control list, not believed
// because icacls exited zero.
//
// icacls exits 0 whether it changed anything or not: on a path it cannot find it
// prints "Successfully processed 0 files; Failed processing 1 files" and still
// returns success -- measured, for both /grant and /remove:g. An entry travels
// with a directory that is renamed, so withdrawing by the recorded path removed
// nothing, reported success, and let the record be deleted while the permission
// lived on under the new name. Every "the withdrawal failed, keep the record"
// branch in this package was unreachable, because the condition could not occur.
func TestAWithdrawalThatRemovedNothingIsReportedAsFailure(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "tools")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sid, err := scopeCapabilitySID(dir)
	if err != nil {
		t.Skipf("cannot derive a capability SID here: %v", err)
	}
	if _, gerr := grantSandboxReadExec(sid, dir); gerr != nil {
		t.Skipf("cannot write an ACL in the test environment: %v", gerr)
	}

	renamed := filepath.Join(parent, "tools_renamed")
	if rerr := os.Rename(dir, renamed); rerr != nil {
		t.Skipf("cannot rename: %v", rerr)
	}
	t.Cleanup(func() { _ = revokeSandboxReadExec(sid, renamed) })

	// The permission is now on `renamed`; the record still names `dir`.
	if err := revokeSandboxReadExec(sid, dir); err == nil {
		t.Fatal("withdrawing from a path that no longer exists reported success; the record would be deleted while the permission lives on under the new name")
	}
	if !readExecEntryIsOurs(sid, renamed) {
		t.Fatal("test precondition: the entry did not travel with the rename")
	}
}

// A grant that did not land must not be reported as one that did, or the caller
// records a permission that does not exist and the sandbox fails to read a
// directory nvx said it had granted.
func TestAGrantThatWroteNothingIsReportedAsFailure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "never-created")
	sid, err := scopeCapabilitySID(dir)
	if err != nil {
		t.Skipf("cannot derive a capability SID here: %v", err)
	}
	if _, gerr := grantSandboxReadExec(sid, dir); gerr == nil {
		t.Fatal("granting on a path that does not exist reported success")
	}
}

// A permission nvx did not grant must never be withdrawn.
//
// Withdrawing takes an identity's whole entry, not one right from it, so a record
// naming a path whose entry is something broader must not be acted on. Measured
// before this guard existed: a project directory that was both the sandbox's
// writable root and named in allow_read_exec was recorded as a read/execute
// grant, and `nvx grants reset` deleted the sandbox's write access to the user's
// own project while reporting it had withdrawn a read/execute permission.
//
// This drives revokeSandboxReadExec itself against a real permission, not a
// helper: the two tests that covered this area asserted on the helper's return
// value and passed while the call site ignored it.
func TestAPermissionNvxDidNotGrantIsNeverWithdrawn(t *testing.T) {
	dir := t.TempDir()
	sid, err := scopeCapabilitySID(dir)
	if err != nil {
		t.Skipf("cannot derive a capability SID here: %v", err)
	}
	t.Cleanup(func() { _ = revokeACL(dir, sid) })

	// A modify permission, as nvx writes for a writable root -- not a read/execute
	// grant, and so not this feature's to remove.
	if err := grantSandboxModify(sid, dir); err != nil {
		t.Skipf("cannot write an ACL in the test environment: %v", err)
	}

	err = revokeSandboxReadExec(sid, dir)
	if err == nil {
		t.Fatal("withdrawing reported success against a permission nvx never granted")
	}
	if !errors.Is(err, errPermissionNotOurs) {
		t.Fatalf("error was %v, want one identifying the permission as not nvx's", err)
	}

	e, present, aerr := appContainerHomeAccess(sid, dir)
	if aerr != nil || !present {
		t.Fatalf("the modify permission was removed: present=%v err=%v", present, aerr)
	}
	if e&aclMaskModify != aclMaskModify {
		t.Fatalf("mask is now %#x; the write access nvx granted for another reason was destroyed", e)
	}
}

// ...and the decision not to record it has to be made before the record is
// written, because the record is written before the permission is granted.
func TestOnlyAPermissionThatWillBeOursIsRecorded(t *testing.T) {
	covered := t.TempDir()
	sid, err := scopeCapabilitySID(covered)
	if err != nil {
		t.Skipf("cannot derive a capability SID here: %v", err)
	}
	t.Cleanup(func() { _ = revokeACL(covered, sid) })
	if err := grantSandboxModify(sid, covered); err != nil {
		t.Skipf("cannot write an ACL in the test environment: %v", err)
	}
	if readExecGrantWouldBeOurs(sid, covered) {
		t.Error("a directory already carrying a broader permission was treated as nvx's to record; withdrawing it later would delete that permission")
	}

	// A directory with nothing on it is one nvx is about to write its own entry to.
	fresh := t.TempDir()
	freshSID, err := scopeCapabilitySID(fresh)
	if err != nil {
		t.Skipf("cannot derive a capability SID here: %v", err)
	}
	t.Cleanup(func() { _ = revokeACL(fresh, freshSID) })
	if !readExecGrantWouldBeOurs(freshSID, fresh) {
		t.Error("a directory nvx is about to grant was not treated as nvx's to record, so the grant would be untrackable")
	}
}

// An explicit deny must keep denying after nvx writes its own entry.
//
// Access control lists are order-sensitive: a deny placed after an allow does not
// take effect. A first version of this test read a temporary directory that had
// no explicit deny at all, so its assertion never ran -- it passed with the
// ordering reversed. It has to put a real deny there first.
func TestAnExplicitDenyStillDeniesAfterNvxWrites(t *testing.T) {
	dir := t.TempDir()
	ourSID, err := scopeCapabilitySID(dir)
	if err != nil {
		t.Skipf("cannot derive a capability SID here: %v", err)
	}
	// A different identity, so nvx's own write does not simply replace it.
	denySID, err := scopeCapabilitySID(t.TempDir())
	if err != nil {
		t.Skipf("cannot derive a second capability SID here: %v", err)
	}
	t.Cleanup(func() { _ = revokeACL(dir, ourSID) })

	if out, derr := runWinCmd(20*time.Second, "icacls", dir, "/deny", "*"+denySID+":(R)", "/c", "/q"); derr != nil {
		t.Skipf("cannot write a deny entry here: %v (%s)", derr, out)
	}
	before, err := readDACL(dir)
	if err != nil {
		t.Fatalf("readDACL: %v", err)
	}
	if countExplicitDenies(before) == 0 {
		t.Skip("the deny entry did not take in this environment")
	}

	if err := grantACL(dir, ourSID, aclMaskReadExec, nvxInheritFlags); err != nil {
		t.Fatalf("grantACL: %v", err)
	}

	after, err := readDACL(dir)
	if err != nil {
		t.Fatalf("readDACL after granting: %v", err)
	}
	if countExplicitDenies(after) != countExplicitDenies(before) {
		t.Fatalf("deny entries went from %d to %d: writing nvx's permission lost one",
			countExplicitDenies(before), countExplicitDenies(after))
	}
	lastDeny, firstAllow := -1, -1
	for i, e := range after {
		if e.Inherited {
			continue
		}
		if e.Deny {
			lastDeny = i
		} else if firstAllow < 0 {
			firstAllow = i
		}
	}
	if lastDeny < 0 {
		t.Fatal("the explicit deny is gone after nvx wrote its own permission")
	}
	if firstAllow >= 0 && lastDeny > firstAllow {
		t.Fatalf("the deny sits at %d, after an allow at %d; a deny placed after an allow does not take effect", lastDeny, firstAllow)
	}
}

func countExplicitDenies(entries []aclEntry) int {
	n := 0
	for _, e := range entries {
		if e.Deny && !e.Inherited {
			n++
		}
	}
	return n
}
