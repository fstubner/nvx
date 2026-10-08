//go:build windows

package nvx

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// isolatePackageSweep points the package sweep at an empty folder, so a test of
// the sweep as a whole does not list or delete the profiles of this machine.
func isolatePackageSweep(t *testing.T) {
	t.Helper()
	root := tempDir(t)
	prevRoot, prevDelete := packagesRoot, deleteSandboxPackage
	packagesRoot = func() string { return root }
	deleteSandboxPackage = func(string) {}
	t.Cleanup(func() { packagesRoot, deleteSandboxPackage = prevRoot, prevDelete })
}

// Listing the package folder is what the sweep costs. Over this machine's 638
// profiles it took 83 to 91 ms a call, and a profile is only reclaimed a week
// after its last use. It is listed once an hour.
func TestThePackageFolderIsListedOnceAnHour(t *testing.T) {
	root := tempDir(t)
	nvxHome := tempDir(t)
	prevRoot, prevDelete := packagesRoot, deleteSandboxPackage
	listed := 0
	packagesRoot = func() string { listed++; return root }
	deleteSandboxPackage = func(string) {}
	t.Cleanup(func() { packagesRoot, deleteSandboxPackage = prevRoot, prevDelete })

	reclaimStaleSandboxes(nvxHome)
	if listed != 1 {
		t.Fatalf("the first sweep listed the package folder %d times, want 1", listed)
	}
	reclaimStaleSandboxes(nvxHome)
	reclaimStaleSandboxes(nvxHome)
	if listed != 1 {
		t.Fatalf("the package folder was listed %d times in three sweeps within the hour, want 1", listed)
	}

	old := time.Now().Add(-retentionSweepInterval - time.Minute)
	if err := os.Chtimes(retentionStampPath(nvxHome), old, old); err != nil {
		t.Fatal(err)
	}
	reclaimStaleSandboxes(nvxHome)
	if listed != 2 {
		t.Fatalf("the package folder was listed %d times after the hour, want 2", listed)
	}
}

// A package sweep that reached its budget has more to delete, so the next
// command lists again.
func TestAPackageBacklogIsNotWaitedOutForAnHour(t *testing.T) {
	root := tempDir(t)
	nvxHome := tempDir(t)
	prevRoot, prevDelete := packagesRoot, deleteSandboxPackage
	packagesRoot = func() string { return root }
	deleteSandboxPackage = func(name string) { _ = os.RemoveAll(filepath.Join(root, name)) }
	t.Cleanup(func() { packagesRoot, deleteSandboxPackage = prevRoot, prevDelete })

	old := time.Now().Add(-30 * 24 * time.Hour)
	const total = reclaimBudgetPerRun + 3
	for i := 0; i < total; i++ {
		stagePackage(t, root, nvxHome, nvxPackagePrefix+string(rune('a'+i))+"aaaaaaaaaaaaaaa", old)
	}

	reclaimStaleSandboxes(nvxHome)
	left, _ := os.ReadDir(root)
	if len(left) != total-reclaimBudgetPerRun {
		t.Fatalf("%d profiles left after one sweep, want %d", len(left), total-reclaimBudgetPerRun)
	}
	reclaimStaleSandboxes(nvxHome)
	left, _ = os.ReadDir(root)
	if len(left) != 0 {
		t.Fatalf("%d profiles left after the second sweep, want none: a backlog waited out the hour", len(left))
	}
}
