package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Reported from real use 2026-08-20. A failed contained install ended with:
//
//	npm error A complete log of this run can be found in:
//	C:\Users\<u>\.nvx\sandbox_home\<id>\...\npm-cache\_logs\...-debug-0.log
//
// and that tree was deleted when the run ended, so the path was dead before the
// user finished reading it -- on precisely the occasion the log is wanted.
func TestFailedRunsKeepTheirDebugLogs(t *testing.T) {
	nvxHome := tempDir(t)
	guestHome := tempDir(t)

	// The real layout is <guest>/AppData/Local/Packages/<pkg>/AC/npm-cache/_logs
	// on Windows and <guest>/.npm/_logs elsewhere. Both are found by name rather
	// than by reconstructing either, so this uses a nested path to prove the
	// search is not hard-coded to one shape.
	logDir := filepath.Join(guestHome, "AppData", "Local", "Packages", "nvx.sandbox", "AC", "npm-cache", "_logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	const body = "0 verbose cli npm-cli.js\n1 error E404 not found\n"
	if err := os.WriteFile(filepath.Join(logDir, "2026-08-20T14_00_49_697Z-debug-0.log"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	dest := rescueSandboxLogs(nvxHome, guestHome, "sess1")
	if dest == "" {
		t.Fatal("no logs were rescued; a failing install would still point at a path that is about to be deleted")
	}

	// It must survive the guest home, which is the entire point.
	if err := os.RemoveAll(guestHome); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatalf("rescued log directory unreadable after the guest home was removed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 rescued log, got %d", len(entries))
	}
	got, err := os.ReadFile(filepath.Join(dest, entries[0].Name()))
	if err != nil {
		t.Fatalf("rescued log unreadable: %v", err)
	}
	if string(got) != body {
		t.Errorf("rescued log content differs:\ngot  %q\nwant %q", got, body)
	}

	// Outside the guest home, and under nvxHome rather than the project: a
	// contained process must not be able to reach or rewrite its own evidence.
	if !strings.HasPrefix(filepath.Clean(dest), filepath.Clean(nvxHome)) {
		t.Errorf("logs were rescued to %q, which is outside nvxHome %q", dest, nvxHome)
	}
}

// Nothing to rescue must leave nothing behind -- no empty directory per run, and
// no message pointing at one.
func TestRescueIsSilentWhenThereAreNoLogs(t *testing.T) {
	nvxHome := tempDir(t)
	guestHome := tempDir(t)
	if err := os.MkdirAll(filepath.Join(guestHome, "AppData", "Local"), 0o700); err != nil {
		t.Fatal(err)
	}

	if dest := rescueSandboxLogs(nvxHome, guestHome, "sess2"); dest != "" {
		t.Errorf("rescue reported %q for a guest home with no logs", dest)
	}
	if _, err := os.Stat(rescuedLogsDir(nvxHome, "sess2")); err == nil {
		t.Error("an empty rescue directory was left behind; every run would litter one")
	}
}

func TestDebugLogDirsAreFoundByNameAtAnyDepth(t *testing.T) {
	guestHome := tempDir(t)
	deep := filepath.Join(guestHome, "a", "b", "c", "_logs")
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatal(err)
	}
	// Casing varies by tool; matching must not depend on it.
	upper := filepath.Join(guestHome, "other", "_LOGS")
	if err := os.MkdirAll(upper, 0o700); err != nil {
		t.Fatal(err)
	}

	dirs := findDebugLogDirs(guestHome)
	if len(dirs) != 2 {
		t.Fatalf("found %d log dirs, want 2: %v", len(dirs), dirs)
	}
}
