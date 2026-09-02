//go:build windows

package main

// doctor reports on the permissions, not on what a previous run wrote down.
//
// The stranded-setup check compared the identity recorded by the last COMPLETED
// `nvx setup` against the one nvx launches under, and reported a failure whenever
// they differed. That record is written at the end of a setup run, so a run
// interrupted part-way through a slow volume leaves it naming the old identity
// while the volumes it already finished carry the new one.
//
// Measured 2026-09-02: two cancelled runs, C:\, C:\Users and D:\ correctly
// granted to the current identity, and doctor still reporting that an earlier
// setup "granted an identity nvx no longer uses" and that npx "fails there with
// EPERM". Neither half was true of that machine by then, and the advice cost a
// maintainer 46 minutes of elevated grant on a volume unrelated to the failure
// they were actually chasing.

import "testing"

const (
	oldSID = "S-1-15-2-999-old-package-identity"
	newSID = "S-1-15-3-1024-current-setup-capability"
)

func TestNothingIsReportedWhenThePermissionsAreActuallyThere(t *testing.T) {
	// The record names the old identity -- an interrupted run never rewrote it --
	// but every path the machine needs is granted to the current one.
	missing := strandedSetupGrantPaths(`C:\Users\someone\.nvx`, `C:\Users\someone`,
		oldSID, newSID, func(string) bool { return true })

	if len(missing) != 0 {
		t.Errorf("doctor would report %v as missing while the current identity holds every one of "+
			"them. That is a FAIL nobody can act on, over advice to run an elevated command that "+
			"would change nothing.", missing)
	}
}

func TestOnlyThePathsActuallyMissingAreReported(t *testing.T) {
	// A working directory on a volume that is NOT the system drive, so the path
	// list is guaranteed to contain a root the granted map below does not cover.
	// Deriving the "missing" case from the machine's own drives instead let this
	// skip itself on a one-volume machine -- and a check that stands down exactly
	// when it has something to check is the failure mode this suite keeps finding.
	granted := map[string]bool{`C:\`: true, `C:\Users`: true}
	missing := strandedSetupGrantPaths(`C:\Users\someone\.nvx`, `Z:\work\project`,
		oldSID, newSID, func(p string) bool { return granted[p] })

	if !containsPath(missing, `Z:\`) {
		t.Errorf("the volume the working directory is on is not granted to the current identity, "+
			"and a tool resolving up to it would fail there -- but it was not reported: %v", missing)
	}
	for _, p := range missing {
		if granted[p] {
			t.Errorf("%q is granted to the current identity but was reported as missing", p)
		}
	}
}

func TestAMatchingRecordIsNeverReported(t *testing.T) {
	missing := strandedSetupGrantPaths(`C:\Users\someone\.nvx`, `C:\Users\someone`,
		newSID, newSID, func(string) bool { return false })
	if len(missing) != 0 {
		t.Errorf("the recorded identity matches the current one, so nothing is stranded, "+
			"but %v was reported", missing)
	}
}
