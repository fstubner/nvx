//go:build windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestFailedAncestorGrantIsNotRetried is F76. An ancestor grant that cannot
// succeed was retried on every launch, and each attempt burned the full per-path
// timeout: measured at 3054ms warm, against tens of milliseconds for every other
// phase of a contained launch.
func TestFailedAncestorGrantIsNotRetried(t *testing.T) {
	nvxHome := tempDir(t)
	paths := []string{`C:\Users\x\AppData\Local\Temp`, `C:\Users\x\AppData\Local`}

	attempts := 0
	failAll := func(string) error {
		attempts++
		return errors.New("icacls timed out")
	}

	grantAncestorsSkippingKnownFailures(nvxHome, paths, failAll)
	if attempts != len(paths) {
		t.Fatalf("first pass attempted %d of %d paths", attempts, len(paths))
	}

	attempts = 0
	grantAncestorsSkippingKnownFailures(nvxHome, paths, failAll)
	if attempts != 0 {
		t.Errorf("a known-failing grant was retried %d time(s); this is the 3s per launch that "+
			"F76 measured, and the grant cannot succeed however often it is tried", attempts)
	}
}

// TestSucceedingAncestorGrantIsNotSkipped guards the obvious way to get this
// wrong: a cache that suppresses grants which would have worked would quietly
// remove the traverse rights the walk exists to add.
func TestSucceedingAncestorGrantIsNotSkipped(t *testing.T) {
	nvxHome := tempDir(t)
	paths := []string{`C:\Users\x\projects\app`}

	attempts := 0
	succeed := func(string) error { attempts++; return nil }

	grantAncestorsSkippingKnownFailures(nvxHome, paths, succeed)
	grantAncestorsSkippingKnownFailures(nvxHome, paths, succeed)
	if attempts != 2 {
		t.Errorf("a succeeding grant was attempted %d times across two passes, want 2; "+
			"only failures may be remembered", attempts)
	}
}

// TestAncestorSkipClearsWhenTheGrantStartsWorking covers recovery. The cause is
// environmental -- a filter driver, an antivirus policy -- so a machine that
// starts working must stop being penalised without anyone knowing a cache exists.
func TestAncestorSkipClearsWhenTheGrantStartsWorking(t *testing.T) {
	nvxHome := tempDir(t)
	paths := []string{`C:\Users\x\AppData\Local`}

	grantAncestorsSkippingKnownFailures(nvxHome, paths, func(string) error {
		return errors.New("icacls timed out")
	})
	if len(loadAncestorSkips(nvxHome)) != 1 {
		t.Fatal("the failure was not recorded")
	}

	// Expire the entry the way the TTL would, then let it succeed.
	expired := time.Now().Add(-ancestorSkipTTL - time.Hour)
	saveAncestorSkips(nvxHome, map[string]time.Time{normalizeAncestorKey(paths[0]): expired})

	attempts := 0
	grantAncestorsSkippingKnownFailures(nvxHome, paths, func(string) error { attempts++; return nil })
	if attempts != 1 {
		t.Fatalf("an expired skip did not lead to a retry (attempts=%d)", attempts)
	}
	if len(loadAncestorSkips(nvxHome)) != 0 {
		t.Error("a grant that succeeded left its old failure recorded; the machine would keep " +
			"being penalised after the cause was fixed")
	}
}

// TestAncestorSkipSurvivesACorruptCacheFile keeps a damaged cache from disabling
// the grants silently. Unreadable means "nothing is skipped", which costs a retry
// rather than removing traverse rights nobody notices are gone.
func TestAncestorSkipSurvivesACorruptCacheFile(t *testing.T) {
	nvxHome := tempDir(t)
	if err := os.WriteFile(filepath.Join(nvxHome, "ancestor-grant-skip.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	attempts := 0
	grantAncestorsSkippingKnownFailures(nvxHome, []string{`C:\Users\x\p`}, func(string) error {
		attempts++
		return nil
	})
	if attempts != 1 {
		t.Errorf("a corrupt cache suppressed the grant (attempts=%d); it must fail towards "+
			"attempting, not towards skipping", attempts)
	}
}

// TestAncestorSkipIsDisabledWithoutAnNvxHome pins the escape hatch the probes
// rely on: passing "" means no persistence, so a test cannot write a cache file
// into the real ~/.nvx as a side effect.
func TestAncestorSkipIsDisabledWithoutAnNvxHome(t *testing.T) {
	attempts := 0
	fail := func(string) error { attempts++; return errors.New("nope") }

	grantAncestorsSkippingKnownFailures("", []string{`C:\Users\x\p`}, fail)
	grantAncestorsSkippingKnownFailures("", []string{`C:\Users\x\p`}, fail)
	if attempts != 2 {
		t.Errorf("with no nvxHome the grant was attempted %d times, want 2 (no persistence)", attempts)
	}
}

// Expiring several remembered failures must not make one command pay for all of
// them.
//
// Dropping expired entries on load made every one eligible in the same run, so a
// machine with three failing ancestors spent the whole three-second grant budget
// on whichever command happened to be first after the TTL lapsed -- a command
// that is otherwise ~660ms. Recovery is still the point; paying for it all at
// once was not.
func TestExpiredAncestorSkipsAreRetriedOneRunAtATime(t *testing.T) {
	nvxHome := tempDir(t)
	paths := []string{`C:\a`, `C:\b`, `C:\c`}

	expired := time.Now().Add(-ancestorSkipTTL - time.Hour)
	stale := map[string]time.Time{}
	for _, p := range paths {
		stale[normalizeAncestorKey(p)] = expired
	}
	saveAncestorSkips(nvxHome, stale)

	// Every retry fails again, which is the case worth bounding: a genuinely
	// broken environment must not get slower the longer it stays broken.
	var attemptedPaths []string
	grant := func(p string) error {
		attemptedPaths = append(attemptedPaths, p)
		return errAncestorGrantForTest
	}

	attempted := grantAncestorsSkippingKnownFailures(nvxHome, paths, grant)
	if attempted != 1 {
		t.Fatalf("attempted %d expired paths in one run (%v); one at a time is the point",
			attempted, attemptedPaths)
	}

	// The one that was re-tested failed again, so it is remembered afresh and
	// the NEXT run moves on to a different path rather than repeating this one.
	second := grantAncestorsSkippingKnownFailures(nvxHome, paths, grant)
	if second != 1 {
		t.Fatalf("second run attempted %d paths, want 1", second)
	}
	if len(attemptedPaths) != 2 || attemptedPaths[0] == attemptedPaths[1] {
		t.Errorf("the same path was re-tested twice instead of progressing: %v", attemptedPaths)
	}
}

// A live (unexpired) record must still suppress the grant entirely.
func TestUnexpiredAncestorSkipsAreNotRetriedAtAll(t *testing.T) {
	nvxHome := tempDir(t)
	paths := []string{`C:\a`, `C:\b`}
	fresh := map[string]time.Time{}
	for _, p := range paths {
		fresh[normalizeAncestorKey(p)] = time.Now()
	}
	saveAncestorSkips(nvxHome, fresh)

	attempted := grantAncestorsSkippingKnownFailures(nvxHome, paths, func(string) error {
		t.Error("a recently failed grant was retried")
		return errAncestorGrantForTest
	})
	if attempted != 0 {
		t.Errorf("attempted %d, want 0", attempted)
	}
}

var errAncestorGrantForTest = errors.New("grant refused")
