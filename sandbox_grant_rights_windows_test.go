//go:build windows

package main

import "testing"

// A read/execute ACE must not be mistaken for write access.
//
// It was, and the consequence was unrecoverable: a directory granted by
// allow_read_exec and later used as a working directory kept its read-only ACE
// for ever, because the modify grant saw the SID in the ACL and skipped itself.
// Nothing in the product could clear it -- repeat runs, `grants reset --all`,
// `doctor --fix` and deleting the policy entry all left every write in that
// directory failing with EPERM.
func TestReadExecAccessIsNotMistakenForWriteAccess(t *testing.T) {
	const sid = "S-1-15-3-1024-1111"
	readExecLine := "  " + sid + ":(OI)(CI)(RX)"

	if grantModify.satisfies(readExecLine) {
		t.Error("a read/execute entry was read as satisfying a modify grant; the modify grant would be skipped and writes would fail for ever")
	}
	if !grantReadExec.satisfies(readExecLine) {
		t.Error("a read/execute entry was not read as satisfying a read/execute grant, so it would be re-granted every launch")
	}

	// Modify covers read/execute, so a directory already writable needs no second
	// grant to be readable.
	modifyLine := "  " + sid + ":(OI)(CI)(M)"
	for name, k := range map[string]grantKind{"modify": grantModify, "read/execute": grantReadExec} {
		if !k.satisfies(modifyLine) {
			t.Errorf("a modify entry was not read as satisfying a %s grant", name)
		}
	}
	if !grantModify.satisfies("  " + sid + ":(OI)(CI)(F)") {
		t.Error("full control was not read as satisfying a modify grant")
	}
}

// The two rights must not share a cache slot, or removing one ACE by hand leaves
// the cache still claiming the other is in place -- which is how a hand-repaired
// directory ended up with no grant at all and could not even be entered.
func TestTheGrantCacheKeepsTheTwoRightsApart(t *testing.T) {
	const sid = "S-1-15-3-1024-2222"
	if grantIdentityFor(sid, grantModify) == grantIdentityFor(sid, grantReadExec) {
		t.Fatal("modify and read/execute share a cache key; one would mask the other")
	}
}
