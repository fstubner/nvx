//go:build windows

package nvx

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

// hideDotenvFromSandbox takes the sandbox's access away from each dotenv file
// in the project, by permissions. See isDotenvName for which files.
//
// A contained process reads .env through the project capability's entry, which
// .env inherits from the project folder. Measured 2026-10-06 on Windows 11
// 26300: a deny entry for an AppContainer identity does not stop the read, and
// an integrity label is not applied to an AppContainer process (see
// TestDenyACEHidesSecretFromAppContainer and
// TestIntegrityLabelHidesSecretFromAppContainer). What does stop it is .env's
// permission list not containing a sandbox identity at all. So each file gets a protected
// list holding every entry it has now, inherited ones copied in as explicit, in
// the same order, without the allow entries for AppContainer package SIDs
// (S-1-15-2-*, ALL APPLICATION PACKAGES among them) and capability SIDs
// (S-1-15-3-*). The user, SYSTEM, Administrators and every other identity keep
// exactly what they had. The contained process then gets "access denied" on
// read, write, rename and delete, and the developer still reads and edits the
// file in place. A later grant on the project folder does not reach a file whose
// list is protected.
//
// An editor that saves by writing a new file and renaming it over .env, and a
// git checkout, leave a file that inherits again. The next launch finds it and
// protects it again, so the files are checked on every launch. While a contained
// process runs, watchDotenvFiles protects such a file as it appears. A file that
// is already protected is only read, so a normal launch writes nothing.
//
// Each file is recorded in the project's grant ledger with its permissions
// from before, before it is changed, so `nvx grants reset` can put them back.
//
// A link or junction named like a dotenv file, and a file reached through one
// that lies outside the project, is left alone: the file it lands on is not
// the project's to change. Linux follows a link and masks its target, which
// is a mount in the run's own namespace and changes nothing on disk.
//
// Best effort: a file nvx cannot change, because the user does not own it or
// the record cannot be saved, stays readable to the sandbox and the run goes
// ahead with one line saying so.
func hideDotenvFromSandbox(nvxHome, scope string) {
	found, complete := findDotenvFiles(scope, dotenvScanLimit)
	if !complete {
		LogWarn("Stopped looking for .env files after %d entries under %s; any further down stay readable in the sandbox.", dotenvScanLimit, scope)
	}
	protectDotenvFiles(nvxHome, scope, found)
}

// protectDotenvFiles hides each of found, dotenv files under the project scope,
// from the sandbox, as hideDotenvFromSandbox describes. The launch hands it every
// dotenv file in the project, and watchDotenvFiles the ones that appear while a
// contained process runs.
func protectDotenvFiles(nvxHome, scope string, found []string) {
	if len(found) == 0 {
		return
	}
	root, err := finalPathOf(scope)
	if err != nil {
		LogWarn("Could not resolve %s, so its .env files stay readable in the sandbox: %v", scope, err)
		return
	}

	type pendingDotenv struct {
		path string
		h    syscall.Handle
		acl  dotenvACL
	}
	var todo []pendingDotenv
	defer func() {
		for _, p := range todo {
			syscall.CloseHandle(p.h)
		}
	}()
	for _, f := range found {
		h, writable, err := openDotenvWithin(root, f)
		if errors.Is(err, errDotenvLink) {
			LogInfo("Left the permissions of %s alone: it is a link or lies outside the project, so it is not hidden from the sandbox.", f)
			continue
		}
		if os.IsNotExist(err) {
			continue // removed since the walk
		}
		if err != nil {
			LogWarn("Could not check %s, so it may stay readable in the sandbox: %v", f, err)
			continue
		}
		acl, err := readDotenvACL(h)
		if err != nil || !dotenvNeedsProtection(acl.protected, acl.aces) {
			if err != nil {
				LogWarn("Could not read the permissions of %s, so it may stay readable in the sandbox: %v", f, err)
			}
			syscall.CloseHandle(h)
			continue
		}
		if !writable || acl.null {
			syscall.CloseHandle(h)
			LogWarn("Could not hide %s from the sandbox: nvx may not change its permissions. It stays readable in the sandbox.", f)
			continue
		}
		todo = append(todo, pendingDotenv{path: f, h: h, acl: acl})
	}
	if len(todo) == 0 {
		return
	}

	// Recorded before anything is changed, as with the read/execute grants: a
	// change with no record is one `nvx grants reset` could never put back.
	// Records of files that no longer exist go in the same write, which keeps
	// the record small without a write of its own.
	if err := updateProjectGrants(nvxHome, scope, func(g *projectGrants) error {
		kept := g.ProtectedDotenv[:0]
		for _, r := range g.ProtectedDotenv {
			if _, err := os.Lstat(r.Path); !os.IsNotExist(err) {
				kept = append(kept, r)
			}
		}
		g.ProtectedDotenv = kept
		for _, p := range todo {
			g.ProtectedDotenv = recordProtectedDotenv(g.ProtectedDotenv, p.path, p.acl.sddl, !p.acl.protected)
		}
		return nil
	}); err != nil {
		LogWarn("Could not record the permissions of this project's .env files, so they stay readable in the sandbox: %v", err)
		return
	}

	for _, p := range todo {
		if err := writeProtectedDotenvACL(p.h, p.acl); err != nil {
			LogWarn("Could not hide %s from the sandbox, so it stays readable there: %v", p.path, err)
			continue
		}
		LogDetail("Hid %s from the sandbox.", p.path)
	}
}

// errDotenvLink says a dotenv path is a link, or reaches a file outside the
// project through one. nvx does not change permissions through a link: the
// file it lands on is not the project's to change.
var errDotenvLink = errors.New("a link, or outside the project")

// openDotenvWithin opens path for reading and, if allowed, writing its
// permissions, without following a link at its last component. It refuses a
// link, a directory, and a file whose real path is not under root (a junction
// further up the path). The handle is what is read and written afterwards, so
// the file checked is the file changed.
func openDotenvWithin(root, path string) (h syscall.Handle, writable bool, err error) {
	const readControl, writeDAC = 0x00020000, 0x00040000
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, false, err
	}
	open := func(access uint32) (syscall.Handle, error) {
		return syscall.CreateFile(p, access,
			syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
			nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	}
	writable = true
	h, err = open(readControl | writeDAC)
	if errors.Is(err, syscall.ERROR_ACCESS_DENIED) {
		writable = false
		h, err = open(readControl)
	}
	if err != nil {
		return 0, false, err
	}
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(h, &info); err != nil {
		syscall.CloseHandle(h)
		return 0, false, err
	}
	if info.FileAttributes&(syscall.FILE_ATTRIBUTE_REPARSE_POINT|syscall.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		syscall.CloseHandle(h)
		return 0, false, errDotenvLink
	}
	final, err := finalPathOfHandle(h)
	if err != nil {
		syscall.CloseHandle(h)
		return 0, false, err
	}
	if !pathWithin(root, final) {
		syscall.CloseHandle(h)
		return 0, false, errDotenvLink
	}
	return h, writable, nil
}

// pathWithin reports whether path is strictly beneath root. Both are final
// paths, so case is the only difference to allow for.
func pathWithin(root, path string) bool {
	root = strings.TrimRight(root, `\`) + `\`
	return len(path) > len(root) && strings.EqualFold(path[:len(root)], root)
}

var procGetFinalPathNameByHandleW = modKernel32.NewProc("GetFinalPathNameByHandleW")

// finalPathOf returns the real path of path, with every link and junction on
// the way resolved.
func finalPathOf(path string) (string, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	h, err := syscall.CreateFile(p, 0,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer syscall.CloseHandle(h)
	return finalPathOfHandle(h)
}

func finalPathOfHandle(h syscall.Handle) (string, error) {
	buf := make([]uint16, 1024)
	for {
		n, _, e := procGetFinalPathNameByHandleW.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0)
		if n == 0 {
			return "", fmt.Errorf("resolve the real path: %v", e)
		}
		if int(n) < len(buf) {
			return syscall.UTF16ToString(buf[:n]), nil
		}
		buf = make([]uint16, n)
	}
}

// dotenvACL is a file's permission list as nvx reads it before changing it.
type dotenvACL struct {
	sddl      string   // the whole list, to record
	aces      [][]byte // each entry as Windows stores it
	protected bool     // the list does not inherit
	control   uint16
	null      bool // no list at all, which admits everyone
}

var (
	procGetSecurityInfo           = modAdvapi32.NewProc("GetSecurityInfo")
	procSetKernelObjectSecurity   = modAdvapi32.NewProc("SetKernelObjectSecurity")
	procConvertSDToStringSD       = modAdvapi32.NewProc("ConvertSecurityDescriptorToStringSecurityDescriptorW")
	procConvertStringSDToSDDotenv = modAdvapi32.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW")
)

func readDotenvACL(h syscall.Handle) (dotenvACL, error) {
	var dacl *win32ACL
	var sd *byte
	if rc, _, _ := procGetSecurityInfo.Call(uintptr(h), seFileObject, daclSecurityInformation,
		0, 0, uintptr(unsafe.Pointer(&dacl)), 0, uintptr(unsafe.Pointer(&sd))); rc != 0 {
		return dotenvACL{}, syscall.Errno(rc)
	}
	defer syscall.LocalFree(syscall.Handle(unsafe.Pointer(sd)))
	return dotenvACLFrom(sd, dacl)
}

// dotenvACLFrom reads a security descriptor and its DACL into a dotenvACL.
func dotenvACLFrom(sd *byte, dacl *win32ACL) (dotenvACL, error) {
	var out dotenvACL
	var str *uint16
	if ret, _, e := procConvertSDToStringSD.Call(uintptr(unsafe.Pointer(sd)), 1, daclSecurityInformation,
		uintptr(unsafe.Pointer(&str)), 0); ret == 0 {
		return out, fmt.Errorf("describe the permissions: %v", e)
	}
	out.sddl = syscall.UTF16ToString(unsafe.Slice(str, 1<<16))
	syscall.LocalFree(syscall.Handle(unsafe.Pointer(str)))
	out.control = daclControl(sd)
	out.protected = out.control&seDaclProtected != 0
	if dacl == nil {
		out.null = true
		return out, nil
	}
	for i := uint16(0); i < dacl.AceCount; i++ {
		var ace *aceHeader
		if ret, _, e := procGetAce.Call(uintptr(unsafe.Pointer(dacl)), uintptr(i), uintptr(unsafe.Pointer(&ace))); ret == 0 {
			return out, fmt.Errorf("read entry %d: %v", i, e)
		}
		out.aces = append(out.aces, bytes.Clone(unsafe.Slice((*byte)(unsafe.Pointer(ace)), int(ace.AceSize))))
	}
	return out, nil
}

// accessAllowedCallbackAceType is a conditional allow entry. Its SID sits where
// a plain allow entry's does.
const accessAllowedCallbackAceType = 9

// isSandboxAllowACE reports whether ace admits an AppContainer package
// (S-1-15-2-*) or capability (S-1-15-3-*). The SID is read from the entry's
// bytes: revision, sub-authority count, a 6-byte authority, then the
// sub-authorities.
func isSandboxAllowACE(ace []byte) bool {
	if len(ace) < 8+8+8 || (ace[0] != accessAllowedAceType && ace[0] != accessAllowedCallbackAceType) {
		return false
	}
	sid := ace[8:]
	if sid[0] != 1 || sid[1] < 2 || len(sid) < 8+4*int(sid[1]) {
		return false
	}
	if !bytes.Equal(sid[2:8], []byte{0, 0, 0, 0, 0, 15}) {
		return false
	}
	first := binary.LittleEndian.Uint32(sid[8:12])
	return first == 2 || first == 3
}

// dotenvNeedsProtection reports whether a file's list still lets the sandbox
// in, or still inherits, which a later grant on the project would use.
func dotenvNeedsProtection(protected bool, aces [][]byte) bool {
	if !protected {
		return true
	}
	for _, a := range aces {
		if isSandboxAllowACE(a) {
			return true
		}
	}
	return false
}

// dotenvKeptACEs is the list a protected dotenv file gets: every entry but the
// sandbox's allow entries, in the order they were in, each marked explicit.
//
// The order is kept, not regrouped with every deny first. An inherited deny
// sits after the file's own allow entries, and moving it ahead of them could
// take away access the user has now. Deny entries for a sandbox identity stay:
// removing a deny could only widen access.
func dotenvKeptACEs(aces [][]byte) [][]byte {
	var out [][]byte
	for _, a := range aces {
		if isSandboxAllowACE(a) {
			continue
		}
		c := bytes.Clone(a)
		c[1] &^= inheritedACE
		out = append(out, c)
	}
	return out
}

// writeProtectedDotenvACL writes dotenvKeptACEs, protected, to the open file,
// as one write to that file alone, and checks it took.
func writeProtectedDotenvACL(h syscall.Handle, acl dotenvACL) error {
	kept := dotenvKeptACEs(acl.aces)
	size := int(unsafe.Sizeof(win32ACL{}))
	for _, a := range kept {
		size += len(a)
	}
	buf := make([]byte, size)
	newACL := unsafe.Pointer(&buf[0])
	if ret, _, e := procInitializeAcl.Call(uintptr(newACL), uintptr(size), aclRevision); ret == 0 {
		return fmt.Errorf("build a permission list: %v", e)
	}
	const maxDWORD = ^uint32(0)
	for _, a := range kept {
		if ret, _, e := procAddAce.Call(uintptr(newACL), aclRevision, uintptr(maxDWORD),
			uintptr(unsafe.Pointer(&a[0])), uintptr(len(a))); ret == 0 {
			return fmt.Errorf("carry over an existing permission: %v", e)
		}
	}
	const (
		securityDescriptorRevision = 1
		securityDescriptorMinLen   = 40 // SECURITY_DESCRIPTOR_MIN_LENGTH on 64-bit
		seDaclAutoInheritReq       = 0x0100
		seDaclAutoInherited        = 0x0400
	)
	desc := make([]byte, securityDescriptorMinLen)
	if ret, _, e := procInitializeSecurityDescriptor.Call(uintptr(unsafe.Pointer(&desc[0])), securityDescriptorRevision); ret == 0 {
		return fmt.Errorf("build a security descriptor: %v", e)
	}
	if ret, _, e := procSetSecurityDescriptorDacl.Call(uintptr(unsafe.Pointer(&desc[0])), 1, uintptr(newACL), 0); ret == 0 {
		return fmt.Errorf("attach the permission list: %v", e)
	}
	control := uintptr(seDaclProtected)
	if acl.control&seDaclAutoInherited != 0 {
		control |= seDaclAutoInherited | seDaclAutoInheritReq
	}
	if ret, _, e := procSetSecurityDescriptorControl.Call(uintptr(unsafe.Pointer(&desc[0])),
		seDaclAutoInherited|seDaclAutoInheritReq|seDaclProtected, control); ret == 0 {
		return fmt.Errorf("mark the permission list protected: %v", e)
	}
	if ret, _, e := procSetKernelObjectSecurity.Call(uintptr(h), daclSecurityInformation, uintptr(unsafe.Pointer(&desc[0]))); ret == 0 {
		return fmt.Errorf("set permissions: %v", e)
	}
	after, err := readDotenvACL(h)
	if err != nil {
		return err
	}
	if dotenvNeedsProtection(after.protected, after.aces) {
		return errors.New("the permissions still admit the sandbox after writing them")
	}
	return nil
}

// errDotenvChanged says a protected file's permissions are no longer the ones
// nvx wrote, so putting back the recorded ones would undo someone else's
// change.
var errDotenvChanged = errors.New("its permissions were changed after nvx protected it")

// restoreProtectedDotenv puts back the permissions recorded for r, if the file
// still carries the list nvx wrote. root is the project the record belongs to;
// a path that now leads outside it, or through a link, is left alone.
//
// errNothingToWithdraw: the file is gone, or no longer carries nvx's list (an
// editor or git replaced it, so it inherits again). errDotenvChanged: someone
// changed the list since, and it is left as it is.
func restoreProtectedDotenv(root string, r protectedDotenv) error {
	realRoot, err := finalPathOf(root)
	if err != nil {
		if os.IsNotExist(err) {
			return errNothingToWithdraw
		}
		return err
	}
	h, writable, err := openDotenvWithin(realRoot, r.Path)
	if os.IsNotExist(err) || errors.Is(err, errDotenvLink) {
		return errNothingToWithdraw
	}
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(h)
	current, err := readDotenvACL(h)
	if err != nil {
		return err
	}
	if current.sddl == r.SDDL || !current.protected {
		return errNothingToWithdraw
	}

	p, err := syscall.UTF16PtrFromString(r.SDDL)
	if err != nil {
		return err
	}
	var sd *byte
	if ret, _, e := procConvertStringSDToSDDotenv.Call(uintptr(unsafe.Pointer(p)), 1,
		uintptr(unsafe.Pointer(&sd)), 0); ret == 0 {
		return fmt.Errorf("read the recorded permissions: %v", e)
	}
	defer syscall.LocalFree(syscall.Handle(unsafe.Pointer(sd)))
	var present, defaulted int32
	var dacl *win32ACL
	if ret, _, e := procGetSecurityDescriptorDacl.Call(uintptr(unsafe.Pointer(sd)),
		uintptr(unsafe.Pointer(&present)), uintptr(unsafe.Pointer(&dacl)), uintptr(unsafe.Pointer(&defaulted))); ret == 0 {
		return fmt.Errorf("read the recorded permissions: %v", e)
	}
	original, err := dotenvACLFrom(sd, dacl)
	if err != nil {
		return err
	}
	if !aceListsEqual(current.aces, dotenvKeptACEs(original.aces)) {
		return errDotenvChanged
	}
	if !writable {
		return errors.New("nvx may not change its permissions")
	}
	// Windows keeps the auto-inherited mark only when AUTO_INHERIT_REQ comes
	// with it; see writeThisFolderEntry.
	const seDaclAutoInheritReq, seDaclAutoInherited = 0x0100, 0x0400
	if original.control&seDaclAutoInherited != 0 {
		if ret, _, e := procSetSecurityDescriptorControl.Call(uintptr(unsafe.Pointer(sd)),
			seDaclAutoInheritReq, seDaclAutoInheritReq); ret == 0 {
			return fmt.Errorf("prepare the recorded permissions: %v", e)
		}
	}
	if ret, _, e := procSetKernelObjectSecurity.Call(uintptr(h), daclSecurityInformation, uintptr(unsafe.Pointer(sd))); ret == 0 {
		return fmt.Errorf("set permissions: %v", e)
	}
	return nil
}

var procGetSecurityDescriptorDacl = modAdvapi32.NewProc("GetSecurityDescriptorDacl")

func aceListsEqual(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}
