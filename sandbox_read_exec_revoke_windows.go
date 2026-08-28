//go:build windows

package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// revokeSandboxReadExec removes the read/execute entry nvx granted sidStr on
// path. The inverse of grantSandboxReadExec, and deliberately narrow: it names
// the exact identity, so an entry someone else put on the same directory is
// untouched.
//
// The grant cache is cleared for this identity and path as well. Without that,
// the cache would keep answering "already granted" for the entry that was just
// removed -- which is not merely stale but actively harmful: it is what turned a
// hand-repaired directory into one with no grant at all, because the modify grant
// that should have replaced it was skipped too.
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

	if out, err := runWinCmd(45*time.Second, "icacls", path, "/remove:g", "*"+sidStr, "/c", "/q"); err != nil {
		return fmt.Errorf("icacls revoke for sandbox identity: %v (%s)", err, strings.TrimSpace(string(out)))
	}

	// Confirm against the access-control list itself rather than trusting the exit
	// code, which is the only signal icacls gives and does not carry this.
	//
	// icacls exits 0 whether it changed anything or not: on a path it could not
	// find it prints "Successfully processed 0 files; Failed processing 1 files"
	// and still returns success (measured, for both /grant and /remove:g). Every
	// "keep the record, the withdrawal failed" branch in this package was therefore
	// unreachable -- the condition they test could not be produced. Reading the
	// entry back is language-independent, unlike parsing that line, which is
	// localized.
	if readExecEntryIsOurs(sidStr, path) {
		return fmt.Errorf("the read/execute permission is still on %s after icacls reported removing it", path)
	}

	// The whole subtree, not just this path: the entry removed was inheritable, so
	// every descendant loses the access it had through it, and a descendant still
	// cached as granted would be skipped on the next launch and fail with EPERM.
	grantCacheForgetUnder(grantIdentityFor(sidStr, grantReadExec), path)
	grantCacheForgetUnder(grantIdentityFor(sidStr, grantModify), path)
	return nil
}
