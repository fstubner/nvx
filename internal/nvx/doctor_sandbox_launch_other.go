//go:build !windows && !linux

package nvx

// The sandbox-launch check is not implemented on macOS.
//
// Seatbelt is applied through sandbox-exec, which reports a profile it cannot
// apply as the command's own error, already attributed. Windows and Linux have a
// control launch because their failures predate the command: an AppContainer
// CreateProcess refused, or namespaces the kernel will not create. A macOS
// launch check would be a small addition on the Seatbelt provider, but it has no
// measured failure behind it yet, so doctor says nothing rather than guess.
func reportSandboxLaunch(nvxHome string) bool { return true }
