//go:build windows

package nvx

import (
	"errors"
	"strings"
	"testing"
)

// A folder an older nvx left taking its parent's permissions is protected again
// by setup, with no command typed by hand. The write is the injected one. The
// real C:\Users is never touched from a test.
func TestSetupRestoresTheProtectionOnAnUnprotectedUsersFolder(t *testing.T) {
	f := newFakeSetupOps(true, nothingLeft)
	f.ops.unprotected = func() []unprotectedDir { return []unprotectedDir{{Dir: `C:\Users`, Safe: true}} }
	code, out := runCleanup(t, tempDir(t), f.ops)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if len(f.restored) != 1 || f.restored[0] != `C:\Users` {
		t.Fatalf("restored %v, want [C:\\Users]", f.restored)
	}
	if strings.Contains(out, "Nothing to remove") {
		t.Errorf("setup fixed something and still said there was nothing to do:\n%s", out)
	}
}

// Protection is restored only where the folder's own entries keep SYSTEM and
// Administrators in. Otherwise setup refuses, says why, fails, and writes nothing.
func TestSetupRefusesToRestoreProtectionThatWouldLockPeopleOut(t *testing.T) {
	f := newFakeSetupOps(true, nothingLeft)
	f.ops.unprotected = func() []unprotectedDir { return []unprotectedDir{{Dir: `C:\Users`, Safe: false}} }
	code, out := runCleanup(t, tempDir(t), f.ops)
	if code == 0 {
		t.Fatalf("exit 0 with a folder still open to other accounts:\n%s", out)
	}
	if len(f.restored) != 0 {
		t.Errorf("setup removed inherited entries from %v although its own entries would not keep SYSTEM and Administrators in", f.restored)
	}
	if !strings.Contains(out, "would not keep") || !strings.Contains(out, `C:\Users`) {
		t.Errorf("setup did not say why it left the folder alone:\n%s", out)
	}
}

// A safe folder and an unsafe one together. The safe one is still fixed and the
// run still fails for the other.
func TestSetupRestoresTheSafeFolderEvenWhenAnotherIsRefused(t *testing.T) {
	f := newFakeSetupOps(true, nothingLeft)
	f.ops.unprotected = func() []unprotectedDir {
		return []unprotectedDir{{Dir: `C:\Users`, Safe: true}, {Dir: `C:\Users\someone`, Safe: false}}
	}
	if code, _ := runCleanup(t, tempDir(t), f.ops); code == 0 {
		t.Fatal("exit 0 although one folder was refused")
	}
	if len(f.restored) != 1 || f.restored[0] != `C:\Users` {
		t.Fatalf("restored %v, want only C:\\Users", f.restored)
	}
}

// Unelevated, setup lists the folder among what it would fix and writes nothing.
func TestSetupListsAnUnprotectedFolderWithoutElevation(t *testing.T) {
	f := newFakeSetupOps(false, nothingLeft)
	f.ops.unprotected = func() []unprotectedDir { return []unprotectedDir{{Dir: `C:\Users`, Safe: true}} }
	code, out := runCleanup(t, tempDir(t), f.ops)
	if code == 0 || !strings.Contains(out, `C:\Users`) || !strings.Contains(out, "Administrator") {
		t.Fatalf("exit %d, and the folder was not named with the need for an Administrator terminal:\n%s", code, out)
	}
	if len(f.restored) != 0 {
		t.Errorf("setup wrote without elevation: %v", f.restored)
	}
}

func TestSetupFailsWhenProtectionCouldNotBeRestored(t *testing.T) {
	f := newFakeSetupOps(true, nothingLeft)
	f.ops.unprotected = func() []unprotectedDir { return []unprotectedDir{{Dir: `C:\Users`, Safe: true}} }
	f.ops.restoreProtection = func(string) error { return errors.New("access denied") }
	if code, _ := runCleanup(t, tempDir(t), f.ops); code == 0 {
		t.Fatal("exit 0 although the protection was not restored")
	}
}

// C:\Users has no owner to name, so the check is SYSTEM and Administrators with
// full control in the folder's own entries. Inherited entries do not count,
// since they are what the repair removes.
func TestUsersFolderProtectionNeedsSystemAndAdministrators(t *testing.T) {
	full := func(sid string) aclEntry { return aclEntry{SID: sid, Mask: fileAllAccess} }
	const system, admins = "S-1-5-18", "S-1-5-32-544"

	if !protectionIsSafeToRestore([]aclEntry{full(system), full(admins), {SID: "S-1-1-0", Mask: 0x1200a9}}, "") {
		t.Error(`the stock C:\Users entries were judged unsafe`)
	}
	if protectionIsSafeToRestore([]aclEntry{full(system)}, "") {
		t.Error("Administrators missing, and the repair was still allowed")
	}
	if protectionIsSafeToRestore([]aclEntry{full(admins)}, "") {
		t.Error("SYSTEM missing, and the repair was still allowed")
	}
	if protectionIsSafeToRestore([]aclEntry{full(system), {SID: admins, Mask: fileAllAccess, Inherited: true}}, "") {
		t.Error("Administrators' access was inherited, so it goes with the repair, and the repair was still allowed")
	}
	if protectionIsSafeToRestore([]aclEntry{full(system), {SID: admins, Mask: 0x1200a9}}, "") {
		t.Error("Administrators held read and execute only, and the repair was still allowed")
	}
}
