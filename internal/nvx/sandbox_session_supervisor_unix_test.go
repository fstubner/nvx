//go:build !windows

package nvx

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A guest home stays in use while its supervisor runs, even when the nvx that
// started it is gone. The marker named only nvx, so a run that outlived its nvx
// looked abandoned and the next cleanup deleted the home under it.
func TestGuestHomeStaysInUseWhileTheSupervisorRuns(t *testing.T) {
	supervisor := exec.Command("/bin/sh", "-c", "sleep 60")
	if err := supervisor.Start(); err != nil {
		t.Fatalf("start a stand-in supervisor: %v", err)
	}
	defer func() { _ = supervisor.Process.Kill(); _ = supervisor.Wait() }()

	home := tempDir(t)
	writeOwner := func(o sessionOwner) {
		data, _ := json.Marshal(o)
		if err := os.WriteFile(filepath.Join(home, sessionOwnerFile), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	started := time.Now().UTC().Format(time.RFC3339)

	writeOwner(sessionOwner{PID: deadPID(t), StartedUTC: started})
	if guestHomeIsInUse(home, time.Now()) {
		t.Fatal("a home whose owner is gone was reported in use with no supervisor recorded")
	}

	writeOwner(sessionOwner{PID: deadPID(t), StartedUTC: started, SupervisorPID: supervisor.Process.Pid})
	if !guestHomeIsInUse(home, time.Now()) {
		t.Error("a home whose supervisor is running was reported unused; cleanup would delete it under the contained process")
	}

	_ = supervisor.Process.Kill()
	_ = supervisor.Wait()
	if guestHomeIsInUse(home, time.Now()) {
		t.Error("a home whose owner and supervisor have both exited was reported in use")
	}
}

// recordSupervisorPID updates this process's own records, in the ephemeral
// marker and in a lease, and leaves another nvx process's lease alone. A
// persistent tool home is shared by concurrent runs.
func TestRecordSupervisorPIDTouchesOnlyThisProcessRecords(t *testing.T) {
	home := tempDir(t)
	now := time.Now()
	writeSessionOwner(home, now)
	release := writeSessionLease(home, "mine", now)
	defer release()
	other := deadPID(t)
	data, _ := json.Marshal(sessionOwner{PID: other, StartedUTC: now.UTC().Format(time.RFC3339)})
	otherLease := filepath.Join(home, sessionLeasePrefix+"theirs")
	if err := os.WriteFile(otherLease, data, 0o600); err != nil {
		t.Fatal(err)
	}

	recordSupervisorPID(home, 4242)

	if o, ok := readSessionOwner(home); !ok || o.SupervisorPID != 4242 || o.PID != os.Getpid() {
		t.Errorf("session marker = %+v, want this process with supervisor 4242", o)
	}
	read := func(path string) sessionOwner {
		var o sessionOwner
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &o); err != nil {
			t.Fatal(err)
		}
		return o
	}
	if o := read(filepath.Join(home, sessionLeasePrefix+"mine")); o.SupervisorPID != 4242 {
		t.Errorf("own lease = %+v, want supervisor 4242", o)
	}
	if o := read(otherLease); o.SupervisorPID != 0 || o.PID != other {
		t.Errorf("another process's lease was changed: %+v", o)
	}
}

// A runtime killed by a signal is reported as 128 plus the signal, as a shell
// does, not as -1 (which os.Exit turns into 255).
func TestChildExitCodeReportsSignalDeathsAsShellDoes(t *testing.T) {
	for _, tc := range []struct {
		script string
		want   int
	}{
		{"kill -TERM $$", 143},
		{"kill -KILL $$", 137},
		{"exit 7", 7},
	} {
		err := exec.Command("/bin/sh", "-c", tc.script).Run()
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("%q: expected an ExitError, got %v", tc.script, err)
		}
		if got := childExitCode(exitErr); got != tc.want {
			t.Errorf("%q: childExitCode = %d, want %d", tc.script, got, tc.want)
		}
	}
}

// The signals nvx forwards leave out any it was started ignoring, so
// `nohup nvx node x.js` keeps the protection nohup gave it.
func TestForwardedSignalsRespectAnIgnoredHangup(t *testing.T) {
	if os.Getenv("NVX_TEST_IGNORED_HUP_CHILD") == "1" {
		for _, s := range forwardedSignals() {
			if s.String() == "hangup" {
				os.Exit(3)
			}
		}
		os.Exit(0)
	}
	cmd := exec.Command("/bin/sh", "-c", `trap '' HUP; exec "$0" -test.run=^TestForwardedSignalsRespectAnIgnoredHangup$`, os.Args[0])
	cmd.Env = append(os.Environ(), "NVX_TEST_IGNORED_HUP_CHILD=1")
	if err := cmd.Run(); err != nil {
		t.Errorf("SIGHUP was in the forwarded set although it was ignored at start: %v", err)
	}
}
