//go:build !windows

package main

// pruneUnusedSupervisors is a no-op off Windows. Only the AppContainer path
// stages a copy of the nvx binary: Landlock and Seatbelt apply isolation to the
// process nvx already launched, so there is nothing staged to reclaim.
func pruneUnusedSupervisors(nvxHome string) {}
