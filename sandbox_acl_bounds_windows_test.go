//go:build windows

package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// stalledACLWrites installs a write that blocks until the returned release is
// called, and clears the per-process state grantACLWithin accumulates.
//
// The draining matters, and getting it wrong is what this comment is for: an
// earlier version released the blocked writes from t.Cleanup and reset the
// counter immediately. Those goroutines then finished AFTER the reset and
// decremented from zero, so the next test read `outstanding = -3` and failed --
// a defect in the tests that looked exactly like a defect in the counter. The
// release must therefore complete before the state is reset, which means waiting
// for the goroutines rather than assuming they are gone.
func stalledACLWrites(t *testing.T, attempts *atomic.Int32) (release func()) {
	t.Helper()
	prev := aclWrite
	unblock := make(chan struct{})
	var running sync.WaitGroup

	aclStalledPaths = sync.Map{}
	aclAbandoned.Store(0)
	aclWrite = func(path, sidStr string, mask uint32, flags uint8) error {
		running.Add(1)
		defer running.Done()
		if attempts != nil {
			attempts.Add(1)
		}
		<-unblock // never returns within the deadline, like a driver-blocked write
		return nil
	}

	released := false
	release = func() {
		if released {
			return
		}
		released = true
		close(unblock)
		running.Wait()
	}
	t.Cleanup(func() {
		release()
		aclWrite = prev
		aclStalledPaths = sync.Map{}
		aclAbandoned.Store(0)
	})
	return release
}

// A path whose permission write stalls must not be attempted again in this
// process.
//
// A goroutine blocked in a syscall pins an OS thread, and an abandoned write can
// only end when the filter driver lets go. The ancestor walk meets the same few
// directories on every launch -- the user profile root above all -- so retrying
// one that has already stalled grows the pinned-thread count with the number of
// LAUNCHES rather than the number of troublesome paths.
//
// That is not theoretical. An acceptance pass dumped a release-gate run that died
// with "runtime: SetWaitableTimer failed; errno= 5": 49 of 83 goroutines were
// blocked in this write, all created here, 34 of them for over a minute. The
// original reasoning -- "the ancestor walk's own budget bounds how many can be
// outstanding" -- holds for one walk and fails for a process that does hundreds.
func TestAStalledPathIsNotRetriedInThisProcess(t *testing.T) {
	var attempts atomic.Int32
	stalledACLWrites(t, &attempts)

	const path = `C:\Users\someone`
	for i := 0; i < 5; i++ {
		if err := grantACLWithin(path, "S-1-15-3-1024-x", aclMaskTraverse, 0, 20*time.Millisecond); err == nil {
			t.Fatalf("attempt %d reported success while the write was still blocked", i)
		}
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("started %d writes for one stalling path; each one pins an OS thread "+
			"for as long as the driver holds it, so this grows with launches", got)
	}
}

// ...and a flood of DIFFERENT stalling paths must stop rather than pin a thread
// each.
//
// The per-path rule above does not cover this: every path is new, so every one is
// eligible. Without a ceiling the only bound is how many distinct directories the
// walk can reach.
func TestOutstandingStalledWritesAreCapped(t *testing.T) {
	var attempts atomic.Int32
	stalledACLWrites(t, &attempts)

	for i := 0; i < maxAbandonedACLWrites*3; i++ {
		unique := `C:\Users\someone\` + string(rune('a'+i%26)) + string(rune('a'+i/26))
		_ = grantACLWithin(unique, "S-1-15-3-1024-x", aclMaskTraverse, 0, 20*time.Millisecond)
	}
	if got := attempts.Load(); got > maxAbandonedACLWrites {
		t.Fatalf("started %d blocked writes with a ceiling of %d", got, maxAbandonedACLWrites)
	}
}

// A write that comes back late must give its slot up again, or the ceiling is a
// one-way ratchet that wedges every later grant in the process.
func TestASlowWriteThatFinishesReleasesItsSlot(t *testing.T) {
	release := stalledACLWrites(t, nil)

	const path = `C:\Users\someone\slow`
	if err := grantACLWithin(path, "S-1-15-3-1024-x", aclMaskTraverse, 0, 20*time.Millisecond); err == nil {
		t.Fatal("expected the deadline to fire")
	}
	if aclAbandoned.Load() != 1 {
		t.Fatalf("outstanding = %d, want 1 after one abandoned write", aclAbandoned.Load())
	}

	release() // the driver lets go, and this waits for the write to actually finish
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if aclAbandoned.Load() == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if aclAbandoned.Load() != 0 {
		t.Fatal("a completed write never released its slot; the ceiling would wedge shut")
	}
	if _, stalled := aclStalledPaths.Load(`c:\users\someone\slow`); stalled {
		t.Fatal("the path is still marked as stalling after its write completed")
	}
}
