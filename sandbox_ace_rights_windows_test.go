//go:build windows

package main

import "testing"

// Matching an entry by SID alone reported the current design's own ancestor grant
// as a pre-0.5.0 leftover: the traverse grant nvx writes on the directories above
// a sandbox, so running nvx from a subdirectory leaves one on the project root.
// doctor announced it as a grant letting "any nvx sandbox read and write this
// project", which is false in every clause, and offered to remove it. The same
// scan drives the launch-path cleanup, so the bad match would have revoked a
// grant nvx had just written.
//
// The distinction is the access mask, so that is what these pin. They used to
// pin it against the text icacls prints, in every order and spacing it might use;
// with real masks the orderings and the parsing are gone and only the meaning is
// left.
func TestTraverseAccessIsDistinguishedFromRealAccess(t *testing.T) {
	stale := func(mask uint32) bool { return mask&^aclMaskTraverse != 0 }

	cases := []struct {
		mask  uint32
		stale bool
		why   string
	}{
		{aclMaskTraverse, false, "the ancestor traverse grant nvx writes today"},
		{fileExecute, false, "traverse alone: cannot read or write anything"},
		{fileReadAttributes, false, "read-attributes alone: metadata, not contents"},
		{0, false, "no access at all"},

		{aclMaskModify, true, "the legacy modify grant"},
		{aclMaskReadExec, true, "read+execute lets another sandbox LIST the project"},
		{fileGenericRead, true, "read"},
		{fileGenericWrite, true, "write"},
		{aclMaskTraverse | fileReadData, true, "adds read-data, which is a real read of the project"},
	}
	for _, tc := range cases {
		if got := stale(tc.mask); got != tc.stale {
			t.Errorf("mask %#x judged stale=%v, want %v -- %s", tc.mask, got, tc.stale, tc.why)
		}
	}
}

// Read/execute must never satisfy a request for write access.
//
// It did, and the consequence was unrecoverable: a directory granted by
// allow_read_exec and later used as a working directory kept its read-only entry
// for ever, because the modify grant saw the identity present and skipped itself.
// Nothing in the product could clear it -- repeat runs, `grants reset --all`,
// `doctor --fix` and deleting the policy entry all left every write in that
// directory failing with EPERM.
func TestReadExecAccessIsNotMistakenForWriteAccess(t *testing.T) {
	const sid = "S-1-15-3-1024-1111"
	readExec := aclEntry{SID: sid, Mask: aclMaskReadExec, Flags: nvxInheritFlags}

	if readExec.grantsAtLeast(grantModify.mask()) {
		t.Error("a read/execute entry satisfied a modify grant; the modify grant would be skipped and writes would fail for ever")
	}
	if !readExec.grantsAtLeast(grantReadExec.mask()) {
		t.Error("a read/execute entry did not satisfy a read/execute grant, so it would be re-granted every launch")
	}

	// Modify covers read/execute, so a directory already writable needs no second
	// grant to be readable.
	modify := aclEntry{SID: sid, Mask: aclMaskModify, Flags: nvxInheritFlags}
	for name, k := range map[string]grantKind{"modify": grantModify, "read/execute": grantReadExec} {
		if !modify.grantsAtLeast(k.mask()) {
			t.Errorf("a modify entry did not satisfy a %s grant", name)
		}
	}

	// A deny satisfies nothing, whatever its mask says.
	denied := aclEntry{SID: sid, Mask: aclMaskModify, Deny: true}
	if denied.grantsAtLeast(grantReadExec.mask()) {
		t.Error("a deny entry was read as granting access")
	}
}
