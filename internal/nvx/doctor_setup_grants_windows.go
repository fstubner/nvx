//go:build windows

package nvx

import (
	"fmt"
)

// Checking that what `nvx setup` granted is still granted, and still granted to
// the identity this build launches under.
//
// An elevated `nvx setup` writes a read/execute ACE for the nvx.setup.driveroots
// capability onto the drive root and the profile parent, and records the SID it
// used. A contained process needs traverse on every ancestor of its working
// directory, so if either of those goes missing the launch fails with a bare
// "Access is denied" that names no path and no cause.
//
// Two ways it goes wrong, and doctor could see neither:
//
//   - The ACE is gone. An unrelated tool reset the ACL, or `setup --undo` ran.
//   - The ACE is for an identity this build no longer launches under. Measured
//     2026-08-30: on an account whose windows-setup.json recorded the older
//     package SID, `npx` failed with a raw "EPERM: operation not permitted,
//     lstat" out of npm's own dependency walker and nvx said nothing at all.
//
// noteMissingElevatedGrants already reports the first at runtime, but only under
// --verbose and only while running a contained command -- so the person whose
// command just died with EPERM has to know to re-run it verbose to find out.
// doctor is where someone goes when it is already broken.
//
// This reports rather than repairs. Only an elevated process can write these
// ACEs, and doctor does not elevate.

// reportSetupGrants prints what `nvx setup` left behind and whether it is still
// usable. It returns false only when something is actually wrong, so a machine
// that never ran setup is not reported as broken: setup is optional, and a
// machine without it simply carries a capability nothing has granted to.
func reportSetupGrants(nvxHome string) bool {
	state, ok := readWindowsSetupState(nvxHome)
	if !ok {
		// Not a fault. Plenty of machines never need the elevated grants.
		return true
	}

	current, err := deriveCapabilitySIDString(setupCapabilityName)
	if err != nil {
		fmt.Printf("  [WARN] could not work out which identity nvx launches under: %v\n", err)
		return true
	}

	if state.AppContainerSID != "" && state.AppContainerSID != current {
		fmt.Println("  [FAIL] `nvx setup` granted a different identity than this nvx launches under")
		fmt.Printf("         granted to: %s\n", state.AppContainerSID)
		fmt.Printf("         now using:  %s\n", current)
		fmt.Println("         Contained commands fail on paths those grants were meant to cover,")
		fmt.Println("         usually as an EPERM from the package manager rather than an nvx error.")
		fmt.Println("         Fix: re-run `nvx setup` from an Administrator terminal.")
		return false
	}

	var missing []string
	for _, path := range state.GrantedPaths {
		if !driveRootHasGrant(current, path) {
			missing = append(missing, path)
		}
	}
	if len(missing) > 0 {
		fmt.Println("  [FAIL] grants `nvx setup` made are no longer on disk:")
		for _, path := range missing {
			fmt.Printf("         %s\n", path)
		}
		fmt.Println("         A contained process needs traverse on every ancestor of its working")
		fmt.Println("         directory, so commands under those paths cannot start.")
		fmt.Println("         Fix: re-run `nvx setup` from an Administrator terminal.")
		return false
	}

	fmt.Printf("  [OK]   `nvx setup` grants are in place on %d path(s)\n", len(state.GrantedPaths))
	return true
}
