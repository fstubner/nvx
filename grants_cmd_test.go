package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunGrantsListShowsCurrentProjectGrants(t *testing.T) {
	tmp := t.TempDir()
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
	tmp := t.TempDir()
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
