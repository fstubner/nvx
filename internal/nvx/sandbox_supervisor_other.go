//go:build !windows

package nvx

// pruneUnusedSupervisors is a no-op off Windows. Only the AppContainer path
// stages a copy of the nvx binary: Landlock and Seatbelt apply isolation to the
// process nvx already launched, so there is nothing staged to reclaim.
func pruneUnusedSupervisors(nvxHome string) {}

// pruneStaleCommandCopies is a no-op off Windows, for the same reason: only
// the AppContainer path stages copies of commands.
func pruneStaleCommandCopies(nvxHome string, budget int) int { return 0 }
