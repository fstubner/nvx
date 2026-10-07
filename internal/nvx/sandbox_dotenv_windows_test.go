//go:build windows

package nvx

import (
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

// testACE builds an access-control entry as Windows stores it.
func testACE(t *testing.T, aceType, flags byte, mask uint32, sidStr string) []byte {
	t.Helper()
	sid, err := sidFromString(sidStr)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.LocalFree(syscall.Handle(unsafe.Pointer(sid)))
	n, _, _ := procGetLengthSid.Call(uintptr(unsafe.Pointer(sid)))
	ace := make([]byte, 8+int(n))
	ace[0], ace[1] = aceType, flags
	binary.LittleEndian.PutUint16(ace[2:], uint16(len(ace)))
	binary.LittleEndian.PutUint32(ace[4:], mask)
	copy(ace[8:], unsafe.Slice(sid, int(n)))
	return ace
}

const (
	testUserSID = "S-1-5-21-1004336348-1177238915-682003330-1001"
	testCapSID  = "S-1-15-3-1024-1065365936-1281604716-3511738428-1654721687-432734479-3232135806-4053264122-3456934681"
	testPkgSID  = "S-1-15-2-2-3-4-5-6-7-8"
)

// The filter itself: which entries a protected .env keeps.
func TestDotenvKeptACEsDropsOnlySandboxAllows(t *testing.T) {
	in := [][]byte{
		testACE(t, accessDeniedAceType, 0, aclMaskReadExec, "S-1-15-2-1"),               // deny for a sandbox identity: kept
		testACE(t, accessAllowedAceType, 0, aclMaskModify, testUserSID),                 // the user: kept
		testACE(t, accessAllowedAceType, inheritedACE, aclMaskModify, testCapSID),       // the project capability: dropped
		testACE(t, accessDeniedAceType, inheritedACE, fileWriteData, testUserSID),       // inherited deny: kept, after the user's allow
		testACE(t, accessAllowedAceType, inheritedACE, 0x1F01FF, "S-1-5-18"),            // SYSTEM: kept
		testACE(t, accessAllowedAceType, inheritedACE, 0x1F01FF, "S-1-5-32-544"),        // Administrators: kept
		testACE(t, accessAllowedAceType, inheritedACE, aclMaskReadExec, "S-1-15-2-1"),   // ALL APPLICATION PACKAGES: dropped
		testACE(t, accessAllowedAceType, inheritedACE, aclMaskReadExec, testPkgSID),     // a package SID: dropped
		testACE(t, accessAllowedCallbackAceType, 0, aclMaskReadExec, testCapSID),        // conditional allow for a capability: dropped
		testACE(t, accessAllowedAceType, inheritedACE, aclMaskReadExec, "S-1-15-21-5"),  // not S-1-15-2-*: kept
		testACE(t, accessAllowedAceType, inheritedACE, aclMaskReadExec, "S-1-5-11"),     // Authenticated Users: kept
		testACE(t, accessAllowedAceType, inheritedACE, aclMaskReadExec, "S-1-15-3-1"),   // a well-known capability: dropped
		testACE(t, accessAllowedAceType, inheritedACE, aclMaskReadExec, "S-1-3-4"),      // OWNER RIGHTS: kept
		testACE(t, accessAllowedAceType, inheritedACE, aclMaskReadExec, "S-1-15-1-123"), // not a package or capability: kept
	}
	keptIdx := []int{0, 1, 3, 4, 5, 9, 10, 12, 13}
	got := dotenvKeptACEs(in)
	if len(got) != len(keptIdx) {
		t.Fatalf("kept %d entries, want %d", len(got), len(keptIdx))
	}
	for i, j := range keptIdx {
		want := append([]byte{}, in[j]...)
		want[1] &^= inheritedACE
		if string(got[i]) != string(want) {
			t.Errorf("entry %d: got % x, want entry %d marked explicit (% x)", i, got[i], j, want)
		}
	}
	for _, a := range got {
		if a[1]&inheritedACE != 0 {
			t.Errorf("an entry kept its inherited mark: % x", a)
		}
	}
}

func TestDotenvNeedsProtection(t *testing.T) {
	user := testACE(t, accessAllowedAceType, 0, aclMaskModify, testUserSID)
	capAllow := testACE(t, accessAllowedAceType, 0, aclMaskModify, testCapSID)
	aapDeny := testACE(t, accessDeniedAceType, 0, aclMaskModify, "S-1-15-2-1")
	cases := []struct {
		name      string
		protected bool
		aces      [][]byte
		want      bool
	}{
		{"inherits, no sandbox entry", false, [][]byte{user}, true},
		{"protected, sandbox allow", true, [][]byte{user, capAllow}, true},
		{"protected, user only", true, [][]byte{user}, false},
		{"protected, sandbox deny only", true, [][]byte{aapDeny, user}, false},
	}
	for _, c := range cases {
		if got := dotenvNeedsProtection(c.protected, c.aces); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func dotenvTestSDDL(t *testing.T, path string) string {
	t.Helper()
	root, err := finalPathOf(filepath.VolumeName(path) + `\`)
	if err != nil {
		t.Fatal(err)
	}
	h, _, err := openDotenvWithin(root, path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer syscall.CloseHandle(h)
	acl, err := readDotenvACL(h)
	if err != nil {
		t.Fatal(err)
	}
	return acl.sddl
}

// TestHideDotenvFromSandboxProtectsAndRestores runs the launch step and the
// reset against real files, granted the way a launch grants a project.
func TestHideDotenvFromSandboxProtectsAndRestores(t *testing.T) {
	nvxHome := tempDir(t)
	project := tempDir(t)
	outside := tempDir(t)
	capSID, err := scopeCapabilitySID(project)
	if err != nil {
		t.Fatal(err)
	}
	write := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	env := filepath.Join(project, ".env")
	local := filepath.Join(project, "sub", ".env.local")
	example := filepath.Join(project, ".env.example")
	pkg := filepath.Join(project, "package.json")
	dep := filepath.Join(project, "node_modules", "dep", ".env")
	away := filepath.Join(outside, ".env")
	write(env, "API_KEY=1")
	write(local, "API_KEY=2")
	write(example, "API_KEY=")
	write(pkg, "{}")
	write(dep, "X=1")
	write(away, "API_KEY=outside")
	// A junction inside the project to a folder outside it.
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(project, "linked"), outside).CombinedOutput(); err != nil {
		t.Fatalf("mklink: %v %s", err, out)
	}
	if err := grantSandboxModify(capSID, project); err != nil {
		t.Fatal(err)
	}

	before := map[string]string{}
	for _, p := range []string{env, local, example, pkg, dep, away} {
		before[p] = dotenvTestSDDL(t, p)
		if !strings.Contains(before[p], capSID) && p != away {
			t.Fatalf("%s does not carry the project grant to begin with: %s", p, before[p])
		}
	}

	unchanged := dotenvChangeTime(t, env)
	hideDotenvFromSandbox(nvxHome, project)
	if dotenvChangeTime(t, env) == unchanged {
		t.Fatalf("protecting %s did not move its change time, so the second-launch check below proves nothing", env)
	}

	for _, p := range []string{env, local} {
		after := dotenvTestSDDL(t, p)
		entries, err := readDACL(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if strings.HasPrefix(e.SID, "S-1-15-2-") || strings.HasPrefix(e.SID, "S-1-15-3-") {
				t.Errorf("%s still has an entry for %s", p, e.SID)
			}
		}
		if !strings.HasPrefix(after, "D:P") {
			t.Errorf("%s is not protected: %s", p, after)
		}
		if b, err := os.ReadFile(p); err != nil || !strings.HasPrefix(string(b), "API_KEY=") {
			t.Errorf("the user cannot read %s: %v", p, err)
		}
		if err := os.WriteFile(p, []byte("API_KEY=edited"), 0o600); err != nil {
			t.Errorf("the user cannot edit %s: %v", p, err)
		}
	}
	for _, p := range []string{example, pkg, dep, away} {
		if got := dotenvTestSDDL(t, p); got != before[p] {
			t.Errorf("%s was changed: %s -> %s", p, before[p], got)
		}
	}
	g := loadProjectGrants(nvxHome, project)
	if len(g.ProtectedDotenv) != 2 {
		t.Fatalf("recorded %d files, want 2: %+v", len(g.ProtectedDotenv), g.ProtectedDotenv)
	}
	for _, r := range g.ProtectedDotenv {
		if r.SDDL != before[r.Path] {
			t.Errorf("record for %s holds %s, want the permissions from before, %s", r.Path, r.SDDL, before[r.Path])
		}
	}

	// A second launch reads and writes nothing: the files' change times and
	// the record stay as they were.
	ledger := grantsPath(nvxHome, project)
	ledgerInfo, err := os.Stat(ledger)
	if err != nil {
		t.Fatal(err)
	}
	changed := dotenvChangeTime(t, env)
	hideDotenvFromSandbox(nvxHome, project)
	if got := dotenvChangeTime(t, env); got != changed {
		t.Errorf("a second launch wrote %s's permissions again", env)
	}
	if info, err := os.Stat(ledger); err != nil || !info.ModTime().Equal(ledgerInfo.ModTime()) {
		t.Errorf("a second launch rewrote the grant record")
	}

	// An editor that saves by replacing the file brings the inherited entries
	// back. The next launch protects the new file again.
	if err := os.WriteFile(env+".tmp", []byte("API_KEY=replaced"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(env+".tmp", env); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dotenvTestSDDL(t, env), capSID) {
		t.Fatalf("the replaced file does not inherit the project grant, so this checks nothing")
	}
	hideDotenvFromSandbox(nvxHome, project)
	if got := dotenvTestSDDL(t, env); strings.Contains(got, capSID) || !strings.HasPrefix(got, "D:P") {
		t.Errorf("the replaced .env was not protected again: %s", got)
	}

	// A sandbox entry added to the protected file is taken out again, and the
	// record keeps the permissions from before nvx rather than nvx's own list.
	if err := grantACL(env, capSID, aclMaskReadExec, 0); err != nil {
		t.Fatal(err)
	}
	hideDotenvFromSandbox(nvxHome, project)
	if got := dotenvTestSDDL(t, env); strings.Contains(got, capSID) {
		t.Errorf("the added sandbox entry is still there: %s", got)
	}
	for _, r := range loadProjectGrants(nvxHome, project).ProtectedDotenv {
		if sameGrantPath(r.Path, env) && r.SDDL != before[env] {
			t.Errorf("the record for .env now holds %s, want %s", r.SDDL, before[env])
		}
	}

	// `nvx grants reset` puts the permissions back and drops the record.
	prevDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(prevDir)
	if code := runGrants([]string{"reset"}, nvxHome); code != 0 {
		t.Fatalf("grants reset exited %d", code)
	}
	for _, p := range []string{env, local} {
		if got := dotenvTestSDDL(t, p); got != before[p] {
			t.Errorf("%s restored to\n  %s\nwant\n  %s", p, got, before[p])
		}
	}
	if _, err := os.Stat(ledger); !os.IsNotExist(err) {
		t.Errorf("grants reset left the record in place: %v", err)
	}
}

var procGetFileInformationByHandleExTest = modKernel32.NewProc("GetFileInformationByHandleEx")

// dotenvChangeTime returns the time a file's metadata, its permissions
// included, last changed.
func dotenvChangeTime(t *testing.T, path string) int64 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var info struct {
		CreationTime, LastAccessTime, LastWriteTime, ChangeTime int64
		FileAttributes                                          uint32
		_                                                       uint32
	}
	const fileBasicInfo = 0
	if r, _, e := procGetFileInformationByHandleExTest.Call(f.Fd(), fileBasicInfo,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info)); r == 0 {
		t.Fatal(e)
	}
	return info.ChangeTime
}

// A file nvx may not change does not stop the others or the launch, and is
// not recorded.
func TestHideDotenvFromSandboxCarriesOnPastAFileItCannotChange(t *testing.T) {
	nvxHome := tempDir(t)
	project := tempDir(t)
	capSID, err := scopeCapabilitySID(project)
	if err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(project, ".env")
	open := filepath.Join(project, ".env.local")
	for _, p := range []string{locked, open} {
		if err := os.WriteFile(p, []byte("API_KEY=1"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := grantSandboxModify(capSID, project); err != nil {
		t.Fatal(err)
	}
	// The user and OWNER RIGHTS may read, and nobody this process speaks for
	// may change the permissions: OWNER RIGHTS replaces the owner's implicit
	// right to. The sandbox has read, so the file needs protecting.
	user, err := currentUserSIDString()
	if err != nil {
		t.Fatal(err)
	}
	sddl := "D:P(A;;0x1200a9;;;OW)(A;;0x1200a9;;;" + user + ")(A;;FA;;;SY)(A;;0x1200a9;;;" + capSID + ")"
	p, _ := syscall.UTF16PtrFromString(sddl)
	var sd *byte
	if ret, _, e := procConvertStringSDToSDDotenv.Call(uintptr(unsafe.Pointer(p)), 1, uintptr(unsafe.Pointer(&sd)), 0); ret == 0 {
		t.Fatal(e)
	}
	defer syscall.LocalFree(syscall.Handle(unsafe.Pointer(sd)))
	path, _ := syscall.UTF16PtrFromString(locked)
	if ret, _, e := procSetFileSecurityW.Call(uintptr(unsafe.Pointer(path)), daclSecurityInformation, uintptr(unsafe.Pointer(sd))); ret == 0 {
		t.Fatal(e)
	}
	before := dotenvTestSDDL(t, locked)
	if !strings.Contains(before, capSID) {
		t.Fatalf("the locked file does not admit the sandbox, so this checks nothing: %s", before)
	}

	hideDotenvFromSandbox(nvxHome, project)

	if got := dotenvTestSDDL(t, locked); got != before {
		t.Errorf("the file nvx may not change was changed: %s -> %s", before, got)
	}
	if got := dotenvTestSDDL(t, open); strings.Contains(got, capSID) {
		t.Errorf("the other .env was left readable to the sandbox: %s", got)
	}
	g := loadProjectGrants(nvxHome, project)
	if len(g.ProtectedDotenv) != 1 || !sameGrantPath(g.ProtectedDotenv[0].Path, open) {
		t.Errorf("recorded %+v, want only %s", g.ProtectedDotenv, open)
	}
}

// lowerDotenvCap sets maxProtectedDotenv for one test.
func lowerDotenvCap(t *testing.T, n int) {
	t.Helper()
	prev := maxProtectedDotenv
	maxProtectedDotenv = n
	t.Cleanup(func() { maxProtectedDotenv = prev })
}

// Past the cap, files are left as they are and the caller is told. A file that
// already has a record still gets its protection back after an editor replaces
// it, and one without a record does not.
func TestProtectDotenvFilesStopsAtTheCap(t *testing.T) {
	lowerDotenvCap(t, 5)
	nvxHome := tempDir(t)
	project := tempDir(t)
	capSID, err := scopeCapabilitySID(project)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for i := 0; i < maxProtectedDotenv+3; i++ {
		p := filepath.Join(project, fmt.Sprintf(".env.%02d", i))
		if err := os.WriteFile(p, []byte("API_KEY=1"), 0o600); err != nil {
			t.Fatal(err)
		}
		files = append(files, p)
	}
	if err := grantSandboxModify(capSID, project); err != nil {
		t.Fatal(err)
	}
	root, err := finalPathOf(project)
	if err != nil {
		t.Fatal(err)
	}
	protected := func() (n int) {
		for _, p := range files {
			if dotenvIsProtected(t, root, p) {
				n++
			}
		}
		return n
	}

	if !protectDotenvFiles(nvxHome, project, files) {
		t.Errorf("protecting %d files with a cap of %d did not say it stopped", len(files), maxProtectedDotenv)
	}
	if n := protected(); n != maxProtectedDotenv {
		t.Errorf("%d files are protected, want the cap, %d", n, maxProtectedDotenv)
	}
	if n := len(loadProjectGrants(nvxHome, project).ProtectedDotenv); n != maxProtectedDotenv {
		t.Errorf("the record holds %d files, want the cap, %d", n, maxProtectedDotenv)
	}

	// files[0] has a record. files[maxProtectedDotenv] was left out.
	replaced, left := files[0], files[maxProtectedDotenv]
	if err := os.WriteFile(replaced+".tmp", []byte("API_KEY=2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replaced+".tmp", replaced); err != nil {
		t.Fatal(err)
	}
	if dotenvIsProtected(t, root, replaced) {
		t.Fatalf("the replaced file is still protected, so this checks nothing")
	}
	if !protectDotenvFiles(nvxHome, project, []string{replaced, left}) {
		t.Errorf("a file past the cap was not reported")
	}
	if !dotenvIsProtected(t, root, replaced) {
		t.Errorf("a file with a record was not protected again after it was replaced")
	}
	if dotenvIsProtected(t, root, left) {
		t.Errorf("a file past the cap was protected")
	}
	if n := len(loadProjectGrants(nvxHome, project).ProtectedDotenv); n != maxProtectedDotenv {
		t.Errorf("the record holds %d files after the replace, want the cap, %d", n, maxProtectedDotenv)
	}
}

// A .env with more than one name is one file, so it is hidden through every
// name. That is what a .env shared between two git worktrees by a hard link
// needs. A contained process cannot use this to aim nvx at a file it may not
// change, see TestContainedProcessCannotLinkDotenvToAFileItCannotWrite.
func TestHideDotenvFromSandboxHidesAHardLinkedFileThroughEveryName(t *testing.T) {
	nvxHome := tempDir(t)
	project := tempDir(t)
	otherWorktree := tempDir(t)
	capSID, err := scopeCapabilitySID(project)
	if err != nil {
		t.Fatal(err)
	}
	env := filepath.Join(project, ".env")
	shared := filepath.Join(otherWorktree, ".env")
	if err := os.WriteFile(env, []byte("API_KEY=1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(env, shared); err != nil {
		t.Fatal(err)
	}
	if err := grantSandboxModify(capSID, project); err != nil {
		t.Fatal(err)
	}
	sandboxEntries := func(p string) (n int) {
		entries, err := readDACL(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if strings.HasPrefix(e.SID, "S-1-15-2-") || strings.HasPrefix(e.SID, "S-1-15-3-") {
				n++
			}
		}
		return n
	}
	if sandboxEntries(shared) == 0 {
		t.Fatalf("the shared name does not carry the project grant to begin with, so this checks nothing")
	}

	hideDotenvFromSandbox(nvxHome, project)

	for _, p := range []string{env, shared} {
		if n := sandboxEntries(p); n != 0 {
			t.Errorf("%s still has %d entries for sandbox identities", p, n)
		}
		if b, err := os.ReadFile(p); err != nil || string(b) != "API_KEY=1" {
			t.Errorf("the user cannot read %s: %q %v", p, b, err)
		}
	}
	if g := loadProjectGrants(nvxHome, project); len(g.ProtectedDotenv) != 1 || !sameGrantPath(g.ProtectedDotenv[0].Path, env) {
		t.Errorf("recorded %+v, want only %s", g.ProtectedDotenv, env)
	}
}
