//go:build windows

package main

import (
	"fmt"
	"os"
	"syscall"
	"testing"
	"time"
)

// The classifier decides whether a command is safe to run again, so it is
// checked case by case rather than only through the retry that uses it.
//
// The two false cases carry the weight. A missing executable fails at fork/exec
// too and must not be retried five times before nvx says what is wrong; and
// ERROR_INVALID_HANDLE reaching us from a READ is a different event -- the
// process ran -- which a check on the error code alone would confuse with this
// one.
func TestAProcessThatNeverStartedIsRecognised(t *testing.T) {
	forkExec := func(errno syscall.Errno) error {
		return &os.PathError{Op: "fork/exec", Path: `C:\Windows\system32\icacls.exe`, Err: errno}
	}

	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"the measured transient", forkExec(errorInvalidHandle), true},
		{"a missing executable", forkExec(syscall.Errno(2)), false},
		{"access denied at fork/exec", forkExec(syscall.Errno(5)), false},
		{"the same code from a read", &os.PathError{Op: "read", Err: syscall.Errno(errorInvalidHandle)}, false},
		{"an ordinary command failure", fmt.Errorf("icacls exited 1"), false},
		{"no error at all", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := processNeverStarted(tc.err); got != tc.want {
				t.Errorf("processNeverStarted(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// stubWinCmdOnce replaces the single attempt and reports how many were made.
// The pause goes to zero: this test is about the count, not the wait.
func stubWinCmdOnce(t *testing.T, fn func(attempt int) ([]byte, error)) *int {
	t.Helper()
	prevOnce, prevPause := runWinCmdOnce, winCmdRetryPause
	t.Cleanup(func() { runWinCmdOnce, winCmdRetryPause = prevOnce, prevPause })
	winCmdRetryPause = 0

	attempts := 0
	runWinCmdOnce = func(timeout time.Duration, name, tool string, args ...string) ([]byte, error) {
		attempts++
		return fn(attempts)
	}
	return &attempts
}

// A command whose process was never created is run again.
//
// This is the failure that turned Windows CI red four times in two days on four
// different tests: the runner momentarily could not create a process, icacls
// failed at fork/exec, and prepareAppContainerFilesystem reported it as a
// containment defect. On a user's machine the same moment refuses their sandbox.
func TestATransientProcessCreationFailureIsRetried(t *testing.T) {
	attempts := stubWinCmdOnce(t, func(attempt int) ([]byte, error) {
		if attempt < 3 {
			return nil, &os.PathError{Op: "fork/exec", Path: "icacls.exe", Err: syscall.Errno(errorInvalidHandle)}
		}
		return []byte("labelled"), nil
	})

	out, err := runWinCmd(5*time.Second, "icacls", "/?")
	if err != nil {
		t.Fatalf("a transient process-creation failure was not retried to success: %v", err)
	}
	if *attempts != 3 {
		t.Errorf("made %d attempts, want 3 (two transient failures then success)", *attempts)
	}
	if string(out) != "labelled" {
		t.Errorf("output of the successful attempt was lost: %q", out)
	}
}

// Anything else is reported at once.
//
// Retrying a permanent failure costs the caller five waits and then tells them
// the same thing. Worse here than usual: this runs on the launch path, so it is
// time a person spends watching a sandbox not start.
func TestAPermanentFailureIsNotRetried(t *testing.T) {
	attempts := stubWinCmdOnce(t, func(int) ([]byte, error) {
		return nil, &os.PathError{Op: "fork/exec", Path: "icacls.exe", Err: syscall.Errno(2)}
	})

	if _, err := runWinCmd(5*time.Second, "icacls", "/?"); err == nil {
		t.Fatal("a missing executable was reported as success")
	}
	if *attempts != 1 {
		t.Errorf("made %d attempts for a permanent failure, want 1", *attempts)
	}
}

// A success is not retried either -- the obvious property, and the one a
// miswritten loop breaks first.
func TestASuccessfulCommandRunsOnce(t *testing.T) {
	attempts := stubWinCmdOnce(t, func(int) ([]byte, error) { return []byte("ok"), nil })

	if _, err := runWinCmd(5*time.Second, "icacls", "/?"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *attempts != 1 {
		t.Errorf("made %d attempts for a command that succeeded, want 1", *attempts)
	}
}

// The retry is bounded, and the last error is what the caller sees.
//
// A machine that stays unable to create processes must end in nvx saying so
// rather than in a loop: the sandbox has to fail closed, and it cannot do that
// from inside a retry that never ends.
func TestTheRetryGivesUpAndReportsTheFailure(t *testing.T) {
	attempts := stubWinCmdOnce(t, func(int) ([]byte, error) {
		return nil, &os.PathError{Op: "fork/exec", Path: "icacls.exe", Err: syscall.Errno(errorInvalidHandle)}
	})

	_, err := runWinCmd(5*time.Second, "icacls", "/?")
	if err == nil {
		t.Fatal("a host that never recovered was reported as success")
	}
	if *attempts != winCmdRetries {
		t.Errorf("made %d attempts, want %d (the bound)", *attempts, winCmdRetries)
	}
	if !processNeverStarted(err) {
		t.Errorf("the error returned is not the one the attempts failed with: %v", err)
	}
}
