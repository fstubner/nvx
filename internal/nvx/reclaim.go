package nvx

import (
	"os"
	"path/filepath"
	"time"
)

// When the automatic sweep runs, and how long a command waits for it.
//
// reclaimStaleSandboxes ran after every shimmed command and the command did not
// finish until it had. Measured 2026-10-08 on Windows with 638 package profiles
// on the machine, the package-profile sweep alone took 83 to 91 ms a call (three
// runs of 20). With one guest home of 12,000 files left by a killed install,
// `node -e 0` through the shim took 3.50 s (median of 5), because it waited for
// the home to be deleted.
//
// Two changes. The sweeps that go by age (package profiles, rescued logs, staged
// command copies) only find something once it is a week or two old, so looking
// on every command found what the last look found. They run when the last full
// look is an hour old. And the command waits for the sweep only briefly. What it
// has not finished, the next command finishes. Each step is bounded, can be run
// again, and is safe to leave half done, which is what a killed process leaves
// anyway.
//
// One sweep runs at a time. Two commands that end together used to sweep
// together, each trying to delete the same guest home. The lock is the operating
// system's, so a process that dies holding it does not leave it held.

// retentionSweepInterval is how long a full look at the age-based leftovers
// stays good.
const retentionSweepInterval = time.Hour

// reclaimExitGrace is how long the end of a command waits for the sweep. Zero
// waits for all of it, which is what the tests want. A variable for them.
var reclaimExitGrace = 100 * time.Millisecond

// reclaimGuestHomes is the step that runs on every sweep. A variable so a test
// can make it slow, to show that a command does not wait for it.
var reclaimGuestHomes = func(nvxHome string) {
	cleanupStaleSandboxes(nvxHome, reclaimBudgetPerRun)
}

// reclaimAfterCommand is the sweep a command runs on its way out. It returns
// when the sweep has finished or reclaimExitGrace has passed.
func reclaimAfterCommand(nvxHome string) {
	reclaim(nvxHome, reclaimExitGrace)
}

// reclaim sweeps once and waits for it at most grace, or for all of it when
// grace is zero.
func reclaim(nvxHome string, grace time.Duration) {
	if nvxHome == "" {
		return
	}
	release, ok := lockReclaim(nvxHome)
	if !ok {
		// Another command is sweeping. What it leaves, a later command takes.
		return
	}
	retention := retentionSweepDue(nvxHome)
	backlog := false
	if retention {
		// AppContainer package profiles, on Windows. Swept here rather than only
		// from `nvx cleanup` for the same reason guest homes are. A command nobody
		// runs reclaims nothing. One profile is registered per project nvx has ever
		// contained. The first version of this swept only on an explicit cleanup,
		// and only when no session at all was running. On a machine with a couple
		// of long-lived MCP servers that is never.
		//
		// Safe unprompted on the same terms. A package held by a live session is
		// skipped, and one used inside the retention window is left alone so the
		// common case never pays to re-register a profile it is about to use again.
		//
		// This runs here and not in the goroutine below. Deleting a profile is a
		// call into Windows that this process should not be cut off in the middle
		// of, and the process may leave while the goroutine is still working.
		backlog = sweepOrphanedSandboxPackages(nvxHome, reclaimBudgetPerRun) >= reclaimBudgetPerRun
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer release()
		reclaimGuestHomes(nvxHome)
		if !retention {
			return
		}
		// Logs rescued from failed runs. They had no sweep at all, so they
		// accumulated for the life of the installation. That was 3,146 directories
		// and 181 MB on the development machine, which `nvx cleanup` also left alone.
		//
		// A larger budget than the sweeps above, because the work is not
		// comparable. A guest home may hold a large tree and a package profile
		// costs a registry write. These are small directories of log files. At
		// eight per run a three-thousand-folder backlog needs some four hundred
		// commands to clear, which is not a reclaim so much as a rumour of one.
		if sweepRescuedLogs(nvxHome, rescuedLogBudgetPerRun) >= rescuedLogBudgetPerRun {
			backlog = true
		}
		// Staged command copies whose command has changed. One per run: each is a
		// whole directory, up to tens of thousands of files.
		if pruneStaleCommandCopies(nvxHome, 1) >= 1 {
			backlog = true
		}
		// A step that used its whole budget may have more to do, so the next
		// command looks again instead of waiting out the hour.
		if !backlog {
			markRetentionSweep(nvxHome)
		}
	}()

	if grace <= 0 {
		<-done
		return
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}

// reclaimLockPath is the file the sweep's lock is taken on.
func reclaimLockPath(nvxHome string) string { return filepath.Join(nvxHome, "reclaim.lock") }

// lockReclaim takes the right to sweep, and reports false when another process
// holds it. release gives it back. A lock the file system cannot give is not a
// reason to skip the sweep, which is housekeeping and was safe to run twice.
func lockReclaim(nvxHome string) (release func(), ok bool) {
	f, err := os.OpenFile(reclaimLockPath(nvxHome), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return func() {}, true
	}
	locked, err := tryLockFileExclusive(f)
	if err == nil && !locked {
		_ = f.Close()
		return nil, false
	}
	// Closing the file releases the lock, and so does the process ending.
	return func() { _ = f.Close() }, true
}

func retentionStampPath(nvxHome string) string { return filepath.Join(nvxHome, "reclaim.stamp") }

// retentionSweepDue reports whether the age-based sweeps have not run in the
// last retentionSweepInterval. A stamp dated in the future, from a clock that
// was put back, counts as due.
func retentionSweepDue(nvxHome string) bool {
	info, err := os.Stat(retentionStampPath(nvxHome))
	if err != nil {
		return true
	}
	age := time.Since(info.ModTime())
	return age < 0 || age >= retentionSweepInterval
}

func markRetentionSweep(nvxHome string) {
	_ = os.WriteFile(retentionStampPath(nvxHome), nil, 0o600)
}
