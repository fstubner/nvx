//go:build !windows

package main

// reportStaleProjectGrantsHere is a no-op off Windows. The concern is specific to
// AppContainer package SIDs left on a directory's ACL: Landlock and Seatbelt grant
// access per launch and persist nothing, so there is no leftover to find.
func reportStaleProjectGrantsHere(fix bool) bool {
	return false
}
