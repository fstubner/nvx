package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestToolHomeKeyIsStableAndScoped(t *testing.T) {
	a := toolHomeKey(`/home/u/projA`, "wrangler")
	if a != toolHomeKey(`/home/u/projA`, "wrangler") {
		t.Fatal("toolHomeKey must be stable for the same (scope, tool)")
	}
	if a == toolHomeKey(`/home/u/projB`, "wrangler") {
		t.Fatal("different project scope must yield a different key")
	}
	if a == toolHomeKey(`/home/u/projA`, "gh") {
		t.Fatal("different tool must yield a different key")
	}
	if len(a) == 0 {
		t.Fatal("key must be non-empty")
	}
}

func TestEnsurePersistentGuestProfileCreatesAndReuses(t *testing.T) {
	nvxHome := t.TempDir()
	scope := filepath.Join(t.TempDir(), "project")

	p1, err := ensurePersistentGuestProfile(nvxHome, scope, "wrangler")
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	for _, sub := range []string{"tmp", ".config", ".cache"} {
		if _, err := os.Stat(filepath.Join(p1, sub)); err != nil {
			t.Fatalf("expected skeleton dir %s: %v", sub, err)
		}
	}
	if filepath.Dir(filepath.Dir(p1)) != nvxHome {
		t.Fatalf("persistent profile should be two levels under nvxHome, got %s", p1)
	}
	if filepath.Base(filepath.Dir(p1)) != "tool_home" {
		t.Fatalf("persistent profile should live under tool_home, got %s", p1)
	}

	if err := os.WriteFile(filepath.Join(p1, "marker"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	p2, err := ensurePersistentGuestProfile(nvxHome, scope, "wrangler")
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if p2 != p1 {
		t.Fatalf("reuse: got %s, want %s", p2, p1)
	}
	if _, err := os.Stat(filepath.Join(p2, "marker")); err != nil {
		t.Fatalf("persistent profile must preserve state across calls: %v", err)
	}
}
