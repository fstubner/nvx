//go:build windows

package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// Reading and writing filesystem permissions through the Win32 API instead of
// shelling out to icacls.
//
// icacls is a program for printing permissions at a person, and nvx was using it
// as an interface. Four independent acceptance passes each found a defect that
// came from that, and the fourth found the reason they kept coming:
//
//   - It reports success it did not achieve. On a path it cannot find it prints
//     "Successfully processed 0 files; Failed processing 1 files" and exits 0 --
//     for /grant and /remove:g alike. Every "the change failed, keep the record"
//     branch written against that exit code was unreachable.
//   - Its output has to be parsed to learn anything, and its notation collides
//     with the data. A directory named "d(M)x" read as holding modify access; a
//     path containing "(I)" read as an inherited entry. Both shipped.
//   - That output is localized, so any parse is correct only in English.
//
// The API returns a Win32 error code for every operation and structured entries
// with real access masks, so none of those three failure modes exist here. There
// is nothing to parse, nothing to collide, and nothing to translate. Reading a
// permission back to confirm a write is still worth doing and still done -- but
// it is now confirmation rather than the only available signal.
//
// It also removes a process spawn per permission check from the launch path,
// which is what the grant cache was built to avoid. Measured interleaved over ten
// pairs of contained launches against a fresh nvx home on this machine: median
// 4596 ms before, 3989 ms after. A first attempt suggested three times that
// improvement and did not reproduce -- the machine is noisy enough that only the
// interleaved figure is worth quoting.
//
// One icacls call is deliberately left: the low integrity label in
// sandbox_windows.go. That is a SACL operation, a different mechanism from these
// DACL entries, and it is not part of the class of defect above.

var (
	procGetNamedSecurityInfoW  = modAdvapi32.NewProc("GetNamedSecurityInfoW")
	procSetNamedSecurityInfoW  = modAdvapi32.NewProc("SetNamedSecurityInfoW")
	procConvertStringSidToSidW = modAdvapi32.NewProc("ConvertStringSidToSidW")
	procConvertSidToStringSidW = modAdvapi32.NewProc("ConvertSidToStringSidW")
	procInitializeAcl          = modAdvapi32.NewProc("InitializeAcl")
	procAddAce                 = modAdvapi32.NewProc("AddAce")
	procAddAccessAllowedAceEx  = modAdvapi32.NewProc("AddAccessAllowedAceEx")
	procGetAce                 = modAdvapi32.NewProc("GetAce")
	procGetLengthSid           = modAdvapi32.NewProc("GetLengthSid")
	procEqualSid               = modAdvapi32.NewProc("EqualSid")
)

const (
	seFileObject                       = 1
	daclSecurityInformation            = 0x00000004
	unprotectedDaclSecurityInformation = 0x20000000
	aclRevision                        = 2

	accessAllowedAceType = 0
	accessDeniedAceType  = 1

	objectInheritACE    = 0x01
	containerInheritACE = 0x02
	inheritedACE        = 0x10
)

// The access masks nvx grants, matching what the icacls notation below expanded
// to, so entries written by earlier versions are still recognised for what they
// are.
const (
	fileReadData        = 0x0001
	fileReadEA          = 0x0008
	fileExecute         = 0x0020
	fileReadAttributes  = 0x0080
	fileWriteData       = 0x0002
	fileAppendData      = 0x0004
	fileWriteEA         = 0x0010
	fileWriteAttributes = 0x0100
	deleteAccess        = 0x00010000
	standardRightsRead  = 0x00020000
	synchronizeAccess   = 0x00100000

	fileGenericRead    = standardRightsRead | fileReadData | fileReadAttributes | fileReadEA | synchronizeAccess
	fileGenericWrite   = standardRightsRead | fileWriteData | fileAppendData | fileWriteAttributes | fileWriteEA | synchronizeAccess
	fileGenericExecute = standardRightsRead | fileReadAttributes | fileExecute | synchronizeAccess

	// aclMaskReadExec is icacls (RX).
	aclMaskReadExec = fileGenericRead | fileGenericExecute
	// aclMaskModify is icacls (M).
	aclMaskModify = deleteAccess | fileGenericRead | fileGenericWrite | fileGenericExecute
	// aclMaskTraverse is icacls (X,RA): enough to walk through a directory, not to
	// list it.
	aclMaskTraverse = fileExecute | fileReadAttributes
)

// aclEntry is one access-control entry, as data rather than as a line of text.
type aclEntry struct {
	SID       string
	Mask      uint32
	Flags     uint8
	Deny      bool
	Inherited bool
}

// grantsAtLeast reports whether this entry confers everything in want.
func (e aclEntry) grantsAtLeast(want uint32) bool {
	return !e.Deny && e.Mask&want == want
}

// aceHeader mirrors ACE_HEADER.
type aceHeader struct {
	AceType  byte
	AceFlags byte
	AceSize  uint16
}

// accessAllowedACE mirrors ACCESS_ALLOWED_ACE; the SID begins at SidStart.
type accessAllowedACE struct {
	Header   aceHeader
	Mask     uint32
	SidStart uint32
}

// win32ACL mirrors the ACL header.
type win32ACL struct {
	AclRevision byte
	Sbz1        byte
	AclSize     uint16
	AceCount    uint16
	Sbz2        uint16
}

// Windows hands these back as pointers into memory it allocated, so they are
// held as typed pointers rather than uintptr: a uintptr is a number to Go, and
// converting one back into a pointer is the pattern vet rejects.
func sidFromString(s string) (*byte, error) {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		return nil, err
	}
	var sid *byte
	ret, _, e := procConvertStringSidToSidW.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&sid)))
	if ret == 0 {
		return nil, fmt.Errorf("parse SID %q: %v", s, e)
	}
	return sid, nil
}

func sidToString(sid *byte) (string, error) {
	var out *uint16
	ret, _, e := procConvertSidToStringSidW.Call(uintptr(unsafe.Pointer(sid)), uintptr(unsafe.Pointer(&out)))
	if ret == 0 {
		return "", fmt.Errorf("format SID: %v", e)
	}
	defer syscall.LocalFree(syscall.Handle(unsafe.Pointer(out)))
	return syscall.UTF16ToString(unsafe.Slice(out, 512)), nil
}

// aceSID returns a pointer to the SID embedded in an ACE. The arithmetic is done
// in one expression, which is the form the unsafe rules permit.
func aceSID(ace *accessAllowedACE) *byte {
	return (*byte)(unsafe.Add(unsafe.Pointer(ace), unsafe.Offsetof(accessAllowedACE{}.SidStart)))
}

// readDACL returns the access-control entries on path.
//
// The error is a real Win32 code, so "the path does not exist" and "the ACL says
// nothing about this identity" are finally distinguishable -- which is the whole
// reason a withdrawal could previously report success having removed nothing.
func readDACL(path string) ([]aclEntry, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	var dacl *win32ACL
	var sd *byte
	rc, _, _ := procGetNamedSecurityInfoW.Call(
		uintptr(unsafe.Pointer(p)), seFileObject, daclSecurityInformation,
		0, 0, uintptr(unsafe.Pointer(&dacl)), 0, uintptr(unsafe.Pointer(&sd)))
	if rc != 0 {
		return nil, fmt.Errorf("read permissions of %s: %w", path, syscall.Errno(rc))
	}
	defer syscall.LocalFree(syscall.Handle(unsafe.Pointer(sd)))

	if dacl == nil {
		return nil, nil // a null DACL grants everyone everything; report it as empty
	}
	entries := make([]aclEntry, 0, dacl.AceCount)
	for i := uint16(0); i < dacl.AceCount; i++ {
		var ace *accessAllowedACE
		ret, _, e := procGetAce.Call(uintptr(unsafe.Pointer(dacl)), uintptr(i), uintptr(unsafe.Pointer(&ace)))
		if ret == 0 {
			return nil, fmt.Errorf("read entry %d of %s: %v", i, path, e)
		}
		hdr := ace
		if hdr.Header.AceType != accessAllowedAceType && hdr.Header.AceType != accessDeniedAceType {
			continue // object/callback ACE types carry no plain SID at this offset
		}
		s, serr := sidToString(aceSID(ace))
		if serr != nil {
			continue // unreadable SID: report nothing rather than something wrong
		}
		entries = append(entries, aclEntry{
			SID:       s,
			Mask:      hdr.Mask,
			Flags:     hdr.Header.AceFlags,
			Deny:      hdr.Header.AceType == accessDeniedAceType,
			Inherited: hdr.Header.AceFlags&inheritedACE != 0,
		})
	}
	return entries, nil
}

// writeDACLEntry rewrites path's explicit entries: every existing entry for
// sidStr is dropped, and if mask is non-zero a new one is added with it.
//
// Replacing rather than merging, so the entry nvx writes has exactly the mask it
// intends. That exactness is load-bearing elsewhere: it is how an entry can be
// identified later as nvx's own and therefore safe to remove.
//
// Inherited entries are excluded from what is written and left to flow from the
// parent, which is what UNPROTECTED_DACL_SECURITY_INFORMATION asks for.
//
// Also defensive rather than load-bearing: Windows discards inherited entries
// supplied in an unprotected DACL, so including them changes nothing. Measured on
// a directory with 19 inherited entries -- identical output, still inheriting,
// child unaffected. Excluding them keeps the buffer honest about what it is
// asserting, which is this directory's own entries and nothing else.
func writeDACLEntry(path, sidStr string, mask uint32, flags uint8) error {
	sid, err := sidFromString(sidStr)
	if err != nil {
		return err
	}
	defer syscall.LocalFree(syscall.Handle(unsafe.Pointer(sid)))

	p, perr := syscall.UTF16PtrFromString(path)
	if perr != nil {
		return perr
	}
	var dacl *win32ACL
	var sd *byte
	rc, _, _ := procGetNamedSecurityInfoW.Call(
		uintptr(unsafe.Pointer(p)), seFileObject, daclSecurityInformation,
		0, 0, uintptr(unsafe.Pointer(&dacl)), 0, uintptr(unsafe.Pointer(&sd)))
	if rc != 0 {
		return fmt.Errorf("read permissions of %s: %w", path, syscall.Errno(rc))
	}
	defer syscall.LocalFree(syscall.Handle(unsafe.Pointer(sd)))

	// Collect the explicit entries to carry over, dropping any for this identity.
	type rawACE struct {
		ptr  unsafe.Pointer
		size uint16
		deny bool
	}
	var keep []rawACE
	if dacl != nil {
		for i := uint16(0); i < dacl.AceCount; i++ {
			var ace *accessAllowedACE
			if ret, _, _ := procGetAce.Call(uintptr(unsafe.Pointer(dacl)), uintptr(i), uintptr(unsafe.Pointer(&ace))); ret == 0 {
				continue
			}
			if ace.Header.AceFlags&inheritedACE != 0 {
				continue
			}
			if ace.Header.AceType == accessAllowedAceType || ace.Header.AceType == accessDeniedAceType {
				if eq, _, _ := procEqualSid.Call(uintptr(unsafe.Pointer(aceSID(ace))), uintptr(unsafe.Pointer(sid))); eq != 0 {
					continue // ours; the replacement is added below
				}
			}
			keep = append(keep, rawACE{ptr: unsafe.Pointer(ace), size: ace.Header.AceSize,
				deny: ace.Header.AceType == accessDeniedAceType})
		}
	}

	sidLen, _, _ := procGetLengthSid.Call(uintptr(unsafe.Pointer(sid)))
	size := int(unsafe.Sizeof(win32ACL{}))
	for _, k := range keep {
		size += int(k.size)
	}
	if mask != 0 {
		size += int(unsafe.Sizeof(accessAllowedACE{})) + int(sidLen)
	}

	buf := make([]byte, size)
	newACL := unsafe.Pointer(&buf[0])
	if ret, _, e := procInitializeAcl.Call(uintptr(newACL), uintptr(size), aclRevision); ret == 0 {
		return fmt.Errorf("build a permission list for %s: %v", path, e)
	}

	// Deny entries first, then allow entries.
	//
	// Defensive, not load-bearing: Windows canonicalises a DACL as it is written,
	// so reversing these two passes produces a byte-identical result. Measured --
	// with the order reversed, an explicit deny still came back ahead of the allow.
	// It stays because the correct order is what the list means, and relying on the
	// OS to repair it is not something to depend on silently.
	for _, pass := range []bool{true, false} {
		for _, k := range keep {
			if k.deny != pass {
				continue
			}
			const maxDWORD = ^uint32(0)
			if ret, _, e := procAddAce.Call(uintptr(newACL), aclRevision, uintptr(maxDWORD),
				uintptr(k.ptr), uintptr(k.size)); ret == 0 {
				return fmt.Errorf("carry over an existing permission on %s: %v", path, e)
			}
		}
	}
	if mask != 0 {
		if ret, _, e := procAddAccessAllowedAceEx.Call(
			uintptr(newACL), aclRevision, uintptr(flags), uintptr(mask),
			uintptr(unsafe.Pointer(sid))); ret == 0 {
			return fmt.Errorf("add a permission on %s: %v", path, e)
		}
	}

	src, _ := syscall.UTF16PtrFromString(path)
	rc, _, _ = procSetNamedSecurityInfoW.Call(
		uintptr(unsafe.Pointer(src)), seFileObject,
		daclSecurityInformation|unprotectedDaclSecurityInformation,
		0, 0, uintptr(newACL), 0)
	if rc != 0 {
		return fmt.Errorf("set permissions on %s: %w", path, syscall.Errno(rc))
	}
	return nil
}

// grantACL gives sidStr exactly mask on path, with the given inheritance flags.
func grantACL(path, sidStr string, mask uint32, flags uint8) error {
	return writeDACLEntry(path, sidStr, mask, flags)
}

// grantACLWithin is grantACL with a deadline, for the ancestor walk.
//
// Writing an ACL is a syscall rather than a process, but a syscall can still
// block: on the user profile root it goes through the OneDrive and Defender
// filter drivers and does not come back. Dropping the timebox on the assumption
// that "a syscall is fast" hung every contained launch on this machine, before
// the sandbox printed anything past its first line -- measured, and the reason
// this exists rather than a plain call.
//
// A write that overruns is abandoned, not cancelled: there is no way to interrupt
// a blocked syscall, and the goroutine ends whenever the driver lets go.
//
// That abandonment has to be bounded per PROCESS, and this used to reason that
// "the ancestor walk's own budget bounds how many can be outstanding" -- true of
// one walk, and false of a process that performs hundreds. A goroutine blocked in
// a syscall pins an OS thread, so they accumulate.
//
// An acceptance pass measured the consequence in the one place nvx is long-lived:
// the release-gate test binary. In a goroutine dump from a run that died with
// "runtime: SetWaitableTimer failed; errno= 5 / fatal error: runtime: netpoll
// failed", 49 of 83 goroutines were blocked in writeDACLEntry, all created here,
// 34 of them for over a minute of a two-minute run. The same binary also failed a
// containment test with "CreateProcess(AppContainer) ... The handle is invalid".
// Both are what thread and handle exhaustion look like, and the gate failed two
// runs in four.
//
// So two bounds, both per process:
//
//   - A path that has already stalled is not attempted again. The ancestor walk
//     meets the same few directories every launch -- the profile root above all --
//     so without this the leak grows with the number of launches rather than with
//     the number of troublesome paths.
//   - A hard ceiling on outstanding abandoned writes, as a backstop for anything
//     the first rule does not cover.
//
// Both make the grant less likely to be applied, which is the right way to fail
// here: these ancestor grants are already best-effort and already skipped for
// speed, and the caller treats an error as "carry on without it". Trading a
// best-effort grant for a process that stays alive is not a close call.
var (
	aclStalledPaths sync.Map     // path -> struct{}, paths whose write overran in this process
	aclAbandoned    atomic.Int64 // writes still outstanding after their deadline
)

// maxAbandonedACLWrites caps the OS threads that can be pinned in stalled ACL
// writes at any moment. Small: reaching it at all means the filesystem is not
// answering, and the walk is optional.
const maxAbandonedACLWrites = 8

// aclWrite is the write grantACLWithin bounds, held in a variable so a test can
// supply one that stalls. The bounds below are about what happens when a write
// does not return, and a test that cannot produce that case cannot check them.
var aclWrite = writeDACLEntry

func grantACLWithin(path, sidStr string, mask uint32, flags uint8, timeout time.Duration) error {
	key := strings.ToLower(filepath.Clean(path))
	if _, stalled := aclStalledPaths.Load(key); stalled {
		return fmt.Errorf("setting permissions on %s stalled earlier in this process; not retried", path)
	}
	if aclAbandoned.Load() >= maxAbandonedACLWrites {
		return fmt.Errorf("setting permissions on %s skipped: %d earlier writes are still blocked",
			path, maxAbandonedACLWrites)
	}

	// Exactly one of the two sides claims the outcome. A buffered channel alone
	// cannot express this: the send always succeeds, so the writer could never tell
	// that the reader had already given up, and the abandoned count would only ever
	// rise -- which would wedge every later grant behind the ceiling above.
	const (
		pending   = 0
		delivered = 1
		abandoned = 2
	)
	var state atomic.Int32
	done := make(chan error, 1)

	go func() {
		err := aclWrite(path, sidStr, mask, flags)
		if state.CompareAndSwap(pending, delivered) {
			done <- err
			return
		}
		// The reader gave up on this one. It has come back after all, so the thread
		// is free again and the path is not permanently stalled.
		aclAbandoned.Add(-1)
		aclStalledPaths.Delete(key)
	}()

	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		if !state.CompareAndSwap(pending, abandoned) {
			// It finished in the gap between the deadline firing and this claim.
			return <-done
		}
		aclAbandoned.Add(1)
		aclStalledPaths.Store(key, struct{}{})
		return fmt.Errorf("setting permissions on %s did not complete within %s", path, timeout)
	}
}

// revokeACL removes every explicit entry for sidStr from path.
func revokeACL(path, sidStr string) error {
	return writeDACLEntry(path, sidStr, 0, 0)
}

// aclEntryFor returns the explicit entry for sidStr on path, if there is one.
func aclEntryFor(path, sidStr string) (aclEntry, bool, error) {
	entries, err := readDACL(path)
	if err != nil {
		return aclEntry{}, false, err
	}
	for _, e := range entries {
		if !e.Inherited && sidsEqual(e.SID, sidStr) {
			return e, true, nil
		}
	}
	return aclEntry{}, false, nil
}

// sidsEqual compares two SID strings. Case-insensitive because Windows formats
// them consistently but callers may not.
func sidsEqual(a, b string) bool {
	return len(a) == len(b) && equalFoldASCII(a, b)
}

func equalFoldASCII(a, b string) bool {
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}
