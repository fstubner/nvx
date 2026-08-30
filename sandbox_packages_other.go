//go:build !windows

package main

// cleanupSandboxPackages is a Windows concern: AppContainer packages exist only
// there. Landlock and Seatbelt sandboxes register nothing that needs sweeping.
func cleanupSandboxPackages() int { return 0 }
