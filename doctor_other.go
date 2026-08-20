//go:build !windows

package main

// repairPersistentPath is a no-op on non-Windows: POSIX shells get the shim dir
// fronted by the `nvx env` snippet in the user's profile, so there is no
// separate persistent PATH store to repair.
func repairPersistentPathImpl(nvxHome string, apply bool) (bool, error) {
	return false, nil
}

// reportSandboxWeakeners is a no-op off Windows. The one weakener it looks for --
// a leftover AppContainer loopback exemption -- has no equivalent under Landlock
// or Seatbelt, where network policy is set per launch and nothing persists.
func reportSandboxWeakeners(nvxHome string) bool {
	return false
}
