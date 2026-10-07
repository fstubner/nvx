//go:build windows

package nvx

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

// A directory whose grant overran is still where the command starts. It used to
// be reported unusable, and the command started in the sandbox's home, where
// `npm create vite@latest myvite` wrote its project and lost it at exit while
// printing "Done". The entry for the folder itself and for what is created in
// it costs one folder's write, so it is made at once rather than waited for.
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
	if !usable {
		t.Fatal("a directory whose grant overran was reported unusable, so the command would start in the " +
			"sandbox's home and anything it wrote there would be deleted at exit")
	}
	requireNewChildrenEntry(t, workDir, sid)

	// The overrun is remembered, and the next launch neither pays the bound
	// again nor loses the directory.
	start = time.Now()
	usable = grantNonProjectWorkdir(nvxHome, sid, "", workDir)
	if again := time.Since(start); again > ancestorGrantPerPath/2 {
		t.Fatalf("second launch waited %v for a directory already recorded as slow", again)
	}
	if !usable {
		t.Fatal("a directory recorded as slow was reported unusable on the next launch")
	}
}

// The entry for new children reaches what is created after it and nothing that
// was already there. Another project inside the same folder stays out of reach,
// and a project the command creates is its own.
func TestTheEntryForNewChildrenLeavesWhatIsAlreadyThere(t *testing.T) {
	dir := tempDir(t)
	existing := filepath.Join(dir, "existing")
	if err := os.MkdirAll(existing, 0o700); err != nil {
		t.Fatal(err)
	}
	sid, err := scopeCapabilitySID(dir)
	if err != nil {
		t.Skipf("cannot derive a capability SID here: %v", err)
	}
	t.Cleanup(func() { _ = revokeACL(dir, sid) })

	if err := writeEntryForNewChildren(dir, sid, aclMaskModify); err != nil {
		t.Fatalf("writeEntryForNewChildren: %v", err)
	}
	requireNewChildrenEntry(t, dir, sid)
	if e, ok := anyEntryFor(t, existing, sid); ok {
		t.Errorf("a folder that was already there received the entry: %+v", e)
	}

	created := filepath.Join(dir, "myvite")
	if err := os.Mkdir(created, 0o700); err != nil {
		t.Fatal(err)
	}
	e, ok := anyEntryFor(t, created, sid)
	if !ok || !e.Inherited || !e.grantsAtLeast(aclMaskModify) {
		t.Errorf("a folder created afterwards did not inherit modify access: %+v (found %v)", e, ok)
	}

	// The same entry again is no change, and is not a second walk.
	if err := writeEntryForNewChildren(dir, sid, aclMaskModify); err != nil {
		t.Errorf("writing the same entry again failed: %v", err)
	}
	// A different entry already there is refused, never replaced without the
	// walk that would correct its copies.
	other := tempDir(t)
	if err := writeDACLEntry(other, sid, aclMaskReadExec, nvxInheritFlags); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = revokeACL(other, sid) })
	if err := writeEntryForNewChildren(other, sid, aclMaskModify); err == nil {
		t.Error("an inheritable read/execute entry was replaced without the walk")
	}
}

// requireNewChildrenEntry fails unless path carries sid's own modify entry,
// inherited by files and folders created in it.
func requireNewChildrenEntry(t *testing.T, path, sid string) {
	t.Helper()
	e, ok, err := aclEntryFor(path, sid)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || e.Deny || e.Mask != aclMaskModify || e.Flags&^inheritedACE != nvxInheritFlags {
		t.Fatalf("%s does not carry the entry for new children: %+v (found %v)", path, e, ok)
	}
}

// anyEntryFor returns sid's entry on path, explicit or inherited.
func anyEntryFor(t *testing.T, path, sid string) (aclEntry, bool) {
	t.Helper()
	entries, err := readDACL(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if sidsEqual(e.SID, sid) {
			return e, true
		}
	}
	return aclEntry{}, false
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
