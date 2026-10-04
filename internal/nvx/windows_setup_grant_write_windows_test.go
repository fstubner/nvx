//go:build windows

package nvx

// `nvx setup` writes its drive-root entry without walking the volume.
//
// The grant and its removal went through SetNamedSecurityInfoW, which re-runs
// auto-inheritance over every descendant of the directory written. On a drive
// root that is the whole volume. Measured 2026-10-04: setup --all-drives took 1s
// on D:\ and 3s on E:\, and was still on F:\ after 33 minutes. The entry is
// this-folder-only, so no descendant can see it and the walk had nothing to do.

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"unsafe"
)

const setupWriteTestSID = "S-1-15-3-1024-1212121212-2323232323-3434343434-1414141414-2525252525-3636363636-1717171717"

// The propagating writer is the only one aclWriteFn stands in for, so a hook
// there that fails proves which writer setup used.
func TestSetupGrantAndUndoDoNotUseThePropagatingWrite(t *testing.T) {
	var propagating atomic.Int32
	hook := func(path, sidStr string, mask uint32, flags uint8) error {
		propagating.Add(1)
		return errors.New("the propagating write was used")
	}
	aclWriteFn.Store(&hook)
	t.Cleanup(func() { aclWriteFn.Store(nil) })

	dir := tempDir(t)
	if err := grantSidReadExecThisFolder(setupWriteTestSID, dir); err != nil {
		t.Fatalf("setup's grant: %v", err)
	}
	if n := propagating.Load(); n != 0 {
		t.Fatalf("setup's grant went through the propagating write %d time(s); on a drive root that walks the whole volume", n)
	}
	e, ok, err := aclEntryFor(dir, setupWriteTestSID)
	if err != nil || !ok {
		t.Fatalf("the grant left no entry (found=%v, err=%v)", ok, err)
	}
	if e.Flags != 0 || e.Mask != aclMaskReadExec {
		t.Errorf("the entry has flags %#x and mask %#x, want 0 and %#x", e.Flags, e.Mask, aclMaskReadExec)
	}

	if err := revokeSidGrant(setupWriteTestSID, dir); err != nil {
		t.Fatalf("setup --undo's revoke: %v", err)
	}
	if n := propagating.Load(); n != 0 {
		t.Fatalf("setup --undo went through the propagating write %d time(s)", n)
	}
	if _, ok, _ := aclEntryFor(dir, setupWriteTestSID); ok {
		t.Error("the revoke left the entry in place")
	}
}

// The write that skips the walk rebuilds the directory's list itself, so this
// checks what that could get wrong: every descendant's security descriptor is
// byte-identical afterwards, and the directory holds its old entries and
// control bits with exactly one entry added.
func TestSetupGrantLeavesEveryChildACLUnchanged(t *testing.T) {
	root := tempDir(t)
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			d := filepath.Join(root, "a"+string(rune('0'+i)), "b"+string(rune('0'+j)))
			if err := os.MkdirAll(d, 0o700); err != nil {
				t.Fatal(err)
			}
			for k := 0; k < 3; k++ {
				if err := os.WriteFile(filepath.Join(d, "f"+string(rune('0'+k))), []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	children := descendantDescriptors(t, root)
	rootBefore, err := readDACL(root)
	if err != nil {
		t.Fatal(err)
	}
	controlBefore := securityControl(t, root)

	if err := grantSidReadExecThisFolder(setupWriteTestSID, root); err != nil {
		t.Fatalf("setup's grant: %v", err)
	}
	t.Cleanup(func() { _ = revokeSidGrant(setupWriteTestSID, root) })

	for path, before := range children {
		after := securityDescriptorBytes(t, path)
		if !bytes.Equal(before, after) {
			t.Errorf("%s: security descriptor changed (%d bytes before, %d after)", path, len(before), len(after))
		}
	}

	rootAfter, err := readDACL(root)
	if err != nil {
		t.Fatal(err)
	}
	var added []aclEntry
	var rest []aclEntry
	for _, e := range rootAfter {
		if sidsEqual(e.SID, setupWriteTestSID) {
			added = append(added, e)
		} else {
			rest = append(rest, e)
		}
	}
	if len(added) != 1 || added[0].Flags != 0 || added[0].Mask != aclMaskReadExec || added[0].Inherited {
		t.Errorf("want exactly one non-inherited this-folder RX entry for the identity, got %+v", added)
	}
	if len(rest) != len(rootBefore) {
		t.Fatalf("the directory had %d entries before and %d other than the new one after", len(rootBefore), len(rest))
	}
	for i := range rest {
		if rest[i] != rootBefore[i] {
			t.Errorf("entry %d changed from %+v to %+v", i, rootBefore[i], rest[i])
		}
	}
	if got := securityControl(t, root); got != controlBefore {
		t.Errorf("the directory's control bits changed from %#04x to %#04x", controlBefore, got)
	}
}

var procGetSecurityDescriptorLength = modAdvapi32.NewProc("GetSecurityDescriptorLength")

const (
	ownerSecurityInformation = 0x1
	groupSecurityInformation = 0x2
)

func descendantDescriptors(t *testing.T, root string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != root {
			out[path] = securityDescriptorBytes(t, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// securityDescriptorBytes returns path's owner, group and DACL as Windows
// stores them, self-relative, so two reads compare byte for byte.
func securityDescriptorBytes(t *testing.T, path string) []byte {
	t.Helper()
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	var sd *byte
	rc, _, _ := procGetNamedSecurityInfoW.Call(uintptr(unsafe.Pointer(p)), seFileObject,
		ownerSecurityInformation|groupSecurityInformation|daclSecurityInformation,
		0, 0, 0, 0, uintptr(unsafe.Pointer(&sd)))
	if rc != 0 {
		t.Fatalf("read the security descriptor of %s: %v", path, syscall.Errno(rc))
	}
	defer syscall.LocalFree(syscall.Handle(unsafe.Pointer(sd)))
	n, _, _ := procGetSecurityDescriptorLength.Call(uintptr(unsafe.Pointer(sd)))
	return append([]byte(nil), unsafe.Slice(sd, int(n))...)
}

func securityControl(t *testing.T, path string) uint16 {
	t.Helper()
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	var sd *byte
	rc, _, _ := procGetNamedSecurityInfoW.Call(uintptr(unsafe.Pointer(p)), seFileObject,
		daclSecurityInformation, 0, 0, 0, 0, uintptr(unsafe.Pointer(&sd)))
	if rc != 0 {
		t.Fatalf("read the security descriptor of %s: %v", path, syscall.Errno(rc))
	}
	defer syscall.LocalFree(syscall.Handle(unsafe.Pointer(sd)))
	return daclControl(sd)
}
