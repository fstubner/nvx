package nvx

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// oldRescuedLog makes a rescued log folder past the retention window.
func oldRescuedLog(t *testing.T, nvxHome, name string) string {
	t.Helper()
	dir := filepath.Join(nvxHome, "logs", name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-rescuedLogRetention - time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
	return dir
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// The sweeps that go by age find something only once it is a week or two old, so
// they look once an hour and not after every command. Measured 2026-10-08 on
// Windows, the package sweep alone took 83 to 91 ms a call.
func TestAgeBasedLeftoversAreLookedForOnceAnHour(t *testing.T) {
	isolatePackageSweep(t)
	nvxHome := tempDir(t)

	first := oldRescuedLog(t, nvxHome, "first")
	reclaimStaleSandboxes(nvxHome)
	if pathExists(first) {
		t.Fatal("the first sweep left a rescued log past its retention window")
	}

	second := oldRescuedLog(t, nvxHome, "second")
	reclaimStaleSandboxes(nvxHome)
	if !pathExists(second) {
		t.Fatal("a sweep a moment after the last full look looked again")
	}

	old := time.Now().Add(-retentionSweepInterval - time.Minute)
	if err := os.Chtimes(retentionStampPath(nvxHome), old, old); err != nil {
		t.Fatal(err)
	}
	reclaimStaleSandboxes(nvxHome)
	if pathExists(second) {
		t.Fatal("a sweep an hour after the last full look did not look again")
	}
}

// A clock put back leaves a stamp dated in the future, and the age-based sweeps
// must not wait for the clock to catch up with it.
func TestAStampFromTheFutureDoesNotHoldBackTheSweep(t *testing.T) {
	isolatePackageSweep(t)
	nvxHome := tempDir(t)
	markRetentionSweep(nvxHome)
	future := time.Now().Add(48 * time.Hour)
	if err := os.Chtimes(retentionStampPath(nvxHome), future, future); err != nil {
		t.Fatal(err)
	}
	log := oldRescuedLog(t, nvxHome, "stuck")
	reclaimStaleSandboxes(nvxHome)
	if pathExists(log) {
		t.Fatal("a stamp dated in the future stopped the sweep")
	}
}

// A sweep that used its whole budget may have more to do. The next command looks
// again, and the hour starts when a look finds the end.
func TestABacklogIsNotWaitedOutForAnHour(t *testing.T) {
	isolatePackageSweep(t)
	nvxHome := tempDir(t)
	const total = rescuedLogBudgetPerRun + 5
	for i := 0; i < total; i++ {
		oldRescuedLog(t, nvxHome, fmt.Sprintf("log%03d", i))
	}

	count := func() int {
		entries, _ := os.ReadDir(filepath.Join(nvxHome, "logs"))
		return len(entries)
	}
	reclaimStaleSandboxes(nvxHome)
	if got := count(); got != total-rescuedLogBudgetPerRun {
		t.Fatalf("%d folders left after a sweep of budget %d from %d, want %d", got, rescuedLogBudgetPerRun, total, total-rescuedLogBudgetPerRun)
	}
	if pathExists(retentionStampPath(nvxHome)) {
		t.Fatal("a sweep that stopped at its budget recorded a full look")
	}
	reclaimStaleSandboxes(nvxHome)
	if got := count(); got != 0 {
		t.Fatalf("%d folders left after the second sweep, want none", got)
	}
	if !pathExists(retentionStampPath(nvxHome)) {
		t.Fatal("a sweep that reached the end recorded no full look")
	}
}

// Two commands that end together used to sweep together, each trying to delete
// the same guest home. One sweeps and the other leaves it, and the lock is the
// operating system's, so a sweep that is killed does not leave it held.
func TestOnlyOneSweepRunsAtATime(t *testing.T) {
	isolatePackageSweep(t)
	nvxHome := tempDir(t)
	dead := makeGuestHome(t, nvxHome, "dead-session", deadPID(t), 0)

	holder, err := os.OpenFile(reclaimLockPath(nvxHome), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	locked, err := tryLockFileExclusive(holder)
	if err != nil || !locked {
		t.Fatalf("could not take the sweep lock for the test: %v, %v", locked, err)
	}

	reclaimStaleSandboxes(nvxHome)
	if !pathExists(dead) {
		t.Fatal("a second sweep ran while another held the lock")
	}

	_ = holder.Close()
	reclaimStaleSandboxes(nvxHome)
	if pathExists(dead) {
		t.Fatal("the sweep did not run once the lock was free")
	}
}

// lockIsFree reports whether nothing holds the sweep lock.
func lockIsFree(t *testing.T, nvxHome string) bool {
	t.Helper()
	probe, err := os.OpenFile(reclaimLockPath(nvxHome), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	locked, _ := tryLockFileExclusive(probe)
	return locked
}

// The command waits for the sweep only briefly. Measured 2026-10-08 on Windows,
// an unrelated command waited 3.50 s (median of 5) for a guest home of 12,000
// files, left by a killed install, to be deleted.
func TestACommandDoesNotWaitLongForASlowSweep(t *testing.T) {
	isolatePackageSweep(t)
	nvxHome := tempDir(t)

	release := make(chan struct{})
	var finished atomic.Bool
	orig := reclaimGuestHomes
	t.Cleanup(func() { reclaimGuestHomes = orig })
	reclaimGuestHomes = func(string) {
		<-release
		finished.Store(true)
	}

	start := time.Now()
	reclaim(nvxHome, 50*time.Millisecond)
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("the command waited %s for a sweep that was still running", took)
	}
	if finished.Load() {
		t.Fatal("the sweep finished, so the test did not show that the command left it running")
	}

	// The sweep goes on holding the lock, so a second command does not start
	// another one on top of it.
	if lockIsFree(t, nvxHome) {
		t.Error("the lock was free while the sweep was still running")
	}

	close(release)
	deadline := time.Now().Add(10 * time.Second)
	for !lockIsFree(t, nvxHome) {
		if time.Now().After(deadline) {
			t.Fatal("the sweep never released its lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !finished.Load() {
		t.Error("the lock was released before the sweep finished")
	}
}

// With no time limit the command waits for the whole sweep, which is what the
// tests and reclaimStaleSandboxes rely on.
func TestWithoutATimeLimitTheCommandWaitsForTheSweep(t *testing.T) {
	isolatePackageSweep(t)
	nvxHome := tempDir(t)

	var finished atomic.Bool
	orig := reclaimGuestHomes
	t.Cleanup(func() { reclaimGuestHomes = orig })
	reclaimGuestHomes = func(string) {
		time.Sleep(100 * time.Millisecond)
		finished.Store(true)
	}

	reclaim(nvxHome, 0)
	if !finished.Load() {
		t.Fatal("reclaim returned before a sweep with no time limit had finished")
	}
}
