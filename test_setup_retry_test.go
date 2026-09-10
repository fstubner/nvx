package main

import (
	"errors"
	"runtime"
	"syscall"
	"time"
)

// Setup that fails because the machine briefly could not hand out a handle.
//
// Windows does this. Measured on a hosted runner 2026-09-10, on a commit whose
// only change was elsewhere: `write ...\windows-setup.json: The handle is
// invalid` out of a test's own setup, failing the test at t.Fatal before a single
// assertion ran. The identical commit re-run was clean. It is the same host
// condition that already forced two other accommodations -- tempDir's
// removeAllBestEffort for cleanup, and stageProbeChild's five retries for reading
// the probe child -- and now a third face, a write.
//
// Deliberately NOT applied to all 135 t.Fatal(err) setup sites across the 52
// Windows test files. Machinery earns its place from a measured failure, not a
// hypothetical one, and a sweep of that size would be unreviewable while making
// every one of those sites quieter about real defects. Two sites use it: the one
// that was observed failing, and the other call of the same function.
//
// The judgement matches stageProbeChild's exactly. A transient failure is
// retried; a host that keeps refusing means the test could not run, which is a
// skip that says so; anything else is a defect and fails loudly.
func setupOrSkip(t launchT, what string, op func() error) {
	t.Helper()
	// The same backoff removeAllBestEffort uses, for the same reason: the handle
	// is released shortly after, so the only question is whether we wait long
	// enough to see it. ~2.5s total, and the first attempt almost always wins.
	delay := setupRetryInitialPause
	var err error
	for attempt := 0; attempt < 8; attempt++ {
		if err = op(); err == nil {
			return
		}
		if !transientHostSetupFailure(err) {
			// A real failure. Fatalf does not return on a *testing.T, but it does on
			// the fake the decision is tested with, so this returns explicitly rather
			// than falling through to the skip below.
			t.Fatalf("%s: %v", what, err)
			return
		}
		time.Sleep(delay)
		delay *= 2
	}
	t.Skipf("%s kept failing because this host would not hand out a handle (%v); "+
		"an assertion is NOT being checked in this run", what, err)
}

// setupRetryInitialPause is the first backoff step, a var so the test for the
// persistent case does not spend five seconds sleeping through the whole ladder.
var setupRetryInitialPause = 20 * time.Millisecond

// errorInvalidHandleOnWindows is ERROR_INVALID_HANDLE.
//
// Written out again rather than shared with the product's errorInvalidHandle,
// which lives in a windows-only file. This helper has to compile everywhere,
// because one of its two call sites is in a cross-platform test. Two constants
// naming the same well-known Win32 code is a smaller cost than a build-tagged
// pair of files to avoid it.
const errorInvalidHandleOnWindows = 6

// transientHostSetupFailure reports whether err is the host momentarily refusing
// a handle rather than anything being wrong.
//
// Matched on the errno, not the message, for the same reason processNeverStarted
// is: "The handle is invalid" is the English text and a localised Windows says
// something else, so a check on the words would read as robust while never
// firing.
//
// Guarded on GOOS because the code is a Win32 one. On Linux errno 6 is ENXIO,
// which has nothing to do with this, and treating it as transient would make a
// real failure retry eight times and then skip.
func transientHostSetupFailure(err error) bool {
	if err == nil || runtime.GOOS != "windows" {
		return false
	}
	var errno syscall.Errno
	return errors.As(err, &errno) && errno == errorInvalidHandleOnWindows
}
