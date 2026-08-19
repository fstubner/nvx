//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
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
// Once per home, marked on disk: it is a migration, and paying an icacls read on
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

	rights, err := appContainerAceRights(sidStr, nvxHome)
	if err != nil {
		return
	}
	// No ACE, or already narrowed to traverse-only: nothing to do, and record it
	// so the check does not run again.
	if rights == "" || aceIsTraverseOnly(rights) {
		_ = os.WriteFile(marker, []byte(rights+"\n"), 0o600)
		return
	}

	if err := replaceAceWithTraverseOnly(sidStr, nvxHome); err != nil {
		return
	}
	LogInfo("Narrowed an old permission on %s so the sandbox can no longer list it.", nvxHome)
	_ = os.WriteFile(marker, []byte("narrowed from "+rights+"\n"), 0o600)
}

// appContainerAceRights returns the rights string of the explicit allow ACE for
// sidStr on path -- e.g. "RX" or "X,RA" -- or "" when there is none.
func appContainerAceRights(sidStr, path string) (string, error) {
	out, err := runWinCmd(10*time.Second, "icacls", path)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, sidStr) {
			continue
		}
		if strings.Contains(strings.ToUpper(line), "(DENY)") {
			return "", nil
		}
		// icacls prints "<sid>:(RX)" or "<sid>:(X,RA)"; inherited ACEs carry an
		// extra (I) which this deliberately keeps out of the comparison.
		_, rest, ok := strings.Cut(line, sidStr+":")
		if !ok {
			continue
		}
		rights := strings.TrimSpace(rest)
		rights = strings.TrimPrefix(rights, "(I)")
		rights = strings.TrimSpace(rights)
		rights = strings.TrimPrefix(rights, "(")
		rights = strings.TrimSuffix(rights, ")")
		return strings.TrimSpace(rights), nil
	}
	return "", nil
}

// aceIsTraverseOnly reports whether a rights string is the traverse+read-attributes
// grant the current design uses, in either order icacls may print it.
func aceIsTraverseOnly(rights string) bool {
	parts := strings.Split(strings.ToUpper(strings.ReplaceAll(rights, " ", "")), ",")
	if len(parts) != 2 {
		return false
	}
	seen := map[string]bool{parts[0]: true, parts[1]: true}
	return seen["X"] && seen["RA"]
}

func replaceAceWithTraverseOnly(sidStr, path string) error {
	if out, err := runWinCmd(20*time.Second, "icacls", path, "/remove:g", "*"+sidStr, "/c", "/q"); err != nil {
		return fmt.Errorf("icacls remove %s: %v (%s)", path, err, strings.TrimSpace(string(out)))
	}
	grantArg := fmt.Sprintf("*%s:(X,RA)", sidStr)
	if out, err := runWinCmd(20*time.Second, "icacls", path, "/grant", grantArg, "/c", "/q"); err != nil {
		return fmt.Errorf("icacls grant %s: %v (%s)", path, err, strings.TrimSpace(string(out)))
	}
	return nil
}
