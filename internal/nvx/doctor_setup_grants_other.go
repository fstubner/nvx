//go:build !windows

package nvx

// reportSetupGrants is Windows-only: `nvx setup`'s elevated ACL grants exist to
// let an AppContainer traverse to a working directory, and no other platform has
// either half of that. See doctor_setup_grants_windows.go.
func reportSetupGrants(string) bool { return true }
