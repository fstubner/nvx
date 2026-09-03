//go:build !windows

package main

// pregrantRuntimeForSandbox has nothing to do off Windows: Landlock and
// Seatbelt are told what a process may reach when it launches, rather than by
// writing an ACL onto the filesystem beforehand. See the Windows file.
func pregrantRuntimeForSandbox(nvxHome, runtimeName, version string) {}
