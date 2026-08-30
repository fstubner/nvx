//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
)

// One AppContainer package per project, and taking them back again.
//
// Windows permits loopback WITHIN an AppContainer package. Every nvx sandbox
// used to share one package, so every port a contained process bound was
// reachable from every other contained process on the machine.
//
// Measured 2026-08-29, with both controls:
//
//	true host process   -> sandbox A's listener   DENIED (ETIMEDOUT)
//	sandbox B           -> a host listener        DENIED (ETIMEDOUT)
//	sandbox B (project B) -> sandbox A's listener GOT SANDBOX_A_SECRET
//
// So sandbox B had no general loopback access -- it could not reach the host at
// all -- and could still reach another sandbox. An unrelated project, an empty
// allowlist, no --connect, no --expose, no loopback exemption on the machine.
//
// That defeats the egress allowlist outright: one sandbox with a permissive
// allowlist relays for one without, which an acceptance pass demonstrated by
// completing a TLS exchange with a host the calling project had never
// allowlisted. It also hands two projects a channel that the per-project
// filesystem identity exists to deny.
//
// The cause was confirmed to be the package rather than the capability by
// varying only the profile name: with two names the same connection was refused
// twice, with one name it succeeded.
//
// The cost is that profiles accumulate -- one registry entry and one folder
// under %LOCALAPPDATA%\Packages per project nvx has ever contained. That is what
// the sweep below is for. Deleting one is safe at any time: the next launch in
// that project recreates it, and CreateAppContainerProfile is idempotent.

// nvxPackagePrefix is what every per-project package name starts with. The bare
// stableSandboxProfile does not match it, which is deliberate: that profile is
// still the one `nvx doctor` derives a SID from to check for a leftover
// pre-0.5.0 loopback exemption, and removing it would break that check.
var nvxPackagePrefix = stableSandboxProfile + "."

// cleanupSandboxPackages deletes the per-project AppContainer profiles nvx has
// registered, and reports how many went.
//
// Enumerated from %LOCALAPPDATA%\Packages rather than from the registry: Windows
// creates a folder there named after each profile, so the directory listing is
// the list of names without any registry walking, and DeleteAppContainerProfile
// takes exactly that name.
//
// Only called when no sandbox session is still running, so nothing is deleted
// out from under a live container.
func cleanupSandboxPackages() int {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return 0
	}
	entries, err := os.ReadDir(filepath.Join(local, "Packages"))
	if err != nil {
		return 0
	}
	removed := 0
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), nvxPackagePrefix) {
			continue
		}
		deleteAppContainerProfile(e.Name())
		removed++
	}
	return removed
}
