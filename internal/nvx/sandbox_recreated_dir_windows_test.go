//go:build windows

package nvx

import (
	"os"
	"path/filepath"
	"testing"
)

// A directory deleted and recreated must not be remembered as granted.
//
// `rm -rf proj && git clone`, a fresh CI workspace, a new worktree: the new
// directory carries none of the old one's entries, but the previous run had
// recorded the modify grant, and the record outlived the directory. The grant
// was skipped, nothing logged, and the contained process could not read its own
// working directory. Every package manager failed, npm included. Measured
// 2026-09-17: removing the one stale record made the same path work.
func TestARecreatedDirectoryIsNotRememberedAsGranted(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "proj")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	sid, err := scopeCapabilitySID(dir)
	if err != nil {
		t.Skipf("cannot derive a capability SID here: %v", err)
	}
	if err := grantSandboxModify(sid, dir); err != nil {
		t.Skipf("cannot write an ACL in the test environment: %v", err)
	}
	if !appContainerHasGrantFor(sid, dir, grantModify) {
		t.Fatal("the grant just written is not visible")
	}

	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = revokeACL(dir, sid) })

	if appContainerHasGrantFor(sid, dir, grantModify) {
		t.Fatal("a recreated directory is still reported as granted; the grant would be skipped and the sandbox could not enter it")
	}
	if err := grantSandboxModify(sid, dir); err != nil {
		t.Fatalf("re-grant: %v", err)
	}
	entries, err := readDACL(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if sidsEqual(e.SID, sid) && !e.Deny && e.grantsAtLeast(aclMaskModify) {
			return
		}
	}
	t.Fatal("after the re-grant the directory still carries no modify entry for the sandbox identity")
}
