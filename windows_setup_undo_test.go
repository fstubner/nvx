//go:build windows

package main

import (
	"errors"
	"testing"
)

// `nvx setup --undo` must not report success when it could not undo something.
//
// It warned on each failure and then printed "nvx sandbox setup removed." at
// exit 0 regardless. The loopback exemption is the one that matters most: while
// it is registered, this codebase's own documentation says the egress allowlist
// can be bypassed through any reachable loopback service — so a user could run
// the cleanup, see a tick, and still be exempt.
//
// Undo needs an Administrator terminal, so none of this is reachable from the
// gate; the operations are injected instead, which is the only way the failing
// path gets exercised at all.
func TestSetupUndoFailsWhenSomethingCouldNotBeUndone(t *testing.T) {
	ok := func(string, string) error { return nil }
	okExempt := func(bool, string) error { return nil }
	okClear := func(string) error { return nil }
	boom := errors.New("access denied")

	t.Run("everything succeeds", func(t *testing.T) {
		if code := runWindowsSetupUndo(tempDir(t), "S-1-15-3-1024-a", "S-1-15-2-b", ok, okExempt, okClear); code != 0 {
			t.Fatalf("exit %d with nothing failing; undo must be able to succeed", code)
		}
	})

	t.Run("the loopback exemption could not be removed", func(t *testing.T) {
		failExempt := func(bool, string) error { return boom }
		if code := runWindowsSetupUndo(tempDir(t), "S-1-15-3-1024-a", "S-1-15-2-b", ok, failExempt, okClear); code == 0 {
			t.Fatal("exit 0 while the loopback exemption is still registered; " +
				"the egress allowlist is bypassable in that state and the user was told it was cleaned up")
		}
	})

	t.Run("a grant could not be revoked", func(t *testing.T) {
		failRevoke := func(string, string) error { return boom }
		if code := runWindowsSetupUndo(tempDir(t), "S-1-15-3-1024-a", "S-1-15-2-b", failRevoke, okExempt, okClear); code == 0 {
			t.Fatal("exit 0 while a drive-root grant is still in place")
		}
	})

	t.Run("the state file could not be cleared", func(t *testing.T) {
		failClear := func(string) error { return boom }
		if code := runWindowsSetupUndo(tempDir(t), "S-1-15-3-1024-a", "S-1-15-2-b", ok, okExempt, failClear); code == 0 {
			t.Fatal("exit 0 while nvx still records a setup it says it removed")
		}
	})

	// With no legacy identity there is nothing to un-exempt, so a failing exempt
	// call is never made and must not be invented as a failure.
	t.Run("no legacy identity", func(t *testing.T) {
		called := false
		watchExempt := func(bool, string) error { called = true; return boom }
		if code := runWindowsSetupUndo(tempDir(t), "S-1-15-3-1024-a", "", ok, watchExempt, okClear); code != 0 {
			t.Fatalf("exit %d with nothing to undo beyond the grants", code)
		}
		if called {
			t.Error("tried to remove a loopback exemption for an identity that does not exist")
		}
	})
}
