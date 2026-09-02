//go:build windows

package main

import (
	"sync/atomic"
	"testing"
	"time"
)

// waitFor polls until cond holds, or fails the test saying what did not happen.
//
// Deadlines here are deliberately far longer than the work needs. What these
// tests assert is that a slot is eventually released and a path eventually
// retried -- outcomes, not latencies -- so a bound tight enough to expire under
// the load of the full probe suite converts a correctness check into a timing
// check and reports a scheduling delay as a broken counter. Measured: the whole
// file passes in isolation and one of these failed inside a full -race gate run.
func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal(what)
}

// stallHarness owns everything the blocking write touches.
//
// A struct rather than captured locals, because the two closures involved --
// the blocking write and the release -- were sharing one closure allocation,
// and the race detector reports a write to any of it against a read of any of
// it. Fields here are only ever mutated through their own synchronisation
// (a channel close or an atomic), so there is nothing left to race.
//
// Not a WaitGroup: Add runs inside the write, which production code calls, so it
// can start after Wait has begun -- the documented misuse, and what -race
// reported after the closure sharing above was fixed.
type stallHarness struct {
	unblock  chan struct{}
	inflight atomic.Int32
	attempts *atomic.Int32
	closed   atomic.Bool
}

func (h *stallHarness) write(path, sidStr string, mask uint32, flags uint8) error {
	h.inflight.Add(1)
	defer h.inflight.Add(-1)
	if h.attempts != nil {
		h.attempts.Add(1)
	}
	<-h.unblock // never returns within the deadline, like a write propagating over a huge tree
	return nil
}

func stalledACLWrites(t *testing.T, attempts *atomic.Int32) (release func()) {
	t.Helper()
	h := &stallHarness{unblock: make(chan struct{}), attempts: attempts}

	clearACLBounds()
	fn := h.write
	aclWriteFn.Store(&fn)

	release = func() {
		if h.closed.CompareAndSwap(false, true) {
			close(h.unblock)
		}
		waitFor(t, func() bool { return h.inflight.Load() == 0 }, "blocked writes did not return")
		// That only covers the write itself. grantACLWithin's goroutine
		// carries on afterwards to claim the outcome and, if it was abandoned, to
		// give its slot back -- so waiting for the write alone leaves that tail
		// running, and whatever the test does next races it.
		waitForACLDrain(t)
	}
	t.Cleanup(func() {
		release()
		aclWriteFn.Store(nil)
		clearACLBounds()
	})
	return release
}

// clearACLBounds empties the shared state without REPLACING the sync.Map.
//
// Assigning a fresh sync.Map over the variable is itself a write to a variable
// that abandoned goroutines are still reading, which is a data race however
// carefully the values are managed. Range/Delete uses the map's own
// synchronisation instead.
func clearACLBounds() {
	aclStalledPaths.Range(func(k, _ any) bool {
		aclStalledPaths.Delete(k)
		return true
	})
	aclAbandoned.Store(0)
}

// waitForACLDrain blocks until no abandoned write is outstanding, which is the
// last thing grantACLWithin's goroutine touches.
func waitForACLDrain(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if aclAbandoned.Load() == 0 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("abandoned ACL writes did not drain: %d still outstanding", aclAbandoned.Load())
}

// A path whose permission write stalls must not be attempted again in this
// process.
//
// A goroutine blocked in a syscall pins an OS thread, and an abandoned write only
// ends when Windows finishes propagating the ACL over everything below the
// directory -- measured at 3m45s for AppData\Local\Temp and its 748,317 entries,
// against the walk's 1500ms budget. The ancestor walk meets the same few
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
		if err := grantACLWithin(path, "S-1-15-3-1024-x", aclMaskTraverse, 0, 500*time.Millisecond, nil); err == nil {
			t.Fatalf("attempt %d reported success while the write was still blocked", i)
		}
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("started %d writes for one stalling path; each one pins an OS thread "+
			"for as long as the propagation runs, so this grows with launches", got)
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
		_ = grantACLWithin(unique, "S-1-15-3-1024-x", aclMaskTraverse, 0, 500*time.Millisecond, nil)
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
	if err := grantACLWithin(path, "S-1-15-3-1024-x", aclMaskTraverse, 0, 500*time.Millisecond, nil); err == nil {
		t.Fatal("expected the deadline to fire")
	}
	if aclAbandoned.Load() != 1 {
		t.Fatalf("outstanding = %d, want 1 after one abandoned write", aclAbandoned.Load())
	}

	release() // the write finishes at last, and this waits for it to actually return
	deadline := time.Now().Add(60 * time.Second)
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
