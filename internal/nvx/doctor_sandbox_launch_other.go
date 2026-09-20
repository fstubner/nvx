//go:build !windows

package nvx

// The sandbox-launch check is Windows-only, because the failure it detects is.
//
// An AppContainer launch is a CreateProcess that can be refused for reasons
// having nothing to do with nvx -- commit exhaustion, security software, the
// Windows edition. Landlock and Seatbelt are applied by the process to itself
// after it has already started, so there is no equivalent "the sandbox would
// not start" state for doctor to find: a Landlock or Seatbelt failure surfaces
// as the command's own error, already attributed.
func reportSandboxLaunch(nvxHome string) bool { return true }
