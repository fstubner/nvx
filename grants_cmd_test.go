package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunGrantsListShowsCurrentProjectGrants(t *testing.T) {
	tmp := tempDir(t)
	projectDir := filepath.Join(tmp, "project")
	nvxHome := filepath.Join(tmp, ".nvx")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nvxHome, 0755); err != nil {
		t.Fatal(err)
	}

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWd) }()
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	scope := projectScopeDir()
	g := loadProjectGrants(nvxHome, scope)
	g.AllowHosts = append(g.AllowHosts, "example.com:443")
	g.TrustedTools = append(g.TrustedTools, "wrangler")
	g.ProjectPath = scope
	if err := saveProjectGrants(nvxHome, g); err != nil {
		t.Fatal(err)
	}

	out := formatProjectGrants(g)
	if !containsAll(out, "example.com:443", "wrangler") {
		t.Fatalf("expected grants listing to mention the host and tool, got:\n%s", out)
	}
}

func TestRunGrantsResetRemovesGrantFile(t *testing.T) {
	tmp := tempDir(t)
	projectDir := filepath.Join(tmp, "project")
	nvxHome := filepath.Join(tmp, ".nvx")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nvxHome, 0755); err != nil {
		t.Fatal(err)
	}

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWd) }()
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	scope := projectScopeDir()
	g := loadProjectGrants(nvxHome, scope)
	g.TrustedTools = append(g.TrustedTools, "wrangler")
	g.ProjectPath = scope
	if err := saveProjectGrants(nvxHome, g); err != nil {
		t.Fatal(err)
	}
	path := grantsPath(nvxHome, scope)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("setup: grant file should exist before reset: %v", err)
	}

	if code := runGrants([]string{"reset"}, nvxHome); code != 0 {
		t.Fatalf("runGrants reset exit code = %d, want 0", code)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected grant file removed after reset, stat err=%v", err)
	}
}

// `grants reset --all` must not report success it did not achieve.
//
// A record it cannot read is left in place, and so is every filesystem
// permission that record names -- which is the right call, since deleting it
// would destroy the only trace those permissions exist. What was wrong is that
// it then printed "Reset all project grants." and exited 0 anyway, two lines
// after warning it had skipped the record. A script that runs this to clear a
// machine's sandbox permissions read that as done.
func TestResettingAllReportsFailureWhenARecordWasLeftBehind(t *testing.T) {
	nvxHome := filepath.Join(tempDir(t), ".nvx")
	dir := grantsDir(nvxHome)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	stuck := filepath.Join(dir, "0123456789abcdef.json")
	if err := os.WriteFile(stuck, []byte("this is not json {{{"), 0644); err != nil {
		t.Fatal(err)
	}

	if code := runGrants([]string{"reset", "--all"}, nvxHome); code == 0 {
		t.Fatal("reported success after leaving a record, and the permissions it names, in place")
	}
	if _, err := os.Stat(stuck); err != nil {
		t.Fatalf("the unreadable record was not kept: %v", err)
	}
}

// ...and it must still succeed when there is genuinely nothing left, or the
// check above would be satisfied by a command that always fails.
func TestResettingAllSucceedsWhenEveryRecordWasCleared(t *testing.T) {
	nvxHome := filepath.Join(tempDir(t), ".nvx")
	dir := grantsDir(nvxHome)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	readable := filepath.Join(dir, "0123456789abcdef.json")
	if err := os.WriteFile(readable, []byte(`{"project_path":"C:\\p"}`), 0644); err != nil {
		t.Fatal(err)
	}

	if code := runGrants([]string{"reset", "--all"}, nvxHome); code != 0 {
		t.Fatalf("exit code = %d, want 0: nothing was left behind", code)
	}
	if _, err := os.Stat(readable); !os.IsNotExist(err) {
		t.Fatalf("the record was not removed, stat err=%v", err)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// `nvx grants reset` must not print a tick and exit 0 when it could not withdraw
// a permission because the directory moved.
//
// An access-control entry follows a renamed directory, so the permission is still
// in force under the new name -- and the reset deletes the record, which is the
// only thing that knew the identity and the original path. An acceptance pass did
// exactly this: renamed a granted directory, ran reset, got
// "✔ Reset grants for this project." at exit 0, and found the entry still on disk.
//
// The record is still dropped and the reset still finishes, so running it again is
// clean; what changes is that this run reports what it could not account for.
func TestResetReportsAPermissionItCouldNotWithdraw(t *testing.T) {
	tmp := tempDir(t)
	projectDir := filepath.Join(tmp, "project")
	nvxHome := filepath.Join(tmp, ".nvx")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nvxHome, 0o755); err != nil {
		t.Fatal(err)
	}

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWd) }()
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	scope := projectScopeDir()
	g := loadProjectGrants(nvxHome, scope)
	g.ProjectPath = scope
	// A recorded grant on a directory that is not there. Never created, which is
	// the same state the ledger is in after the directory is renamed away.
	g.ReadExecGrants = append(g.ReadExecGrants, readExecGrant{
		Path: filepath.Join(tmp, "granted-then-renamed"),
		SID:  "S-1-15-3-1024-deadbeef",
	})
	if err := saveProjectGrants(nvxHome, g); err != nil {
		t.Fatal(err)
	}

	if code := runGrants([]string{"reset"}, nvxHome); code == 0 {
		t.Fatal("reset exited 0 while a recorded permission could not be withdrawn; " +
			"a cleanup script cannot tell this from a clean reset")
	}

	// It still has to have finished: the record is gone, so a second run is clean
	// rather than the command being stuck reporting this for ever.
	if _, err := os.Stat(grantsPath(nvxHome, scope)); !os.IsNotExist(err) {
		t.Fatalf("the grant record survived the reset (err=%v); the reset did not complete", err)
	}
	if code := runGrants([]string{"reset"}, nvxHome); code != 0 {
		t.Fatalf("a second reset returned %d; the command is stuck rather than finished", code)
	}
}
