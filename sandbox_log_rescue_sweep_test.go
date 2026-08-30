package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Rescued logs must be reclaimed, and only once nobody is going to read them.
//
// They are copied out of a guest home when a contained run FAILS, so the user
// can read the log the failure points at. Nothing swept them: guest homes have a
// sweep and package profiles have a sweep, and these accumulated for the life of
// the installation. An acceptance pass measured 3,146 directories and 181 MB on
// the development machine, with `nvx cleanup` leaving every one.
//
// Both directions are asserted together. "Deletes an old one" alone is satisfied
// by a sweep that deletes everything, which would take away the log of the
// failure the user is currently looking at.
func TestOldRescuedLogsAreReclaimedAndRecentOnesKept(t *testing.T) {
	nvxHome := tempDir(t)
	logs := filepath.Join(nvxHome, "logs")

	stale := filepath.Join(logs, "0000000000000001")
	fresh := filepath.Join(logs, "0000000000000002")
	for _, d := range []string{stale, fresh} {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "debug.log"), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-rescuedLogRetention - time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	if got := sweepRescuedLogs(nvxHome, 0); got != 1 {
		t.Fatalf("swept %d, want exactly 1", got)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("a log folder past the retention window was kept: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("a log folder from a recent failure was deleted; that is the one "+
			"someone is reading right now: %v", err)
	}
}

// The budget bounds one run's work, so a machine with thousands of them does not
// pay for the whole backlog in the command the user is waiting on.
func TestTheRescuedLogSweepRespectsItsBudget(t *testing.T) {
	nvxHome := tempDir(t)
	logs := filepath.Join(nvxHome, "logs")
	old := time.Now().Add(-rescuedLogRetention - time.Hour)

	for i := 0; i < 12; i++ {
		d := filepath.Join(logs, "session"+string(rune('a'+i)))
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(d, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if got := sweepRescuedLogs(nvxHome, 5); got != 5 {
		t.Fatalf("swept %d with a budget of 5", got)
	}
}
