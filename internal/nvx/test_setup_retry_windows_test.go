//go:build windows

package nvx

import (
	"fmt"
	"os"
	"syscall"
	"testing"
)

// Windows-only because the condition is. transientHostSetupFailure returns false
// off Windows by design -- errno 6 there is ENXIO and means something else -- so
// the retry and skip branches only exist here, and a cross-platform test of them
// would assert the opposite thing on each platform.

// setupRecorderT captures the decision instead of acting on it. The real
// *testing.T cannot be used: Fatalf and Skipf end the test, which is exactly what
// needs to be observed rather than suffered.
type setupRecorderT struct {
	fatal string
	skip  string
}

func (r *setupRecorderT) Helper() {}
func (r *setupRecorderT) Fatalf(format string, args ...any) {
	r.fatal = fmt.Sprintf(format, args...)
}
func (r *setupRecorderT) Skipf(format string, args ...any) {
	r.skip = fmt.Sprintf(format, args...)
}

func transientHandleError() error {
	return &os.PathError{Op: "write", Path: `C:\Temp\windows-setup.json`, Err: syscall.Errno(errorInvalidHandleOnWindows)}
}

// The ordinary case: setup that works is run once and says nothing.
func TestSetupThatSucceedsRunsOnce(t *testing.T) {
	rec := &setupRecorderT{}
	attempts := 0
	setupOrSkip(rec, "write something", func() error { attempts++; return nil })

	if attempts != 1 {
		t.Errorf("made %d attempts for setup that succeeded, want 1", attempts)
	}
	if rec.fatal != "" || rec.skip != "" {
		t.Errorf("successful setup reported fatal=%q skip=%q, want neither", rec.fatal, rec.skip)
	}
}

// A handle refused for a moment is waited out.
//
// This is the measured failure: the write succeeds shortly afterwards, so the
// test should proceed to its assertions rather than dying in setup.
func TestATransientSetupFailureIsRetriedToSuccess(t *testing.T) {
	prev := setupRetryInitialPause
	setupRetryInitialPause = 0
	t.Cleanup(func() { setupRetryInitialPause = prev })

	rec := &setupRecorderT{}
	attempts := 0
	setupOrSkip(rec, "write something", func() error {
		attempts++
		if attempts < 3 {
			return transientHandleError()
		}
		return nil
	})

	if attempts != 3 {
		t.Errorf("made %d attempts, want 3 (two transient failures then success)", attempts)
	}
	if rec.fatal != "" || rec.skip != "" {
		t.Errorf("a recovered transient reported fatal=%q skip=%q, want neither", rec.fatal, rec.skip)
	}
}

// A host that never gives the handle back ends in a skip, not a failure.
//
// The assertions genuinely did not run, so calling it a pass would be a lie --
// and calling it a failure trains people to re-run red builds without reading
// them, which is what this whole accommodation exists to stop. The message has to
// say an assertion was not checked.
func TestAPersistentTransientFailureSkipsRatherThanFails(t *testing.T) {
	prev := setupRetryInitialPause
	setupRetryInitialPause = 0
	t.Cleanup(func() { setupRetryInitialPause = prev })

	rec := &setupRecorderT{}
	attempts := 0
	setupOrSkip(rec, "write something", func() error { attempts++; return transientHandleError() })

	if rec.fatal != "" {
		t.Errorf("a host refusing handles was reported as a defect: %q", rec.fatal)
	}
	if rec.skip == "" {
		t.Fatal("a host refusing handles did not skip; the run would report success with the assertion unchecked")
	}
	if !contains(rec.skip, "NOT being checked") {
		t.Errorf("the skip does not say an assertion went unchecked: %q", rec.skip)
	}
	if attempts != 8 {
		t.Errorf("made %d attempts before giving up, want 8 (the bound)", attempts)
	}
}

// Anything else is still a defect, reported at once.
//
// The point of the errno check is that this helper forgives one specific host
// condition and nothing else. A disk that is full, a path that does not exist, a
// permission that is genuinely missing -- all must fail loudly and on the first
// attempt, or the helper has quietly become a way to make real failures vanish.
func TestARealSetupFailureStillFailsImmediately(t *testing.T) {
	rec := &setupRecorderT{}
	attempts := 0
	setupOrSkip(rec, "write something", func() error {
		attempts++
		return fmt.Errorf("no space left on device")
	})

	if rec.skip != "" {
		t.Errorf("a real failure was skipped: %q", rec.skip)
	}
	if rec.fatal == "" {
		t.Fatal("a real setup failure was not reported")
	}
	if attempts != 1 {
		t.Errorf("made %d attempts for a real failure, want 1", attempts)
	}
}
