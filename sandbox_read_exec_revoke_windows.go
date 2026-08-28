//go:build windows

package main

import (
	"fmt"
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
	out, err := runWinCmd(45*time.Second, "icacls", path, "/remove:g", "*"+sidStr, "/c", "/q")
	if err != nil {
		return fmt.Errorf("icacls revoke for sandbox identity: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	grantCacheForget(grantIdentityFor(sidStr, grantReadExec), path)
	grantCacheForget(grantIdentityFor(sidStr, grantModify), path)
	return nil
}
