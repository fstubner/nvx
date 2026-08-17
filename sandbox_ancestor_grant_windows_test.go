//go:build windows

package main

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// TestAncestorGrantPathsStopsAtProfileRoot pins which directories are eligible at
// all: the walk must climb from the working directory up to, but never including,
// the profile root.
func TestAncestorGrantPathsStopsAtProfileRoot(t *testing.T) {
	profile := `C:\Users\felix`
	work := `C:\Users\felix\AppData\Local\Temp\proj\sub`

	got := ancestorGrantPaths(work, profile)
	want := []string{
		`C:\Users\felix\AppData\Local\Temp\proj`,
		`C:\Users\felix\AppData\Local\Temp`,
		`C:\Users\felix\AppData\Local`,
		`C:\Users\felix\AppData`,
	}
	if len(got) != len(want) {
		t.Fatalf("ancestorGrantPaths = %v, want %v", got, want)
	}
	for i := range want {
		if !dirsEqual(got[i], want[i]) {
			t.Errorf("index %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestAncestorGrantPathsExcludesPathsOutsideProfile(t *testing.T) {
	if got := ancestorGrantPaths(`D:\work\proj`, `C:\Users\felix`); len(got) != 0 {
		t.Errorf("a workdir outside the profile should yield no ancestor grants, got %v", got)
	}
	if got := ancestorGrantPaths(``, `C:\Users\felix`); len(got) != 0 {
		t.Errorf("empty workdir should yield nothing, got %v", got)
	}
	// The profile root itself must never be granted -- its ACL write is the known
	// hang, and it already grants ALL APPLICATION PACKAGES.
	for _, p := range ancestorGrantPaths(filepath.Join(`C:\Users\felix`, "proj"), `C:\Users\felix`) {
		if dirsEqual(p, `C:\Users\felix`) {
			t.Errorf("profile root must not be in the grant list: %v", p)
		}
	}
}

// TestGrantAncestorsWithinBudgetAbandonsAfterBudget is the fix for F1. One
// pathological directory used to consume 45s -- the entire measured setup stall --
// because the walk worked through every ancestor with a full per-call timeout each.
// The grants are advisory (results were already discarded, and commands run fine
// when they fail), so the loop must abandon the remainder once the budget is spent
// rather than making the user wait.
func TestGrantAncestorsWithinBudgetAbandonsAfterBudget(t *testing.T) {
	paths := []string{"a", "b", "c", "d", "e"}
	var seen []string

	start := time.Now()
	attempted := grantAncestorsWithinBudget(paths, 120*time.Millisecond, func(p string) error {
		seen = append(seen, p)
		time.Sleep(100 * time.Millisecond) // stand in for a hanging icacls
		return errors.New("timed out")
	})
	elapsed := time.Since(start)

	if attempted >= len(paths) {
		t.Errorf("attempted %d of %d paths; the budget must stop the walk early", attempted, len(paths))
	}
	if elapsed > time.Second {
		t.Errorf("walk took %v; the budget should have bounded it", elapsed)
	}
	if len(seen) != attempted {
		t.Errorf("attempted=%d but grant was called %d times", attempted, len(seen))
	}
}

// TestGrantAncestorsWithinBudgetCompletesWhenCheap guards the other side: when
// grants are fast -- which is the normal case, and where they are actually useful --
// every one must still be applied.
func TestGrantAncestorsWithinBudgetCompletesWhenCheap(t *testing.T) {
	paths := []string{"a", "b", "c", "d", "e"}
	var seen []string

	attempted := grantAncestorsWithinBudget(paths, 2*time.Second, func(p string) error {
		seen = append(seen, p)
		return nil
	})
	if attempted != len(paths) {
		t.Errorf("attempted %d of %d fast grants, want all", attempted, len(paths))
	}
	if len(seen) != len(paths) {
		t.Errorf("grant called %d times, want %d", len(seen), len(paths))
	}
}
