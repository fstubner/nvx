package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestAbandonedInstallLockDoesNotBlockForever covers the failure an interrupted
// install used to leave behind: the lock file recorded a pid and nothing ever
// read it back, so Ctrl-C during a download blocked that version from ever being
// installed again. `nvx cleanup` does not touch install locks, and the error said
// "already in progress", which sends the user looking for a process that does not
// exist.
func TestAbandonedInstallLockDoesNotBlockForever(t *testing.T) {
	nvxHome := t.TempDir()
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
	nvxHome := t.TempDir()

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
	nvxHome := t.TempDir()
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
