//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The bug was that "is there an entry" was used where "is it the RIGHT entry"
// was meant, so a pre-0.5.0 read+execute on ~/.nvx was skipped as already granted
// and the sandbox could list nvx's own home for ever. This pins the distinction.
//
// It used to enumerate the orderings and spacings icacls might print ("X,RA",
// "RA,X", "X, RA"). Masks have no orderings.
func TestTraverseOnlyGrantIsDistinguishedFromLegacyReadExec(t *testing.T) {
	traverseOnly := func(mask uint32) bool { return mask&^aclMaskTraverse == 0 }

	if !traverseOnly(aclMaskTraverse) {
		t.Error("the traverse grant nvx writes was not recognised as traverse-only")
	}
	for name, mask := range map[string]uint32{
		"the legacy read+execute grant, which permits listing": aclMaskReadExec,
		"modify":              aclMaskModify,
		"read":                fileGenericRead,
		"traverse plus write": aclMaskTraverse | fileGenericWrite,
	} {
		if traverseOnly(mask) {
			t.Errorf("%s was accepted as traverse-only", name)
		}
	}
}

func TestHomeAccessIsReadAsRightsNotMerePresence(t *testing.T) {
	dir := tempDir(t)
	sid, serr := scopeCapabilitySID(dir)
	if serr != nil {
		t.Skipf("cannot derive a SID here: %v", serr)
	}
	t.Cleanup(func() { _ = revokeACL(dir, sid) })

	// Nothing granted yet.
	if _, present, err := appContainerHomeAccess(sid, dir); err != nil {
		t.Fatalf("reading a fresh directory: %v", err)
	} else if present {
		t.Error("a fresh directory reported an entry for this identity")
	}

	// Grant the legacy shape and confirm it is reported as read+execute, not
	// merely "present" -- that difference is the whole fix.
	if err := grantACL(dir, sid, aclMaskReadExec, 0); err != nil {
		t.Skipf("cannot set a permission here: %v", err)
	}
	mask, present, err := appContainerHomeAccess(sid, dir)
	if err != nil || !present {
		t.Fatalf("after granting read/execute: mask=%#x present=%v err=%v", mask, present, err)
	}
	if mask&^aclMaskTraverse == 0 {
		t.Error("the legacy read/execute grant was accepted as traverse-only; it permits listing")
	}

	// Narrowing must leave a traverse-only entry behind.
	if err := replaceAceWithTraverseOnly(sid, dir); err != nil {
		t.Fatalf("replaceAceWithTraverseOnly: %v", err)
	}
	mask, present, err = appContainerHomeAccess(sid, dir)
	if err != nil || !present {
		t.Fatalf("after narrowing: mask=%#x present=%v err=%v", mask, present, err)
	}
	if mask&^aclMaskTraverse != 0 {
		t.Errorf("after narrowing the mask is %#x, want traverse-only %#x", mask, aclMaskTraverse)
	}
}

// The migration must run once and then stop, or it pays two ACL reads on
// every launch to re-answer a question whose answer cannot change.
func TestHomeGrantMigrationRunsOnce(t *testing.T) {
	const sid = "S-1-15-2-1-2-3-4-5-6-7"
	home := tempDir(t)
	marker := homeGrantMigrationMarker(home)

	if _, err := os.Stat(marker); err == nil {
		t.Fatal("marker exists before the migration ran")
	}
	narrowLegacyHomeGrant(sid, home)
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("no marker after the migration ran: %v", err)
	}

	// A second call must be a no-op even if the ACL changed underneath, which is
	// what "once per home" means.
	before, _ := os.ReadFile(marker)
	if err := os.WriteFile(filepath.Join(home, "sentinel"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	narrowLegacyHomeGrant(sid, home)
	after, _ := os.ReadFile(marker)
	if string(before) != string(after) {
		t.Errorf("second run rewrote the marker (%q -> %q); it should have returned early", before, after)
	}
}
