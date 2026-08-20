package main

import (
	"os"
	"testing"
	"time"
)

// tempDir replaces t.TempDir() throughout this suite, because t.TempDir() fails
// the test when its cleanup fails -- and on GitHub-hosted Windows runners that
// cleanup intermittently fails for reasons that have nothing to do with nvx.
//
// Five times in two days, on five unrelated tests, CI went red with:
//
//	TempDir RemoveAll cleanup: unlinkat C:\Users\RUNNER~1\...: The handle is invalid.
//
// Every one cleared on a plain rerun with no code change. The assertions had
// already passed; the test was failed by the teardown of a directory nobody was
// going to look at again. That is a runner defect reported as a product failure,
// and after five rerun cycles it starts training people to ignore red -- the same
// argument that moved the AppContainer probes' staging step to a skip.
//
// Removal is retried briefly first, because the cause looks like a handle held
// open a moment longer than expected (Defender scanning a freshly written file is
// the usual suspect). Retrying cleans up in almost every case, so this is not a
// licence to leak: a directory that still will not go is left in the OS temp
// folder, which the OS clears anyway, and is a far smaller problem than a false
// red build.
//
// Nothing about the test's own behaviour changes -- creation still fails loudly,
// because a temp dir that cannot be created means the test genuinely cannot run.
func tempDir(t *testing.T) string {
	t.Helper()
	// A short prefix rather than t.TempDir()'s test-name-derived path: several
	// probes here bind AF_UNIX sockets inside these directories, and sun_path is
	// capped at 108 bytes. Long test names have overflowed it before and the
	// failure looks like a permission denial rather than a path-length one.
	dir, err := os.MkdirTemp("", "nvx")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { removeAllBestEffort(dir) })
	return dir
}

// removeAllBestEffort deletes dir, backing off between attempts, and gives up
// silently.
//
// The backoff is not decoration. A flat 5x20ms window left 10 directories behind
// across three suite runs, and every one of them deleted without complaint a few
// seconds later -- so the handle really is released shortly after, and the only
// question is whether cleanup waits long enough to see it. Doubling up to ~2.5s
// total costs nothing in the common case, where the first attempt succeeds.
func removeAllBestEffort(dir string) {
	delay := 20 * time.Millisecond
	for attempt := 0; attempt < 8; attempt++ {
		if err := os.RemoveAll(dir); err == nil {
			return
		}
		time.Sleep(delay)
		delay *= 2
	}
	// Still held. The OS clears its own temp directory, and a stray directory
	// there is a far smaller problem than failing a test whose assertions passed.
}
