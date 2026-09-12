//go:build windows

package nvx

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Each revoke `nvx setup --undo` performs is time-boxed, like each grant.
//
// The undo swept every ancestor path and the profile root through an
// unbounded DACL write. Every grant nvx makes is bounded -- fifteen seconds
// for one the launch cannot do without, a three-second budget for the rest --
// because a filter driver over the profile root can stall an ACL write
// indefinitely, and one did. The revoke had no bound at all, and on the profile
// root the write propagates over the whole tree, so `--undo` after a setup on a
// large profile appeared to hang with nothing to say which path.
//
// The stall is injected through the same hook the grant tests use. Before the
// fix the revoke did not go through the hooked, time-boxed path at all, so the
// injected stall was bypassed and this returned at once with no error.
func TestSetupUndoRevokesAreTimeBoxed(t *testing.T) {
	stall := func(path, sidStr string, mask uint32, flags uint8) error {
		time.Sleep(400 * time.Millisecond)
		return nil
	}
	aclWriteFn.Store(&stall)
	t.Cleanup(func() { aclWriteFn.Store(nil) })

	orig := undoRevokeTimeout
	undoRevokeTimeout = 100 * time.Millisecond
	t.Cleanup(func() { undoRevokeTimeout = orig })

	start := time.Now()
	err := revokeSidGrant("S-1-15-3-1024-1-2-3-4-5-6-7-8", filepath.Join(tempDir(t), "undo-path"))
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("the revoke returned no error in %v although its write was stalled; it is not going through the time-boxed path", elapsed)
	}
	if !strings.Contains(err.Error(), "did not complete within") {
		t.Fatalf("the revoke failed for a different reason than the time-box: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("the revoke waited %v for a write abandoned at %v", elapsed, undoRevokeTimeout)
	}
	// Let the stalled writer finish, so its bookkeeping does not leak into the
	// next test in this process.
	time.Sleep(400 * time.Millisecond)
}
