//go:build windows

package main

import (
	_ "embed"
	"os"
	"path/filepath"
)

// walkupShimJS is preloaded into every contained node process so that a tool
// walking up to the drive root does not die on the directories an AppContainer
// may pass through but not stat. See the file itself for the measurement and
// for what it deliberately does not do.
//
// This is what makes contained `npx` work without an elevated `nvx setup`:
// npm's realpath stats C:\Users and C:\ on the way to its cache under the
// sandbox home, and neither is readable without a drive-root grant.
//
//go:embed sandbox_walkup_shim.js
var walkupShimJS string

const walkupShimName = "nvx-walkup-shim.js"

// writeWalkupShim drops the preload into the guest home and returns its path.
// The guest home for the same reason the stdio shim lives there: the sandbox
// can read it, and rewriting it changes only what the sandbox's own children do.
func writeWalkupShim(guestHome string) (string, error) {
	p := filepath.Join(guestHome, walkupShimName)
	if err := os.WriteFile(p, []byte(walkupShimJS), 0o600); err != nil {
		return "", err
	}
	return p, nil
}
