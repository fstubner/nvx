package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// makeGuestHome creates a guest home under nvxHome's sandbox_home, optionally
// with an owner marker naming pid, and back-dates it by age.
func makeGuestHome(t *testing.T, nvxHome, id string, pid int, age time.Duration) string {
	t.Helper()
	dir := filepath.Join(getSandboxHomeDir(nvxHome), id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if pid > 0 {
		data, err := json.Marshal(sessionOwner{PID: pid, StartedUTC: time.Now().UTC().Format(time.RFC3339)})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, sessionOwnerFile), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if age > 0 {
		old := time.Now().Add(-age)
		if err := os.Chtimes(dir, old, old); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// deadPID returns a process id that is not running. Starting a real process and
// waiting for it to exit is the only way to get one that is genuinely dead
// rather than merely unlikely: the kernel will not reuse it while this test runs,
// and a made-up number could belong to anything.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := helperExitCommand(t)
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start a helper process: %v", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Wait()
	return pid
}

// TestCleanupLeavesRunningSandboxesAlone is F35. `nvx cleanup` deleted every
// guest home under sandbox_home unconditionally, so running it during a
// concurrent `npm install` destroyed that install's HOME while it was using it.
// npm lifecycles routinely run several nvx processes at once, so this needed no
// unusual usage to hit.
func TestCleanupLeavesRunningSandboxesAlone(t *testing.T) {
	nvxHome := tempDir(t)

	// Owned by this very process, which is by definition running.
	live := makeGuestHome(t, nvxHome, "live-session", os.Getpid(), 0)
	// Owned by a process that has exited.
	dead := makeGuestHome(t, nvxHome, "dead-session", deadPID(t), 0)

	cleanupStaleSandboxes(nvxHome, 0)

	if _, err := os.Stat(live); err != nil {
		t.Errorf("cleanup deleted a live session's guest home: %v\n"+
			"A concurrent install would lose its HOME mid-run.", err)
	}
	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Errorf("cleanup left a dead session's guest home behind (err=%v); it no longer cleans up anything", err)
	}
}

// TestCleanupHandlesGuestHomesWithNoOwnerMarker covers the ambiguous case: a
// directory with no marker is equally one written by an older nvx and one being
// created right now. Age decides, and the two directions are asserted together
// so a fix for either cannot silently become "delete everything" or "delete
// nothing".
func TestCleanupHandlesGuestHomesWithNoOwnerMarker(t *testing.T) {
	nvxHome := tempDir(t)

	fresh := makeGuestHome(t, nvxHome, "fresh-unowned", 0, 0)
	old := makeGuestHome(t, nvxHome, "old-unowned", 0, unownedGuestHomeGrace+time.Hour)

	cleanupStaleSandboxes(nvxHome, 0)

	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("cleanup deleted an unowned guest home created moments ago: %v\n"+
			"That is a session between MkdirAll and writing its marker.", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("cleanup kept an unowned guest home older than the grace period (err=%v); stale directories would accumulate forever", err)
	}
}

// TestGuestHomeIsInUseReadsTheMarker pins the decision function directly, so a
// failure says which rule broke rather than only that cleanup misbehaved.
func TestGuestHomeIsInUseReadsTheMarker(t *testing.T) {
	nvxHome := tempDir(t)
	now := time.Now()

	if !guestHomeIsInUse(makeGuestHome(t, nvxHome, "a", os.Getpid(), 0), now) {
		t.Error("a guest home owned by a running process must count as in use")
	}
	if guestHomeIsInUse(makeGuestHome(t, nvxHome, "b", deadPID(t), 0), now) {
		t.Error("a guest home owned by an exited process must not count as in use")
	}
	if !guestHomeIsInUse(makeGuestHome(t, nvxHome, "c", 0, 0), now) {
		t.Error("an unowned guest home inside the grace period must count as in use")
	}
	if guestHomeIsInUse(makeGuestHome(t, nvxHome, "d", 0, unownedGuestHomeGrace+time.Minute), now) {
		t.Error("an unowned guest home past the grace period must not count as in use")
	}
}

// TestSessionOwnerRoundTrips checks the marker createGuestProfile writes is the
// one cleanup reads. They are on opposite sides of a process boundary, so a
// format change that broke this would show up only as cleanup silently sparing
// or deleting everything.
func TestSessionOwnerRoundTrips(t *testing.T) {
	nvxHome := tempDir(t)
	guestHome, err := createGuestProfile(nvxHome, "abc123")
	if err != nil {
		t.Fatalf("createGuestProfile: %v", err)
	}

	owner, ok := readSessionOwner(guestHome)
	if !ok {
		t.Fatal("createGuestProfile did not leave a readable owner marker")
	}
	if owner.PID != os.Getpid() {
		t.Errorf("marker PID = %d, want this process (%d)", owner.PID, os.Getpid())
	}
	if _, err := time.Parse(time.RFC3339, owner.StartedUTC); err != nil {
		t.Errorf("marker start time %q is not RFC3339: %v", owner.StartedUTC, err)
	}
}

// TestProcessIsRunningAgreesWithReality guards the primitive the whole fix rests
// on. A processIsRunning that always returned true would make cleanup a no-op;
// always false would restore F35 exactly.
func TestProcessIsRunningAgreesWithReality(t *testing.T) {
	if !processIsRunning(os.Getpid()) {
		t.Error("processIsRunning says this process is not running")
	}
	if processIsRunning(deadPID(t)) {
		t.Error("processIsRunning says an exited process is still running")
	}
	if processIsRunning(0) || processIsRunning(-1) {
		t.Error("processIsRunning must reject non-positive pids rather than guessing")
	}
}

// Reclamation must happen without anyone asking for it.
//
// A process killed outright cannot clean up after itself, so leftovers are
// unavoidable; what was avoidable is requiring a command nobody runs. 91 guest
// homes had accumulated on the development machine before `nvx cleanup` was
// typed for the first time.
func TestStaleSandboxesAreReclaimedWithoutTheCleanupCommand(t *testing.T) {
	nvxHome := tempDir(t)

	// One abandoned session, and one standing in for a concurrent npm install.
	// Deleting the second is the bug that made reclamation explicit-only.
	dead := makeGuestHome(t, nvxHome, "dead-session", deadPID(t), 0)
	live := makeGuestHome(t, nvxHome, "live-session", os.Getpid(), 0)

	reclaimStaleSandboxes(nvxHome)

	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Error("an abandoned sandbox home survived the automatic sweep; disk accumulates " +
			"until someone happens to run nvx cleanup, which is the thing being removed")
	}
	if _, err := os.Stat(live); err != nil {
		t.Errorf("the automatic sweep removed a session that is still running: %v", err)
	}
}

// One command must not pay for a whole backlog.
func TestAutomaticReclamationIsBounded(t *testing.T) {
	nvxHome := tempDir(t)
	stale := deadPID(t)
	const total = reclaimBudgetPerRun + 5
	for i := 0; i < total; i++ {
		makeGuestHome(t, nvxHome, fmt.Sprintf("stale-%d", i), stale, 0)
	}

	reclaimStaleSandboxes(nvxHome)

	left, err := os.ReadDir(getSandboxHomeDir(nvxHome))
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != total-reclaimBudgetPerRun {
		t.Errorf("the sweep removed %d of %d in one run; it should stop at %d and let the next "+
			"run continue", total-len(left), total, reclaimBudgetPerRun)
	}
}
