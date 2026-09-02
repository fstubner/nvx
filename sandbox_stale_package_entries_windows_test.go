//go:build windows

package main

// The first write a tree gets under the runtime identity takes the per-package
// entries with it. Those entries are what a month of per-project packages left
// on nvx's shared directories -- 388 on one node install -- and removing them
// one at a time would cost one full propagation each.

import (
	"strings"
	"testing"
)

func packageEntriesOn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := readDACL(dir)
	if err != nil {
		t.Fatalf("readDACL: %v", err)
	}
	var out []string
	for _, e := range entries {
		if !e.Inherited && isPackageSID(e.SID) {
			out = append(out, e.SID)
		}
	}
	return out
}

func TestTheFirstRuntimeGrantRemovesStalePackageEntries(t *testing.T) {
	dir := t.TempDir()
	// Two package identities of the shape every per-project AppContainer carries.
	stale := []string{
		"S-1-15-2-1000000001-2-3-4-5-6-7",
		"S-1-15-2-1000000002-2-3-4-5-6-7",
	}
	for _, sid := range stale {
		if err := grantACL(dir, sid, aclMaskReadExec, nvxInheritFlags); err != nil {
			t.Skipf("cannot write an ACL in the test environment: %v", err)
		}
	}
	// And one entry that is not a package and must survive: a capability.
	keep, err := scopeCapabilitySID(dir)
	if err != nil {
		t.Skipf("cannot derive a capability SID here: %v", err)
	}
	if err := grantACL(dir, keep, aclMaskReadExec, nvxInheritFlags); err != nil {
		t.Fatalf("grant the capability: %v", err)
	}
	if got := packageEntriesOn(t, dir); len(got) != 2 {
		t.Fatalf("setup: %d package entries, want 2", len(got))
	}

	if err := grantRuntimeReadExecTree(dir); err != nil {
		t.Fatalf("grantRuntimeReadExecTree: %v", err)
	}
	if got := packageEntriesOn(t, dir); len(got) != 0 {
		t.Errorf("package entries still present after the runtime grant: %s", strings.Join(got, ", "))
	}
	runtimeSID, _ := runtimeCapabilitySID()
	for _, want := range []string{keep, runtimeSID} {
		if !appContainerHasGrantFor(want, dir, grantReadExec) {
			t.Errorf("%s lost its read/execute entry; only package entries may be dropped", want)
		}
	}
}

// On a tree that already carries the runtime identity nothing is written, so
// nothing is dropped either -- the debris on an already-migrated directory is
// left for the next real write, never paid for on its own.
func TestNoWriteMeansNoDrop(t *testing.T) {
	dir := t.TempDir()
	runtimeSID, err := runtimeCapabilitySID()
	if err != nil {
		t.Skipf("cannot derive a capability SID here: %v", err)
	}
	if err := grantACL(dir, runtimeSID, aclMaskReadExec, nvxInheritFlags); err != nil {
		t.Skipf("cannot write an ACL in the test environment: %v", err)
	}
	if err := grantACL(dir, "S-1-15-2-1000000003-2-3-4-5-6-7", aclMaskReadExec, nvxInheritFlags); err != nil {
		t.Fatal(err)
	}
	if err := grantRuntimeReadExecTree(dir); err != nil {
		t.Fatal(err)
	}
	if got := packageEntriesOn(t, dir); len(got) != 1 {
		t.Fatalf("a directory that needed no write lost %d package entries; the drop rides on a write "+
			"that was happening anyway, and there was none", 1-len(got))
	}
}
