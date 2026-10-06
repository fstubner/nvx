//go:build windows

package nvx

import (
	"fmt"
	"os"
	"strings"
)

// Reporting what an older `nvx setup` left on this machine.
//
// Older versions asked for an elevated `nvx setup` that granted the sandbox read
// and list access on drive roots and Users folders. Nothing needs those grants:
// the preload in sandbox_walkup_shim.js answers the stats they were for. Where
// they are still on disk no launch carries the identity they name, so they admit
// nothing. They are permissions on the machine's drive roots that nobody uses, and
// `nvx setup` now exists to remove them.
//
// A note and never a failure. Nothing is broken, so the machine is not reported
// unhealthy. Doctor reports and does not repair, since only an elevated process
// can write these entries.

// reportSetupLeftovers prints the drive-root and Users-folder access an older
// setup left, and how to remove it. It prints nothing on a machine that has none.
func reportSetupLeftovers(nvxHome string) {
	workDir, _ := os.Getwd()
	reportSetupLeftoversWith(nvxHome, workDir, aclHasAnyEntry)
}

func reportSetupLeftoversWith(nvxHome, workDir string, hasEntry func(sid, path string) bool) {
	capSID, legacySID, err := setupIdentities()
	if err != nil {
		return
	}
	paths, _ := setupLeftoverPaths(nvxHome, workDir)
	entries := findSetupLeftovers([]string{capSID, legacySID}, paths, hasEntry)
	if len(entries) == 0 {
		return
	}
	var where []string
	for _, e := range entries {
		where = append(where, e.Path)
	}
	fmt.Println("  [note] an older 'nvx setup' left sandbox access on " + strings.Join(where, ", "))
	fmt.Println("         nvx does not need it, no sandbox carries the identity it names, and nothing is broken")
	fmt.Println("         to remove it, from an Administrator terminal: nvx setup")
}
