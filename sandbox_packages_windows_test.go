//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// stagePackage creates a fake registered profile and records when it was last
// used, backdating both so the retention rule can be exercised without waiting a
// week.
func stagePackage(t *testing.T, root, nvxHome, name string, lastUsed time.Time) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(dir, lastUsed, lastUsed); err != nil {
		t.Fatal(err)
	}
	noteSandboxPackageUse(nvxHome, name)
	if err := os.Chtimes(packageUseFile(nvxHome, name), lastUsed, lastUsed); err != nil {
		t.Fatal(err)
	}
}

// The package sweep must reclaim what nobody is using, keep what anybody is, and
// not churn through what is used regularly.
//
// Per-project packages mean one registered AppContainer profile for every
// project nvx has ever contained, and nothing but this removes them. The first
// version ran only from `nvx cleanup`, and only when no sandbox session at all
// was running -- on a machine with two long-lived MCP servers that is never, so
// it reclaimed nothing on the machine it was written on.
//
// All three rules are asserted together on purpose. "Deletes an orphan" alone is
// satisfied by a sweep that deletes everything, which would take a running
// container's profile with it; "keeps a live one" alone is satisfied by a sweep
// that deletes nothing at all, which is the bug being fixed.
func TestThePackageSweepReclaimsOnlyOrphans(t *testing.T) {
	root := tempDir(t)
	nvxHome := tempDir(t)

	prevRoot, prevDelete := packagesRoot, deleteSandboxPackage
	packagesRoot = func() string { return root }
	var deleted []string
	deleteSandboxPackage = func(name string) { deleted = append(deleted, name) }
	t.Cleanup(func() { packagesRoot, deleteSandboxPackage = prevRoot, prevDelete })

	old := time.Now().Add(-30 * 24 * time.Hour)
	recent := time.Now().Add(-1 * time.Hour)

	orphan := nvxPackagePrefix + "aaaaaaaaaaaaaaaa"
	inUse := nvxPackagePrefix + "bbbbbbbbbbbbbbbb"
	fresh := nvxPackagePrefix + "cccccccccccccccc"
	stagePackage(t, root, nvxHome, orphan, old)
	stagePackage(t, root, nvxHome, inUse, old) // old AND held: liveness must win
	stagePackage(t, root, nvxHome, fresh, recent)

	// Something entirely unrelated, and the shared profile older nvx launched
	// under. Neither is nvx's per-project namespace and neither may be touched.
	stagePackage(t, root, nvxHome, "Microsoft.WindowsCalculator_8wekyb3d8bbwe", old)
	if err := os.MkdirAll(filepath.Join(root, stableSandboxProfile), 0700); err != nil {
		t.Fatal(err)
	}

	// A live session holding `inUse`: a guest home whose owner is this very
	// process, which guestHomeIsInUse treats as running.
	live := filepath.Join(getSandboxHomeDir(nvxHome), "livesession")
	if err := os.MkdirAll(live, 0700); err != nil {
		t.Fatal(err)
	}
	writeSessionOwner(live, time.Now())
	writeGuestHomePackage(live, inUse)

	if got := sweepOrphanedSandboxPackages(nvxHome, 0); got != 1 {
		t.Fatalf("swept %d packages, want exactly 1: %v", got, deleted)
	}
	if len(deleted) != 1 || deleted[0] != orphan {
		t.Fatalf("deleted %v, want only the unused, out-of-retention %s", deleted, orphan)
	}
	// The record must go with the profile, or the next sweep reads a last-use
	// time for something that no longer exists.
	if _, err := os.Stat(packageUseFile(nvxHome, orphan)); !os.IsNotExist(err) {
		t.Fatalf("the use record outlived the package it recorded: %v", err)
	}
	if _, err := os.Stat(packageUseFile(nvxHome, inUse)); err != nil {
		t.Fatalf("a live package's use record was removed: %v", err)
	}
}

// A package used recently must survive, or every run in a project would
// re-register the profile it is about to use again.
func TestThePackageSweepDoesNotChurnThroughActiveProjects(t *testing.T) {
	root := tempDir(t)
	nvxHome := tempDir(t)

	prevRoot, prevDelete := packagesRoot, deleteSandboxPackage
	packagesRoot = func() string { return root }
	var deleted []string
	deleteSandboxPackage = func(name string) { deleted = append(deleted, name) }
	t.Cleanup(func() { packagesRoot, deleteSandboxPackage = prevRoot, prevDelete })

	// Used a moment ago and nothing running: the state every project is in
	// immediately after a contained command finishes.
	stagePackage(t, root, nvxHome, nvxPackagePrefix+"dddddddddddddddd", time.Now())

	if got := sweepOrphanedSandboxPackages(nvxHome, 0); got != 0 {
		t.Fatalf("swept %d packages: %v -- a project used seconds ago would pay to "+
			"re-register its profile on the very next run", got, deleted)
	}
}
