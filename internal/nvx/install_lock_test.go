package nvx

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Two waiters that both find the same abandoned lock must not both get it. The
// steal read the dead pid and then removed the file, so a second waiter running
// its whole steal inside that gap had its fresh lock removed by the first, and
// both installs went ahead. On Windows the open lock file cannot be removed, so
// the old code only lost this race on Unix.
func TestAbandonedInstallLockIsStolenByOneWaiterOnly(t *testing.T) {
	nvxHome := tempDir(t)
	lockDir := filepath.Join(nvxHome, "versions", "node")
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	name, err := installLockFileName("v22.11.0")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lockDir, name), []byte(fmt.Sprintf("%d\n", deadPID(t))), 0o600); err != nil {
		t.Fatal(err)
	}

	type result struct {
		release func()
		err     error
	}
	second := make(chan result, 1)
	var once sync.Once
	installLockBeforeRemove = func() {
		once.Do(func() {
			go func() {
				r, err := acquireRuntimeInstallLock(nvxHome, "node", "v22.11.0")
				second <- result{r, err}
			}()
			// Room for the second waiter to run its whole steal. Serialised, it
			// cannot, and waits until the first is done.
			select {
			case r := <-second:
				second <- r
			case <-time.After(500 * time.Millisecond):
			}
		})
	}
	first, err1 := acquireRuntimeInstallLock(nvxHome, "node", "v22.11.0")
	r2 := <-second
	installLockBeforeRemove = nil

	held := 0
	for _, r := range []result{{first, err1}, r2} {
		if r.err == nil {
			held++
			r.release()
		}
	}
	if held != 1 {
		t.Errorf("%d waiters took the abandoned lock, want 1; two installs would extract into one directory", held)
	}
}

// TestAbandonedInstallLockDoesNotBlockForever covers the failure an interrupted
// install used to leave behind: the lock file recorded a pid and nothing ever
// read it back, so Ctrl-C during a download blocked that version from ever being
// installed again. `nvx cleanup` does not touch install locks, and the error said
// "already in progress", which sends the user looking for a process that does not
// exist.
func TestAbandonedInstallLockDoesNotBlockForever(t *testing.T) {
	nvxHome := tempDir(t)
	lockDir := filepath.Join(nvxHome, "versions", "node")
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	name, err := installLockFileName("v22.11.0")
	if err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(lockDir, name)

	// A lock left by a process that has since exited.
	dead := deadPID(t)
	if err := os.WriteFile(lockPath, []byte(fmt.Sprintf("%d\n", dead)), 0o600); err != nil {
		t.Fatal(err)
	}

	release, err := acquireRuntimeInstallLock(nvxHome, "node", "v22.11.0")
	if err != nil {
		t.Fatalf("an abandoned lock still blocks the install: %v\n"+
			"A cancelled install would make this version permanently uninstallable.", err)
	}
	release()

	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Errorf("releasing the lock left the file behind (err=%v)", err)
	}
}

// TestLiveInstallLockIsRespected is the other half, and the one that matters more:
// clearing a lock whose owner is alive would let two installs extract into the
// same directory at once. The bias is deliberate — only a provably dead owner
// releases it.
func TestLiveInstallLockIsRespected(t *testing.T) {
	nvxHome := tempDir(t)

	release, err := acquireRuntimeInstallLock(nvxHome, "node", "v22.11.0")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer release()

	// A second acquire, while this process still holds it, must fail.
	if _, err := acquireRuntimeInstallLock(nvxHome, "node", "v22.11.0"); err == nil {
		t.Error("a second install acquired a lock this process is holding; two installs could extract into the same directory")
	}
}

// TestUnreadableInstallLockIsLeftAlone pins the conservative direction. A lock
// with no parseable pid could belong to a running install written by a different
// version of nvx; "I cannot tell who owns this" is not evidence that nobody does.
func TestUnreadableInstallLockIsLeftAlone(t *testing.T) {
	nvxHome := tempDir(t)
	lockDir := filepath.Join(nvxHome, "versions", "node")
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	name, err := installLockFileName("v22.11.0")
	if err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(lockDir, name)

	for _, content := range []string{"", "not-a-pid", "-1", "0"} {
		if err := os.WriteFile(lockPath, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := acquireRuntimeInstallLock(nvxHome, "node", "v22.11.0"); err == nil {
			t.Errorf("a lock containing %q was cleared; an unparseable lock must be treated as held", content)
		}
	}
}
