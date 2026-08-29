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
