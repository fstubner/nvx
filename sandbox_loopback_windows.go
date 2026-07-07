//go:build windows

package main

// windowsLoopbackAllowlistEnabled reports whether `nvx setup` has registered the
// loopback exemption for the stable AppContainer SID. When true, the sandbox can
// reach the loopback egress proxy and egress is allowlisted; when false, the
// default internetClient-direct path is used.
func windowsLoopbackAllowlistEnabled(nvxHome string) bool {
	s, ok := readWindowsSetupState(nvxHome)
	return ok && s.LoopbackExempt
}
