//go:build windows

package main

// Every contained command on the development machine spent nine seconds writing
// ACLs before anything ran, on a launch npm itself finished in two. Two things
// were wrong, and these tests pin each one. See runtimeCapabilityName.

import (
	"path/filepath"
	"testing"
	"time"
)

// A traverse entry that is already on a directory is recognised as one, and not
// written again.
//
// The has-grant check asked for modify rights, which a traverse entry does not
// carry, so every launch found its ancestor grants "missing", wrote them again,
// and on ~/.nvx timed out and reported failure -- on every run, for every
// project, with the warning saying npx would fail while npx worked.
//
// Counted through the write hook rather than timed: the failure mode is a
// repeated write, and a write on a small directory is too fast to time.
func TestATraverseGrantAlreadyPresentIsNotWrittenAgain(t *testing.T) {
	dir := t.TempDir()
	sid, err := scopeCapabilitySID(dir)
	if err != nil {
		t.Skipf("cannot derive a capability SID here: %v", err)
	}
	t.Cleanup(func() { _ = revokeACL(dir, sid) })

	writes := 0
	fn := func(path, sidStr string, mask uint32, flags uint8) error {
		if filepath.Clean(path) == filepath.Clean(dir) {
			writes++
		}
		return writeDACLEntry(path, sidStr, mask, flags)
	}
	aclWriteFn.Store(&fn)
	t.Cleanup(func() { aclWriteFn.Store(nil) })

	if err := grantTraverseTimeboxed(sid, dir, directGrantTimeout); err != nil {
		t.Skipf("cannot write an ACL in the test environment: %v", err)
	}
	if writes != 1 {
		t.Fatalf("a fresh directory took %d writes, want 1", writes)
	}
	if err := grantTraverseTimeboxed(sid, dir, directGrantTimeout); err != nil {
		t.Fatalf("second grant: %v", err)
	}
	if writes != 1 {
		t.Fatalf("the traverse entry was written %d times; the second launch should have found the "+
			"first one. This is the repeat that cost every contained command three seconds of "+
			"timeouts and a warning about a grant that had not failed.", writes)
	}
}

// The runtime, the supervisor and the guest home's parent are granted to one
// identity every sandbox carries, so a launch that does not carry it cannot read
// the binary it is about to run. That identity is a capability, not the package:
// packages are per project, and a per-project entry on nvx's shared trees is
// what wrote eight identities onto one node install and thirty-nine onto
// sandbox_home.
func TestEveryLaunchCarriesTheRuntimeIdentity(t *testing.T) {
	want, err := runtimeCapabilitySID()
	if err != nil {
		t.Skipf("cannot derive a capability SID here: %v", err)
	}
	for _, c := range launchCapabilitySIDs([]string{"S-1-15-3-1024-1"}, nil) {
		if c == want {
			return
		}
	}
	t.Fatalf("launchCapabilitySIDs does not include the runtime identity %s; the container would "+
		"be unable to read node.exe", want)
}

// Only the guest home's parent is required. The chain above it was granted too,
// and ~/.nvx -- 51,218 entries beneath it here -- never finished inside the
// timebox; lstat of it from inside the sandbox works without the entry, through
// the profile root's own permissions.
func TestOnlyTheGuestHomeParentIsARequiredGrant(t *testing.T) {
	guest := filepath.Join(`C:\Users\someone\.nvx\sandbox_home`, "0123456789abcdef")
	got := guestHomeRequiredGrants(guest)
	if len(got) != 1 || got[0] != `C:\Users\someone\.nvx\sandbox_home` {
		t.Fatalf("required grants for %s = %v, want only its parent", guest, got)
	}
	if guestHomeRequiredGrants("") != nil {
		t.Fatal("an empty guest home has no required grants")
	}
}

// The direct-grant bound is the generous one. The ancestor walk's bound is tuned
// for grants a launch can do without; using it for a required grant is what
// turned "this directory is large" into "npx does not work".
func TestARequiredGrantGetsTheGenerousTimeout(t *testing.T) {
	if directGrantTimeout <= ancestorGrantPerPath {
		t.Fatalf("directGrantTimeout %v is not longer than the per-ancestor bound %v", directGrantTimeout, ancestorGrantPerPath)
	}
	if directGrantTimeout < 10*time.Second {
		t.Fatalf("directGrantTimeout %v is too short for a sandbox_home holding a few thousand sessions", directGrantTimeout)
	}
}
