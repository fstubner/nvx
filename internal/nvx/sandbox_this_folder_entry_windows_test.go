//go:build windows

package nvx

import (
	"os"
	"path/filepath"
	"testing"
)

// A traverse entry has to carry SYNCHRONIZE, because CreateFile asks for it on
// every open and a capability entry must grant every bit asked for.
//
// Without it, a runtime that opens a directory to stat it -- libuv up to 1.43,
// so Node 18, so pnpm's standalone binary -- got EPERM on ~/.nvx and
// sandbox_home while both carried (X,RA). Node 24 lists the parent instead when
// the open fails, and so hid the defect from every npm-based check. Measured
// 2026-09-17: adding the bit to the two entries made the same stat succeed.
func TestTheTraverseEntryGrantsSynchronize(t *testing.T) {
	if aclMaskTraverse&synchronizeAccess == 0 {
		t.Fatal("the traverse mask lacks SYNCHRONIZE; a runtime on Node 18's libuv cannot stat a directory that carries only this entry")
	}
}

// A this-folder entry is written without touching anything beneath the
// directory, and without disturbing what the directory inherits.
//
// The propagating write costs a walk over every descendant -- 22 s on ~/.nvx
// here -- for an entry no descendant can see. The write that avoids the walk
// rebuilds the list itself, so this checks the three things that rebuild could
// get wrong: the entry lands with no inheritance flags, the inherited entries
// are still there and still marked inherited, and a child created afterwards
// still inherits from the directory's own inheritable entries.
func TestAThisFolderEntryLeavesDescendantsAndInheritanceAlone(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "child")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	sid, err := scopeCapabilitySID(dir)
	if err != nil {
		t.Skipf("cannot derive a capability SID here: %v", err)
	}

	before, err := readDACL(dir)
	if err != nil {
		t.Fatal(err)
	}
	inheritedBefore := 0
	for _, e := range before {
		if e.Inherited {
			inheritedBefore++
		}
	}
	if inheritedBefore == 0 {
		t.Skip("the temp directory inherits nothing here, so inheritance cannot be checked")
	}
	childBefore, err := readDACL(child)
	if err != nil {
		t.Fatal(err)
	}

	if err := writeThisFolderEntry(dir, sid, aclMaskTraverse); err != nil {
		t.Skipf("cannot write an ACL in the test environment: %v", err)
	}
	t.Cleanup(func() { _ = revokeACL(dir, sid) })

	after, err := readDACL(dir)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	inheritedAfter := 0
	for _, e := range after {
		if e.Inherited {
			inheritedAfter++
		}
		if sidsEqual(e.SID, sid) {
			found = true
			if e.Flags != 0 {
				t.Errorf("the entry was written with inheritance flags %#x; it must apply to this folder only", e.Flags)
			}
			if e.Mask != aclMaskTraverse {
				t.Errorf("the entry grants %#x, want %#x", e.Mask, aclMaskTraverse)
			}
		}
	}
	if !found {
		t.Fatal("the entry was not written")
	}
	if inheritedAfter != inheritedBefore {
		t.Errorf("the directory had %d inherited entries before the write and %d after", inheritedBefore, inheritedAfter)
	}

	childAfter, err := readDACL(child)
	if err != nil {
		t.Fatal(err)
	}
	if len(childAfter) != len(childBefore) {
		t.Errorf("the child's list changed from %d to %d entries; a this-folder write must not touch descendants", len(childBefore), len(childAfter))
	}
	for _, e := range childAfter {
		if sidsEqual(e.SID, sid) {
			t.Errorf("the child carries the entry; it was meant for the parent only")
		}
	}

	// Inheritance from the directory must still work for what comes later.
	later := filepath.Join(dir, "later")
	if err := os.MkdirAll(later, 0o700); err != nil {
		t.Fatal(err)
	}
	laterEntries, err := readDACL(later)
	if err != nil {
		t.Fatal(err)
	}
	inheritedLater := 0
	for _, e := range laterEntries {
		if e.Inherited {
			inheritedLater++
		}
		if sidsEqual(e.SID, sid) {
			t.Errorf("a directory created afterwards inherited the this-folder entry")
		}
	}
	if inheritedLater == 0 {
		t.Error("a directory created after the write inherits nothing; the write broke inheritance from its parent")
	}
}

// Replacing or removing an inheritable entry has to reach the descendants that
// inherited it. The this-folder write skips them, so for that case it must
// hand over to the write that walks them.
func TestAThisFolderWriteOverAnInheritableEntryReachesDescendants(t *testing.T) {
	const sid = "S-1-15-3-1024-1313131313-2424242424-3535353535-1616161616-2727272727-3838383838-1919191919"
	for _, mask := range []uint32{aclMaskTraverse, 0} {
		dir := tempDir(t)
		child := filepath.Join(dir, "child")
		if err := os.MkdirAll(child, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := grantACL(dir, sid, aclMaskReadExec, nvxInheritFlags); err != nil {
			t.Fatalf("inheritable grant: %v", err)
		}
		if !childCarriesInherited(t, child, sid) {
			t.Fatal("test setup: the child did not inherit the entry")
		}
		if err := writeThisFolderEntry(dir, sid, mask); err != nil {
			t.Fatalf("mask %#x: %v", mask, err)
		}
		if childCarriesInherited(t, child, sid) {
			t.Errorf("mask %#x: the child still carries an inherited copy of an entry its parent no longer has", mask)
		}
	}
}

func childCarriesInherited(t *testing.T, path, sid string) bool {
	t.Helper()
	entries, err := readDACL(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Inherited && sidsEqual(e.SID, sid) {
			return true
		}
	}
	return false
}
