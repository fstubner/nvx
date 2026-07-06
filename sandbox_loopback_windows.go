//go:build windows

package main

// windowsLoopbackAllowlistEnabled reports whether the admin loopback-allowlist
// opt-in is active (a stable, loopback-exempted AppContainer profile is
// registered so the egress proxy is reachable and egress is allowlisted).
// Fleshed out below; the stub keeps the default (internetClient direct) path.
func windowsLoopbackAllowlistEnabled(nvxHome string) bool {
	return false
}
