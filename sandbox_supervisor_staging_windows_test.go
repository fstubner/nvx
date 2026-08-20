//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A supervisor left running by an earlier build used to block every later
// contained launch.
//
// Windows refuses to replace a running executable, and the staged supervisor had
// one fixed name that each new build overwrote in place. A contained process that
// hangs -- asynchronous piped stdio still does -- leaves its supervisor alive
// holding that file, after which staging fails with a bare "Access is denied"
// naming a path inside ~/.nvx that the user has no reason to connect to their
// stuck process. Observed here after a rebuild; the only cure was finding and
// killing the process by hand.
//
// The fix is per-build names, so nothing is ever replaced. These assert that
// property directly rather than through a real launch, because holding a genuine
// AppContainer supervisor open is not something a unit test can arrange.
func TestSupervisorStagingDoesNotReplaceARunningCopy(t *testing.T) {
	nvxHome := tempDir(t)

	first, err := stageAppContainerSupervisor(nvxHome)
	if err != nil {
		t.Fatalf("first stage: %v", err)
	}
	if _, err := os.Stat(first); err != nil {
		t.Fatalf("staged supervisor missing: %v", err)
	}

	// Staging again with the same build must reuse the same copy rather than
	// rewriting it -- that is what makes a running supervisor harmless.
	again, err := stageAppContainerSupervisor(nvxHome)
	if err != nil {
		t.Fatalf("second stage: %v", err)
	}
	if again != first {
		t.Errorf("the same build staged to two different paths (%q then %q)", first, again)
	}

	// Reproduce the reported failure. Under the old scheme every build staged to
	// this one fixed path, so a supervisor still running from an earlier build
	// held it open and the next launch could not replace it. Holding the file open
	// is what a live supervisor does; an open handle is enough for Windows to
	// refuse both replace and delete.
	dir := filepath.Dir(first)
	other := filepath.Join(dir, "nvx.exe")
	if err := os.WriteFile(other, []byte("an older build, still running"), 0o700); err != nil {
		t.Fatal(err)
	}
	held, err := os.Open(other)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	// Staging must still succeed, and must not have needed to touch the held file.
	third, err := stageAppContainerSupervisor(nvxHome)
	if err != nil {
		t.Fatalf("staging failed while another build's copy was held open -- this is the bug: %v", err)
	}
	if third != first {
		t.Errorf("staged path changed to %q while a stale copy was held", third)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("the held copy was removed despite being in use: %v", err)
	}
}

// The legacy fixed name from before per-build naming should be cleaned up, but
// only when nothing is using it.
func TestSupervisorStagingClearsTheLegacyFixedName(t *testing.T) {
	nvxHome := tempDir(t)
	dir := filepath.Join(nvxHome, "sandbox-exec", "supervisor")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(dir, "nvx.exe")
	if err := os.WriteFile(legacy, []byte("pre-per-build copy"), 0o700); err != nil {
		t.Fatal(err)
	}

	staged, err := stageAppContainerSupervisor(nvxHome)
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	if strings.EqualFold(filepath.Base(staged), "nvx.exe") {
		t.Fatal("still staging to the fixed name a running supervisor would lock")
	}
	if _, err := os.Stat(legacy); err == nil {
		t.Error("the legacy fixed-name copy was left behind")
	}
}
