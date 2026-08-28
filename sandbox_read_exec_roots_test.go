package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Extra read/execute roots exist because a tool can keep the program it runs
// outside everything nvx grants. Playwright is the case: its browsers live in
// %LOCALAPPDATA%\ms-playwright, and a contained process could not list that
// directory at all — measured on Windows 2026-08-28, EPERM inside against 27
// entries outside, and LIST_OK inside once the root was granted.

func TestReadExecRootsResolveAndReject(t *testing.T) {
	real := tempDir(t)
	file := filepath.Join(tempDir(t), "not-a-dir.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := resolveReadExecRoots([]string{
		real,
		filepath.Join(real, "does-not-exist"), // dropped: nothing to grant
		file,                                  // dropped: a file, not a directory
		"",                                    // dropped: empty
		real,                                  // dropped: duplicate
	})
	if len(got) != 1 {
		t.Fatalf("expected only the one usable directory, got %v", got)
	}
	if !strings.EqualFold(got[0], real) {
		t.Errorf("got %q, want %q", got[0], real)
	}
}

// A missing entry must not take the launch down. A policy naming a tool that is
// not installed on this machine is ordinary — the command should still run, just
// without that grant.
func TestAMissingReadExecRootIsDroppedNotFatal(t *testing.T) {
	got := resolveReadExecRoots([]string{filepath.Join(tempDir(t), "nope")})
	if len(got) != 0 {
		t.Fatalf("expected the missing path to be dropped, got %v", got)
	}
}

// The policy file is shared across platforms, so both spellings have to work or
// the field is usable on one OS per project.
func TestReadExecRootsExpandVariables(t *testing.T) {
	dir := tempDir(t)
	t.Setenv("NVX_TEST_ROOT", dir)

	for _, spelling := range []string{"$NVX_TEST_ROOT", "${NVX_TEST_ROOT}", "%NVX_TEST_ROOT%"} {
		got := resolveReadExecRoots([]string{spelling})
		if len(got) != 1 || !strings.EqualFold(got[0], dir) {
			t.Errorf("%s expanded to %v, want [%s]", spelling, got, dir)
		}
	}

	// ~ resolves to the real home, which is what a policy author means by it —
	// not the sandbox's throwaway one.
	if home, err := os.UserHomeDir(); err == nil {
		got := resolveReadExecRoots([]string{"~"})
		if len(got) != 1 || !strings.EqualFold(got[0], filepath.Clean(home)) {
			t.Errorf("~ expanded to %v, want [%s]", got, home)
		}
	}
}

// Adding a root widens what contained code may execute, so a checked-in project
// file must not be able to do it without the developer approving — the same rule
// as an egress allowlist entry.
func TestAddingAReadExecRootCountsAsLoosening(t *testing.T) {
	before := DefaultPolicy()
	after := DefaultPolicy()
	after.Isolation.Filesystem.AllowReadExec = []string{"/some/tool"}

	if !policyLoosens(before, after) {
		t.Fatal("adding allow_read_exec must count as loosening; it lets contained code execute " +
			"something from outside every root nvx grants")
	}
	if policyLoosens(after, after) {
		t.Error("an unchanged list must not read as loosening")
	}
}

// A local policy ADDS to the global one rather than replacing it, matching
// allow_write and the host allowlists.
func TestLocalReadExecRootsAreAppendedToGlobal(t *testing.T) {
	global := DefaultPolicy()
	global.Isolation.Filesystem.AllowReadExec = []string{"/from/global"}
	local := Policy{}
	local.Isolation.Filesystem.AllowReadExec = []string{"/from/local"}

	merged := MergePolicies(global, local)
	got := merged.Isolation.Filesystem.AllowReadExec
	if len(got) != 2 || got[0] != "/from/global" || got[1] != "/from/local" {
		t.Fatalf("expected both roots in order, got %v", got)
	}
}

// The grant is read and execute. It must never widen writes, whatever else the
// policy says — a directory you launch a browser from is not one an install
// should be able to rewrite.
