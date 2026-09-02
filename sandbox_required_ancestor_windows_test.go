//go:build windows

package main

// The chain above the guest home is not optional, and treating it as though it
// were broke contained npx for eleven days without saying so.
//
// nvx grants traverse+stat on the directories above a sandbox's working directory
// and above its guest home. Those grants are best-effort and time-boxed, and a
// path whose grant has overrun is remembered and not retried for thirty days --
// which is right for the chain above a PROJECT, where the measurement behind that
// cache was taken: a contained command still launches, stats and writes its
// working directory without them.
//
// It is wrong for the chain above the guest home. npm walks up from there and
// stats every directory on the way, so with ~/.nvx/sandbox_home in the skip cache
// every `nvx npx` ended in:
//
//	EPERM lstat C:\Users\Felix\.nvx\sandbox_home
//
// Measured 2026-09-01. The entry had been written on 2026-08-29 by a single
// overrun, and the only trace at runtime was "Skipped 2 of 2 ancestor permission
// checks to keep startup fast" -- which reads like an optimisation, not like the
// reason the command about to run cannot work. Deleting that one entry made
// `nvx npx cowsay hi` succeed, unelevated, on the same machine.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestGuestHomeAncestorsAreGrantedEvenWhenPreviouslyRecordedAsFailing is the
// regression. It writes the skip record that disabled npx, then checks the path
// is attempted anyway.
func TestGuestHomeAncestorsAreGrantedEvenWhenPreviouslyRecordedAsFailing(t *testing.T) {
	nvxHome := tempDir(t)
	guestHome := filepath.Join(nvxHome, "sandbox_home", "aaaaaaaaaaaaaaaa")
	if err := os.MkdirAll(guestHome, 0o700); err != nil {
		t.Fatal(err)
	}
	sandboxHome := filepath.Join(nvxHome, "sandbox_home")

	// Exactly the state found on the machine: the guest home's own parent recorded
	// as a failed grant, well inside the thirty-day window.
	saveAncestorSkips(nvxHome, map[string]time.Time{
		normalizeAncestorKey(sandboxHome): time.Now(),
	})

	var attempted []string
	failed := grantRequiredAncestors(
		guestHomeRequiredGrants(guestHome),
		func(p string) error { attempted = append(attempted, p); return nil },
	)

	if len(failed) != 0 {
		t.Errorf("nothing failed, but %v was reported as failing", failed)
	}
	if !containsPath(attempted, sandboxHome) {
		t.Errorf("%s was recorded as a past failure and so was never attempted again. That is the "+
			"state that made every `nvx npx` fail with EPERM on this exact path for the thirty-day "+
			"life of the record. Attempted: %v", sandboxHome, attempted)
	}
}

// A required grant that fails is reported, not remembered. Remembering it is what
// turned one overrun into eleven days of broken npx.
func TestAFailedRequiredAncestorIsReportedAndNotRecorded(t *testing.T) {
	nvxHome := tempDir(t)
	guestHome := filepath.Join(nvxHome, "sandbox_home", "bbbbbbbbbbbbbbbb")
	if err := os.MkdirAll(guestHome, 0o700); err != nil {
		t.Fatal(err)
	}
	sandboxHome := filepath.Join(nvxHome, "sandbox_home")

	failed := grantRequiredAncestors(
		guestHomeRequiredGrants(guestHome),
		func(p string) error {
			if strings.EqualFold(p, sandboxHome) {
				return errors.New("did not complete in time")
			}
			return nil
		},
	)

	if !containsPath(failed, sandboxHome) {
		t.Errorf("a required grant failed and was not reported: %v", failed)
	}
	// Nothing may be written to the skip cache, or the next launch inherits the
	// same silent breakage this test exists to prevent.
	if got := loadAncestorSkips(nvxHome); len(got) != 0 {
		t.Errorf("a failed REQUIRED grant was recorded as skippable (%v); the next launch would "+
			"not retry it, which is exactly how contained npx stayed broken for eleven days", got)
	}
}

func containsPath(paths []string, want string) bool {
	for _, p := range paths {
		if strings.EqualFold(filepath.Clean(p), filepath.Clean(want)) {
			return true
		}
	}
	return false
}
