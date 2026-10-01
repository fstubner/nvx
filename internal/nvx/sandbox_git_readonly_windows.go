//go:build windows

package nvx

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// restrictGitMetadataToReadOnly leaves capSID read and execute, and nothing
// more, on the git metadata of workDir and of the project root it belongs to;
// see gitMetadataPaths.
//
// The project's capability holds modify on the working directory, inherited by
// everything beneath, .git included. A deny entry for the capability on .git
// does not take that back: measured 2026-10-01 on Windows 11 26200, with an
// explicit deny for the capability first in .git's list and the inherited allow
// after it, a contained process still created hooks, rewrote config and renamed
// .git. The capability's allow has to be absent instead. So .git stops
// inheriting from the project (its inherited entries are kept, as explicit
// copies), the capability's entries are left out, and one read/execute entry is
// added for it.
//
// The project root is included because grants persist. A run from the root
// leaves the capability holding modify there, and a later run from a
// subdirectory carries the same capability.
//
// Every other identity keeps the access it had, so git and every other
// uncontained program are unaffected. What changes for them is that a later
// permission change on the project folder no longer flows into .git. Like the
// project's modify entry this stays on disk, and the next launch finds it in
// place and writes nothing.
func restrictGitMetadataToReadOnly(capSID, workDir string) error {
	paths := gitMetadataPaths(workDir)
	if scope := sandboxScopeForWorkDir(workDir); scope != "" && !dirsEqual(scope, workDir) {
		paths = append(paths, gitMetadataPaths(scope)...)
	}
	for _, p := range dedupeStrings(paths) {
		if err := restrictSandboxToReadExec(capSID, p); err != nil {
			return err
		}
	}
	return nil
}

// restrictSandboxToReadExec rewrites path's DACL as described above, unless it
// already reads that way.
//
// Through SetNamedSecurityInfoW, which propagates the change over path's tree.
// That costs time in proportion to what is under .git, once per repository.
// Not timeboxed: like the project's own grant, the launch cannot go ahead
// without it.
func restrictSandboxToReadExec(sidStr, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	var flags uint8
	if info.IsDir() {
		flags = nvxInheritFlags
	}
	if sandboxIsReadExecOnly(sidStr, path, flags) {
		return nil
	}
	if err := writeProtectedDACLReadExec(path, sidStr, flags); err != nil {
		return fmt.Errorf("make %s read-only for the sandbox: %w", path, err)
	}
	if !sandboxIsReadExecOnly(sidStr, path, flags) {
		return fmt.Errorf("%s is still writable by the sandbox after restricting it", path)
	}
	return nil
}

// sandboxIsReadExecOnly reports whether path's DACL is protected and its only
// entry for sidStr is the read/execute entry writeProtectedDACLReadExec adds.
func sandboxIsReadExecOnly(sidStr, path string, flags uint8) bool {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	var dacl *win32ACL
	var sd *byte
	if rc, _, _ := procGetNamedSecurityInfoW.Call(
		uintptr(unsafe.Pointer(p)), seFileObject, daclSecurityInformation,
		0, 0, uintptr(unsafe.Pointer(&dacl)), 0, uintptr(unsafe.Pointer(&sd))); rc != 0 {
		return false
	}
	defer syscall.LocalFree(syscall.Handle(unsafe.Pointer(sd)))
	if !daclIsProtected(sd) {
		return false
	}
	entries, err := readDACL(path)
	if err != nil {
		return false
	}
	found := false
	for _, e := range entries {
		if !sidsEqual(e.SID, sidStr) {
			continue
		}
		if e.Deny || e.Inherited || e.Mask != aclMaskReadExec || e.Flags != flags || found {
			return false
		}
		found = true
	}
	return found
}

// writeProtectedDACLReadExec writes path's DACL protected, with every entry it
// has now -- inherited ones copied as explicit -- except those for sidStr, and
// one read/execute entry for sidStr with the given inheritance.
//
// Denies go ahead of allows, which is the order an access check reads, and the
// order Windows keeps for explicit entries.
func writeProtectedDACLReadExec(path, sidStr string, flags uint8) error {
	sid, err := sidFromString(sidStr)
	if err != nil {
		return err
	}
	defer syscall.LocalFree(syscall.Handle(unsafe.Pointer(sid)))

	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	var dacl *win32ACL
	var sd *byte
	if rc, _, _ := procGetNamedSecurityInfoW.Call(
		uintptr(unsafe.Pointer(p)), seFileObject, daclSecurityInformation,
		0, 0, uintptr(unsafe.Pointer(&dacl)), 0, uintptr(unsafe.Pointer(&sd))); rc != 0 {
		return fmt.Errorf("read permissions of %s: %w", path, syscall.Errno(rc))
	}
	defer syscall.LocalFree(syscall.Handle(unsafe.Pointer(sd)))

	// Copies, because an inherited entry has to lose its inherited flag to be
	// kept in a protected list.
	var denies, allows [][]byte
	if dacl != nil {
		for i := uint16(0); i < dacl.AceCount; i++ {
			var ace *accessAllowedACE
			if ret, _, _ := procGetAce.Call(uintptr(unsafe.Pointer(dacl)), uintptr(i), uintptr(unsafe.Pointer(&ace))); ret == 0 {
				continue
			}
			plain := ace.Header.AceType == accessAllowedAceType || ace.Header.AceType == accessDeniedAceType
			if plain {
				if eq, _, _ := procEqualSid.Call(uintptr(unsafe.Pointer(aceSID(ace))), uintptr(unsafe.Pointer(sid))); eq != 0 {
					continue
				}
			}
			raw := make([]byte, ace.Header.AceSize)
			copy(raw, unsafe.Slice((*byte)(unsafe.Pointer(ace)), int(ace.Header.AceSize)))
			raw[1] &^= inheritedACE // AceFlags
			if ace.Header.AceType == accessDeniedAceType {
				denies = append(denies, raw)
			} else {
				allows = append(allows, raw)
			}
		}
	}

	sidLen, _, _ := procGetLengthSid.Call(uintptr(unsafe.Pointer(sid)))
	size := int(unsafe.Sizeof(win32ACL{})) + int(unsafe.Sizeof(accessAllowedACE{})) + int(sidLen)
	for _, group := range [][][]byte{denies, allows} {
		for _, raw := range group {
			size += len(raw)
		}
	}
	buf := make([]byte, size)
	newACL := unsafe.Pointer(&buf[0])
	if ret, _, e := procInitializeAcl.Call(uintptr(newACL), uintptr(size), aclRevision); ret == 0 {
		return fmt.Errorf("build a permission list for %s: %v", path, e)
	}
	const maxDWORD = ^uint32(0)
	for _, group := range [][][]byte{denies, allows} {
		for _, raw := range group {
			if ret, _, e := procAddAce.Call(uintptr(newACL), aclRevision, uintptr(maxDWORD),
				uintptr(unsafe.Pointer(&raw[0])), uintptr(len(raw))); ret == 0 {
				return fmt.Errorf("carry over an existing permission on %s: %v", path, e)
			}
		}
	}
	if ret, _, e := procAddAccessAllowedAceEx.Call(
		uintptr(newACL), aclRevision, uintptr(flags), uintptr(aclMaskReadExec),
		uintptr(unsafe.Pointer(sid))); ret == 0 {
		return fmt.Errorf("add a permission on %s: %v", path, e)
	}

	if rc, _, _ := procSetNamedSecurityInfoW.Call(
		uintptr(unsafe.Pointer(p)), seFileObject,
		daclSecurityInformation|protectedDaclSecurityInformation,
		0, 0, uintptr(newACL), 0); rc != 0 {
		return fmt.Errorf("set permissions on %s: %w", path, syscall.Errno(rc))
	}
	return nil
}
