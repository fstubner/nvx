//go:build windows

package nvx

import (
	"os"
	"path/filepath"
	"testing"
)

// A directory is found by its ID wherever it went, and is found gone once it is
// deleted. Real folders on a real volume.
func TestADirectoryIsFoundByItsIDAfterARenameAndAMove(t *testing.T) {
	base := tempDir(t)
	dir := filepath.Join(base, "granted")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	id := directoryIdentity(dir)
	if id == "" {
		t.Skip("this volume gives directories no ID")
	}
	g := readExecGrant{Path: dir, SID: "S-1-15-3-1024-a", ID: id}

	if where, loc := locateGrantedDirectory(g); loc != locationHere || !sameGrantPath(where, dir) {
		t.Fatalf("a directory that has not moved is at %q, %v", where, loc)
	}
	renamed := filepath.Join(base, "renamed")
	if err := os.Rename(dir, renamed); err != nil {
		t.Fatal(err)
	}
	if where, loc := locateGrantedDirectory(g); loc != locationHere || !sameGrantPath(where, renamed) {
		t.Fatalf("after a rename it is at %q, %v; want %s", where, loc, renamed)
	}
	if err := os.Mkdir(filepath.Join(base, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(base, "sub", "moved")
	if err := os.Rename(renamed, moved); err != nil {
		t.Fatal(err)
	}
	if where, loc := locateGrantedDirectory(g); loc != locationHere || !sameGrantPath(where, moved) {
		t.Fatalf("after a move it is at %q, %v; want %s", where, loc, moved)
	}

	if err := os.Remove(moved); err != nil {
		t.Fatal(err)
	}
	if where, loc := locateGrantedDirectory(g); loc != locationGone {
		t.Fatalf("after the delete it is at %q, %v; want gone", where, loc)
	}
	// A new directory at the old path is not the old one.
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if where, loc := locateGrantedDirectory(g); loc != locationGone {
		t.Fatalf("a directory recreated at the old path was taken for the old one: %q, %v", where, loc)
	}
}

// What it cannot answer, it does not guess.
func TestAnIDThatCannotBeFollowedIsUnknownNotGone(t *testing.T) {
	dir := tempDir(t)
	id := directoryIdentity(dir)
	if id == "" {
		t.Skip("this volume gives directories no ID")
	}
	fileID := id[len(id)-32:]
	for name, bad := range map[string]string{
		"empty":                 "",
		"no separator":          "abc",
		"not hex":               "zz:" + fileID,
		"short":                 id[:len(id)-2],
		"another disk's serial": "1:" + fileID,
	} {
		if where, loc := locateGrantedDirectory(readExecGrant{Path: dir, ID: bad}); loc != locationUnknown {
			t.Errorf("%s: %q, %v; want unknown", name, where, loc)
		}
	}
}

// Through the real permission and the real command, which is the layer the
// person who reported this used: grant a folder, delete it, `nvx grants reset
// --all`. It exited 1 and told them a permission could not be withdrawn.
func TestResetAllFinishesCleanWhenAGrantedFolderWasDeleted(t *testing.T) {
	f := newGrantedFolder(t)
	if err := os.RemoveAll(f.dir); err != nil {
		t.Fatal(err)
	}
	if code := runGrants([]string{"reset", "--all"}, f.home); code != 0 {
		t.Fatalf("reset --all exited %d for a folder that was deleted", code)
	}
	if _, err := os.Stat(grantsPath(f.home, f.scope)); !os.IsNotExist(err) {
		t.Errorf("the record survived the reset: %v", err)
	}
}

// ...and a folder that was renamed has its permission withdrawn at the new name,
// where before the reset could only say that it was still in force.
func TestResetAllWithdrawsFromAGrantedFolderThatWasRenamed(t *testing.T) {
	f := newGrantedFolder(t)
	renamed := f.dir + "-renamed"
	if err := os.Rename(f.dir, renamed); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = revokeSandboxReadExec(f.sid, renamed) })
	if !readExecEntryIsOurs(f.sid, renamed) {
		t.Fatal("test precondition: the entry did not travel with the rename")
	}
	if code := runGrants([]string{"reset", "--all"}, f.home); code != 0 {
		t.Fatalf("reset --all exited %d for a folder that was renamed", code)
	}
	if readExecEntryIsOurs(f.sid, renamed) {
		t.Error("the permission is still on the renamed folder after the reset said it was done")
	}
}

type grantedFolder struct {
	home, scope, dir, sid string
}

// newGrantedFolder grants a sandbox identity read and execute on a folder and
// records it the way a launch does, in a ledger under a scratch nvx home.
func newGrantedFolder(t *testing.T) grantedFolder {
	t.Helper()
	root := tempDir(t)
	project := filepath.Join(root, "project")
	dir := filepath.Join(root, "granted")
	home := filepath.Join(root, "home")
	for _, d := range []string{project, dir, home} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	inProjectDir(t, project)
	sid, err := scopeCapabilitySID(dir)
	if err != nil {
		t.Skipf("cannot derive a capability SID here: %v", err)
	}
	t.Cleanup(func() { _ = revokeSandboxReadExec(sid, dir) })
	if _, err := grantSandboxReadExec(sid, dir); err != nil {
		t.Skipf("cannot write an ACL in the test environment: %v", err)
	}
	scope := projectScopeDir()
	g := loadProjectGrants(home, scope)
	g.ProjectPath = scope
	g.ReadExecGrants = recordReadExecGrant(g.ReadExecGrants, sid, dir)
	if err := saveProjectGrants(home, g); err != nil {
		t.Fatal(err)
	}
	return grantedFolder{home: home, scope: scope, dir: dir, sid: sid}
}
