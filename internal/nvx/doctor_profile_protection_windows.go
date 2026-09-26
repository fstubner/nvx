//go:build windows

package nvx

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

// reportUnprotectedProfile reports a profile folder, or the folder above it,
// that has stopped being protected from its parent's permissions.
//
// Windows ships C:\Users and every profile protected, so neither takes C:\'s
// "Authenticated Users: Modify" for subfolders. Before 2026-09-26 every
// permission nvx wrote switched that protection off (see keepDACLProtection),
// and fixing the writer does not repair a machine it already changed: the
// development machine's whole profile was modifiable by every signed-in account.
// Doctor is where someone would find out, so it looks.
//
// It names the repair only when the folder's own entries would still let the
// owner in once the inherited ones are gone. Otherwise removing them could lock
// the user out, and the right step is to look first.
func reportUnprotectedProfile() bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	userSID := ""
	if u, uerr := user.Current(); uerr == nil {
		userSID = u.Uid
	}
	weakened := false
	for _, dir := range []string{filepath.Dir(home), home} {
		protected, err := pathDACLProtected(dir)
		if err != nil || protected {
			continue
		}
		entries, err := readDACL(dir)
		if err != nil {
			continue
		}
		owner := ""
		if dir == home {
			owner = userSID
		}
		weakened = true
		fmt.Printf("  [FAIL] %s takes its parent's permissions; Windows ships it protected from them\n", dir)
		fmt.Println("         an nvx version before this one could cause this, and it can let other accounts on this machine into the folder")
		if protectionIsSafeToRestore(entries, owner) {
			fmt.Printf("         to restore it, from an Administrator terminal: icacls \"%s\" /inheritance:r\n", dir)
		} else {
			fmt.Println("         its own entries would not keep you in if the inherited ones were removed; review its permissions before changing them")
		}
	}
	return weakened
}

// protectionIsSafeToRestore reports whether a folder's explicit entries grant
// SYSTEM, Administrators and, when named, the owner full control, so that
// removing every inherited entry cannot lock any of them out.
func protectionIsSafeToRestore(entries []aclEntry, ownerSID string) bool {
	need := []string{"S-1-5-18", "S-1-5-32-544"}
	if ownerSID != "" {
		need = append(need, ownerSID)
	}
	for _, sid := range need {
		found := false
		for _, e := range entries {
			if !e.Inherited && !e.Deny && strings.EqualFold(e.SID, sid) && e.Mask&fileAllAccess == fileAllAccess {
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

// fileAllAccess is FILE_ALL_ACCESS, what icacls shows as (F).
const fileAllAccess = 0x001F01FF

// pathDACLProtected reports whether dir's DACL is protected from inheritance.
// A variable so a test can stand in for a machine in either state.
var pathDACLProtected = func(dir string) (bool, error) {
	p, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return false, err
	}
	var dacl *win32ACL
	var sd *byte
	rc, _, _ := procGetNamedSecurityInfoW.Call(
		uintptr(unsafe.Pointer(p)), seFileObject, daclSecurityInformation,
		0, 0, uintptr(unsafe.Pointer(&dacl)), 0, uintptr(unsafe.Pointer(&sd)))
	if rc != 0 {
		return false, fmt.Errorf("read permissions of %s: %w", dir, syscall.Errno(rc))
	}
	defer syscall.LocalFree(syscall.Handle(unsafe.Pointer(sd)))
	return daclIsProtected(sd), nil
}
