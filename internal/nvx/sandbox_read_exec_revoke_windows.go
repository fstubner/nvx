//go:build windows

package nvx

import (
	"fmt"
	"os"
)

// revokeSandboxReadExec removes the read/execute entry nvx granted sidStr on
// path. The inverse of grantSandboxReadExec, and deliberately narrow: it names
// the exact identity, so an entry someone else put on the same directory is
// untouched.
func revokeSandboxReadExec(sidStr, path string) error {
	// The directory has to still be here, or there is nothing to remove the entry
	// from and no way to confirm one is gone.
	//
	// This is not a formality. An access-control entry travels with a directory
	// that is renamed, so the entry lives on under the new name while the recorded
	// path no longer resolves -- and icacls reports that as success. Measured: a
	// grant, a rename, and a withdrawal by the old name left the permission in
	// place while nvx logged that it had withdrawn it and deleted the record.
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("the directory is no longer at this path, so its permission could not be removed; "+
			"if it was renamed rather than deleted, the permission moved with it: %w", err)
	}

	// Only nvx's own read/execute entry may be removed.
	if !readExecEntryIsOurs(sidStr, path) {
		if _, present, _ := appContainerHomeAccess(sidStr, path); present {
			return fmt.Errorf("%w: %s", errPermissionBroadened, path)
		}
		// Nothing there for this identity at all: the record is stale and there is
		// nothing to remove.
		return errNothingToWithdraw
	}

	if err := revokeACL(path, sidStr); err != nil {
		return fmt.Errorf("withdraw the sandbox identity's permission: %w", err)
	}

	// Confirm the entry is gone by reading it back.
	//
	// The API reports its own failures now, so this is belt and braces rather than
	// the only signal -- but it is the cheap half of the check that caught the
	// original defect, and a withdrawal is exactly where a wrong answer is worst.
	if readExecEntryIsOurs(sidStr, path) {
		return fmt.Errorf("the read/execute permission is still on %s after it was reported removed", path)
	}

	return nil
}
