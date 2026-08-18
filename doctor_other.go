//go:build !windows

package main

// repairPersistentPath is a no-op on non-Windows: POSIX shells get the shim dir
// fronted by the `nvx env` snippet in the user's profile, so there is no
// separate persistent PATH store to repair.
func repairPersistentPath(nvxHome string, apply bool) (bool, error) {
	return false, nil
}
