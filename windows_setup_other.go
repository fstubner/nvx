//go:build !windows

package main

// runWindowsSetup is a no-op off Windows; the native sandbox there
// (Landlock/Seatbelt) needs no elevated one-time grants.
func runWindowsSetup(nvxHome string, undo, allDrives bool) int {
	LogInfo("nvx setup is only needed on Windows; the native sandbox on this OS requires no elevated setup.")
	return 0
}
