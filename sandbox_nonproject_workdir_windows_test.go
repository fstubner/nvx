//go:build windows

package main

// A contained command run from a directory that is not a project must not
// wait for that directory's ACL write. Measured 2026-09-03: `npx -y cowsay hi`
// from %TEMP% and from H:\projects\private hung for over two minutes on the
// write-access grant for the working directory, propagating over hundreds of
// thousands of entries, before the command started. An MCP client launching
// `npx -y <server>` from a non-project directory reports a timeout instead of
// a server.

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// stallACLWrites makes every ACL write take `d`, and restores the real one.
func stallACLWrites(t *testing.T, d time.Duration) {
	t.Helper()
	fn := func(path, sidStr string, mask uint32, flags uint8) error {
		time.Sleep(d)
		return writeDACLEntry(path, sidStr, mask, flags)
	}
	aclWriteFn.Store(&fn)
	t.Cleanup(func() { aclWriteFn.Store(nil) })
}

func TestANonProjectWorkdirGrantIsBoundedAndRemembered(t *testing.T) {
	nvxHome := tempDir(t)
	workDir := tempDir(t) // no package.json anywhere above a temp dir
	if findProjectRoot(workDir) != "" {
		t.Skipf("%s sits under a package.json; the test needs a non-project directory", workDir)
	}
	sid, err := scopeCapabilitySID(workDir)
	if err != nil {
		t.Skipf("cannot derive a capability SID here: %v", err)
	}
	t.Cleanup(func() { _ = revokeACL(workDir, sid) })

	stall := ancestorGrantPerPath + 2*time.Second
	stallACLWrites(t, stall)

	start := time.Now()
	usable := grantNonProjectWorkdir(nvxHome, sid, "", workDir)
	took := time.Since(start)
	if took >= stall {
		t.Fatalf("a non-project working directory's grant was waited for in full (%v); the command behind it "+
			"is the one that hung for two minutes from %%TEMP%%", took)
	}
	if usable {
		t.Fatal("a directory whose grant overran was reported usable; the launch would die with \"chdir: Access is denied\"")
	}

	// The overrun is remembered: the next launch does not pay the bound again,
	// and still does not get the directory.
	start = time.Now()
	usable = grantNonProjectWorkdir(nvxHome, sid, "", workDir)
	if again := time.Since(start); again > ancestorGrantPerPath/2 {
		t.Fatalf("second launch waited %v for a directory already recorded as slow", again)
	}
	if usable {
		t.Fatal("a directory recorded as slow was reported usable on the next launch")
	}
}

// A small non-project directory is granted in time and used as it is.
func TestASmallNonProjectWorkdirIsGrantedAndUsed(t *testing.T) {
	nvxHome := tempDir(t)
	workDir := tempDir(t)
	if findProjectRoot(workDir) != "" {
		t.Skipf("%s sits under a package.json; the test needs a non-project directory", workDir)
	}
	sid, err := scopeCapabilitySID(workDir)
	if err != nil {
		t.Skipf("cannot derive a capability SID here: %v", err)
	}
	t.Cleanup(func() { _ = revokeACL(workDir, sid) })
	if !grantNonProjectWorkdir(nvxHome, sid, "", workDir) {
		t.Fatal("an empty directory was not granted within the bound")
	}
	if !appContainerHasGrantFor(sid, workDir, grantModify) {
		t.Fatal("the directory was reported usable but carries no write entry")
	}
}

// A project directory is never bounded: writing it is the point of an install,
// and abandoning that grant would fail the install a moment later with EPERM.
func TestAProjectWorkdirGrantIsWaitedFor(t *testing.T) {
	workDir := tempDir(t)
	if err := os.WriteFile(filepath.Join(workDir, "package.json"), []byte(`{"name":"p","version":"1.0.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sid, err := scopeCapabilitySID(workDir)
	if err != nil {
		t.Skipf("cannot derive a capability SID here: %v", err)
	}
	t.Cleanup(func() { _ = revokeACL(workDir, sid) })

	stall := ancestorGrantPerPath + 500*time.Millisecond
	stallACLWrites(t, stall)

	start := time.Now()
	if err := grantSandboxModify(sid, workDir); err != nil {
		t.Fatalf("grantSandboxModify: %v", err)
	}
	if took := time.Since(start); took < stall {
		t.Fatalf("a project directory's grant returned after %v, before the write finished (%v)", took, stall)
	}
	if !appContainerHasGrantFor(sid, workDir, grantModify) {
		t.Fatal("the project directory did not end up writable")
	}
}
