//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
// cache must forget the subtree. Forgetting only the exact path left descendants
// cached as granted: nvx logged a grant it then skipped, and the sandbox got
// EPERM on a directory the policy still named, for the full cache lifetime.
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
	t.Cleanup(func() { _ = revokeSandboxReadExec(sid, parent) })

	if _, err := grantSandboxReadExec(sid, parent); err != nil {
		t.Skipf("cannot write an ACL in the test environment: %v", err)
	}
	// The child is covered by inheritance, so a lookup caches it as granted.
	if !appContainerHasGrantFor(sid, child, grantReadExec) {
		t.Skip("the child did not inherit the entry in this environment")
	}

	if err := revokeSandboxReadExec(sid, parent); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if appContainerHasGrantFor(sid, child, grantReadExec) {
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

// The record is written atomically, so an interrupted write cannot leave a
// half-written file -- which would not parse, stranding every permission it named.
func TestTheRecordIsWrittenAtomically(t *testing.T) {
	home := t.TempDir()
	scope := t.TempDir()
	g := projectGrants{ProjectPath: scope, ReadExecGrants: []readExecGrant{{Path: `C:\x`, SID: "S-1-15-3-1024-1"}}}
	if err := saveProjectGrants(home, g); err != nil {
		t.Fatalf("save: %v", err)
	}
	entries, err := os.ReadDir(grantsDir(home))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("a temporary file was left behind (%s); the write is not completing with a rename", e.Name())
		}
	}
	if _, ok := readGrantsFile(grantsPath(home, scope)); !ok {
		t.Fatal("the record just written does not parse")
	}
}
