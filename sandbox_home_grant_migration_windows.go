//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// Upgraded machines keep an over-broad grant on ~/.nvx that fresh ones never get.
//
// nvx needs the sandbox to traverse ~/.nvx to reach the staged runtime and the
// guest home beneath it. 0.5.0 grants exactly that -- (X,RA), traverse and read
// attributes, which does not permit listing. Earlier versions granted (RX), which
// does: an acceptance pass listed 17 entries of nvx's own home from inside a
// sandbox, including `grants` and `audit.log`, on this machine while a fresh
// NVX_HOME correctly refused.
//
// It persists because appContainerHasGrant answers "is there any allow ACE", not
// "is it the right one", so the narrower grant is skipped as already present. The
// same check is used for ancestor grants that `nvx setup` deliberately makes (RX)
// on drive roots, so this narrowing is deliberately scoped to nvxHome rather than
// applied wherever a broad ACE is found.
//
// Once per home, marked on disk: it is a migration, and paying an ACL read on
// every launch to re-answer a question that cannot change again is the kind of
// per-launch cost that already had to be removed from the ancestor walk.

func homeGrantMigrationMarker(nvxHome string) string {
	return filepath.Join(nvxHome, "home-grant-narrowed")
}

// narrowLegacyHomeGrant replaces a pre-0.5.0 (RX) ACE on nvxHome with the
// traverse-only grant the current design uses. Best-effort and quiet: a machine
// that cannot rewrite the ACL is no worse off than before, and the grant it
// already has is what every earlier version shipped with.
func narrowLegacyHomeGrant(sidStr, nvxHome string) {
	if nvxHome == "" || sidStr == "" {
		return
	}
	marker := homeGrantMigrationMarker(nvxHome)
	if _, err := os.Stat(marker); err == nil {
		return
	}

	mask, present, err := appContainerHomeAccess(sidStr, nvxHome)
	if err != nil {
		return
	}
	// No entry, or already narrowed to traverse-only: nothing to do, and record it
	// so the check does not run again.
	if !present || mask&^aclMaskTraverse == 0 {
		_ = os.WriteFile(marker, []byte(fmt.Sprintf("%#x\n", mask)), 0o600)
		return
	}

	if err := replaceAceWithTraverseOnly(sidStr, nvxHome); err != nil {
		return
	}
	LogInfo("Narrowed an old permission on %s so the sandbox can no longer list it.", nvxHome)
	_ = os.WriteFile(marker, []byte(fmt.Sprintf("narrowed from %#x\n", mask)), 0o600)
}

// appContainerHomeAccess returns the access mask of the explicit allow entry for
// sidStr on path, and whether there is one.
//
// A mask rather than the rights text icacls prints. The text version had to strip
// a leading "(I)" for inherited entries and split "X,RA" in either order, and it
// read those from a line that begins with the directory's own path -- the same
// shape of mistake that hid a project's first entry from the stale-grant scan.
func appContainerHomeAccess(sidStr, path string) (uint32, bool, error) {
	e, ok, err := aclEntryFor(path, sidStr)
	if err != nil {
		return 0, false, err
	}
	if !ok || e.Deny {
		return 0, false, nil
	}
	return e.Mask, true, nil
}

func replaceAceWithTraverseOnly(sidStr, path string) error {
	// One write, not a removal followed by a grant: grantACL replaces this
	// identity's entry outright, so there is no window in which the permission is
	// absent and no way for the second half to fail after the first succeeded.
	if err := grantACL(path, sidStr, aclMaskTraverse, 0); err != nil {
		return fmt.Errorf("narrow the permission on %s: %w", path, err)
	}
	return nil
}
