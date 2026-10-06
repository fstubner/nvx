//go:build windows

package nvx

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"time"
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
// Doctor is where someone would find out, so it looks. `nvx setup` is what
// repairs it, when the folder's own entries make that safe.
func reportUnprotectedProfile() bool {
	weakened := false
	for _, d := range findUnprotectedProfileDirs() {
		weakened = true
		fmt.Printf("  [FAIL] %s takes its parent's permissions; Windows ships it protected from them\n", d.Dir)
		fmt.Println("         an nvx version before this one could cause this, and it can let other accounts on this machine into the folder")
		if d.Safe {
			fmt.Println("         to restore it, from an Administrator terminal: nvx setup")
		} else {
			fmt.Println("         its own entries would not keep you in if the inherited ones were removed; review its permissions before changing them")
		}
	}
	return weakened
}

// unprotectedDir is a profile folder, or the folder above it, that takes its
// parent's permissions, and whether its own entries would still let the people
// who need access in once the inherited ones are gone.
type unprotectedDir struct {
	Dir  string
	Safe bool
}

// findUnprotectedProfileDirs looks at the folder above the profile and at the
// profile, both of which Windows ships protected. A folder is Safe to restore
// only when removing its inherited entries cannot lock SYSTEM, Administrators or
// the owner out. Otherwise the right step is to look first.
func findUnprotectedProfileDirs() []unprotectedDir {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	userSID := ""
	if u, uerr := user.Current(); uerr == nil {
		userSID = u.Uid
	}
	var found []unprotectedDir
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
		found = append(found, unprotectedDir{Dir: dir, Safe: protectionIsSafeToRestore(entries, owner)})
	}
	return found
}

// restoreProtectionTimeout bounds one restore. Removing the inherited entries
// from a profile folder makes Windows re-derive the permissions of everything
// beneath it, which takes minutes on a large profile. A variable so a test can
// shorten it.
var restoreProtectionTimeout = 20 * time.Minute

// restoreProfileProtection protects dir from its parent's permissions and drops
// the inherited entries, keeping the explicit ones. It is what
// `icacls dir /inheritance:r` does, run through the same system-directory
// lookup as every other privileged call. The result is read back, because a
// tool that exits 0 without having changed the folder is the failure this
// exists to catch.
func restoreProfileProtection(dir string) error {
	out, err := runWinCmd(restoreProtectionTimeout, "icacls", dir, "/inheritance:r")
	if err != nil {
		return fmt.Errorf("icacls /inheritance:r on %s: %v (%s)", dir, err, strings.TrimSpace(string(out)))
	}
	protected, err := pathDACLProtected(dir)
	if err != nil {
		return err
	}
	if !protected {
		return fmt.Errorf("%s still takes its parent's permissions", dir)
	}
	return nil
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
