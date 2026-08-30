//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// One AppContainer package per project, and reclaiming them again.
//
// Windows permits loopback *within* an AppContainer package. Until 2026-08-29
// nvx ran every sandbox on a machine under one package, so any port a contained
// process bound was reachable from every other contained process.
//
// Measured with both controls, which is what pins it to the package rather than
// to loopback generally:
//
//	true host process     -> sandbox A's listener   DENIED (ETIMEDOUT)
//	sandbox B             -> a listener on the host DENIED (ETIMEDOUT)
//	sandbox B (project B) -> sandbox A's listener   GOT the payload
//
// Confirmed to be the package identity by varying only the profile name: the
// same connection was refused for two different names and succeeded when both
// sides shared one.
//
// The cost is that profiles accumulate: one registry entry and one folder under
// %LOCALAPPDATA%\Packages for every project nvx has ever contained. Nothing else
// removes them, so this file does, on the same terms as the guest-home sweep --
// automatically, after a command, bounded, and never touching one that is in
// use. A first version only ran from `nvx cleanup` and only when no session was
// running at all, which on a machine with a few long-lived MCP servers meant
// never.

// nvxPackagePrefix is what every per-project package name starts with. The bare
// stableSandboxProfile does not match it, deliberately: that name is still the
// one `nvx doctor` derives a SID from to check for a leftover pre-0.5.0 loopback
// exemption, and sweeping it would break that check. It is also the package
// every pre-2026-08-29 nvx launched under, so leaving it alone means a sandbox
// started by an older build on this machine is never swept out from under.
var nvxPackagePrefix = stableSandboxProfile + "."

// sandboxPackageRetention is how long an unused package profile is kept.
//
// Not zero, because deleting one the moment its session ends would mean
// recreating it on the very next run in that project -- a registry write added
// to the startup path of a product whose stated constraint is that overhead
// stays invisible. A week keeps the common case (working on the same handful of
// projects) free of any profile churn at all, while a project abandoned months
// ago does not leave a profile behind for ever.
const sandboxPackageRetention = 7 * 24 * time.Hour

// packageUseFile is where nvx records that a package was used, since Windows
// does not. Measured 2026-08-30: the mtime of %LOCALAPPDATA%\Packages\<name> is
// unchanged by launching a container under that package, so it says when the
// profile was created and nothing about whether anyone still wants it.
func packageUseFile(nvxHome, pkgName string) string {
	return filepath.Join(nvxHome, "packages", pkgName)
}

// noteSandboxPackageUse records that pkgName was used just now.
//
// Best-effort: a package whose use cannot be recorded is still a working
// sandbox, and the only consequence is that the sweep may reclaim it earlier
// than it would have. Failing a launch over a housekeeping file would trade the
// thing the user asked for against tidiness.
func noteSandboxPackageUse(nvxHome, pkgName string) {
	if nvxHome == "" || pkgName == "" {
		return
	}
	dir := filepath.Join(nvxHome, "packages")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return
	}
	_ = os.WriteFile(packageUseFile(nvxHome, pkgName), nil, 0600)
}

// packagesHeldByLiveSessions returns the packages currently in use.
//
// The last-use file cannot answer this on its own: a long-running MCP server can
// outlive any retention window nvx picks, and its package must not be reclaimed
// while it runs. So each guest home records the package it launched under, and
// this reads the ones whose owning process is still alive -- reusing the same
// liveness rule that stops the guest-home sweep deleting a running install's
// HOME, rather than inventing a second answer to the same question.
func packagesHeldByLiveSessions(nvxHome string) map[string]bool {
	held := map[string]bool{}
	now := time.Now()
	for _, root := range []string{getSandboxHomeDir(nvxHome), getToolHomeDir(nvxHome)} {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			home := filepath.Join(root, e.Name())
			if !guestHomeIsInUse(home, now) {
				continue
			}
			if pkg, ok := readGuestHomePackage(home); ok {
				held[strings.ToLower(pkg)] = true
			}
		}
	}
	return held
}

// sweepOrphanedSandboxPackages deletes per-project AppContainer profiles that no
// live session holds and that nothing has used inside the retention window, and
// reports how many went. A budget of 0 means no limit.
//
// Enumerated from %LOCALAPPDATA%\Packages rather than the registry: Windows
// creates a folder there named after each profile, so a directory listing is the
// list of names, and DeleteAppContainerProfile takes exactly that name.
//
// Deleting one is safe: the next launch in that project registers it again, and
// CreateAppContainerProfile is idempotent. What would not be safe is deleting
// one out from under a running container, which is what the live-session set
// above exists to prevent.
func sweepOrphanedSandboxPackages(nvxHome string, budget int) int {
	local := packagesRoot()
	if local == "" || nvxHome == "" {
		return 0
	}
	entries, err := os.ReadDir(local)
	if err != nil {
		return 0
	}

	held := packagesHeldByLiveSessions(nvxHome)
	now := time.Now()
	removed := 0
	for _, e := range entries {
		if budget > 0 && removed >= budget {
			break
		}
		name := e.Name()
		if !e.IsDir() || !strings.HasPrefix(name, nvxPackagePrefix) {
			continue
		}
		if held[strings.ToLower(name)] {
			continue
		}
		if sandboxPackageLastUse(nvxHome, name, now) < sandboxPackageRetention {
			continue
		}
		deleteSandboxPackage(name)
		_ = os.Remove(packageUseFile(nvxHome, name))
		removed++
	}
	return removed
}

// sandboxPackageLastUse returns how long ago a package was used.
//
// Falls back to the age of the package directory itself when there is no
// last-use record, which covers profiles registered before nvx tracked this.
// That reads as "created then, never seen since", which is the right default: it
// makes an untracked profile eligible once it is older than the window rather
// than either immortal or immediately deleted.
func sandboxPackageLastUse(nvxHome, pkgName string, now time.Time) time.Duration {
	if info, err := os.Stat(packageUseFile(nvxHome, pkgName)); err == nil {
		return now.Sub(info.ModTime())
	}
	if info, err := os.Stat(filepath.Join(packagesRoot(), pkgName)); err == nil {
		return now.Sub(info.ModTime())
	}
	return 0
}

// packagesRoot is where Windows keeps a folder per registered AppContainer
// profile, and deleteSandboxPackage unregisters one. Both are variables so the
// sweep's rules can be tested against a temporary directory: the real ones
// enumerate and delete profiles belonging to this machine, and a test that
// exercised them for real would reclaim the developer's own projects.
var packagesRoot = func() string {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return ""
	}
	return filepath.Join(local, "Packages")
}

var deleteSandboxPackage = deleteAppContainerProfile
