//go:build windows

package nvx

import (
	"errors"
	"path/filepath"
	"strings"
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

// --undo must revoke what setup recorded granting. It revoked only a fixed list,
// so a grant setup made on the Users directory of another volume, or on a
// working directory's volume that is not fixed, stayed after undo said
// "removed".
func TestSetupUndoRevokesTheRecordedPaths(t *testing.T) {
	nvxHome := tempDir(t)
	recorded := tempDir(t)
	if err := writeWindowsSetupState(nvxHome, windowsSetupState{GrantedPaths: []string{recorded}}); err != nil {
		t.Fatal(err)
	}
	var revoked []string
	revoke := func(_, p string) error { revoked = append(revoked, p); return nil }
	okExempt := func(bool, string) error { return nil }
	okClear := func(string) error { return nil }
	if code := runWindowsSetupUndo(nvxHome, "S-1-15-3-1024-a", "", revoke, okExempt, okClear); code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, p := range revoked {
		if strings.EqualFold(p, filepath.Clean(recorded)) {
			return
		}
	}
	t.Fatalf("undo never revoked %s, which setup recorded granting; revoked %v", recorded, revoked)
}

// Setup must not report a loopback exemption as absent when removing it failed
// and it is still registered. It treated every failed delete, a timeout
// included, as "nothing to remove".
func TestSetupNoticesALoopbackExemptionThatWasNotRemoved(t *testing.T) {
	const legacy = "S-1-15-2-1-2-3-4-5-6-7"
	failDelete := func(bool, string) error { return errors.New("timed out") }
	stillThere := func() ([]string, error) { return []string{legacy}, nil }
	gone := func() ([]string, error) { return nil, nil }

	if removeLegacyLoopbackExemption(legacy, failDelete, stillThere) {
		t.Error("reported the exemption removed while the list still carries it")
	}
	if !removeLegacyLoopbackExemption(legacy, failDelete, gone) {
		t.Error("a failed delete on a machine with no exemption is not a failure")
	}
	if removeLegacyLoopbackExemption(legacy, failDelete, func() ([]string, error) { return nil, errors.New("no tool") }) {
		t.Error("neither removed nor checked, and reported as gone")
	}
}
