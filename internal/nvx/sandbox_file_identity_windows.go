//go:build windows

package nvx

import (
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

// A directory's identity survives a rename, which its path does not.
//
// A read/execute permission nvx grants lives on the directory and travels with it
// when it is renamed or moved. `nvx grants reset` is given the path it recorded,
// and for a path that is not there it cannot tell a directory that was deleted,
// whose permission went with it, from one that was renamed, whose permission is
// still in force somewhere else. It had to treat both as a failure. The file ID
// NTFS and ReFS give a directory tells them apart: the object can be opened by
// that ID wherever it is now, and cannot be opened once it is deleted.
//
// Measured 2026-10-08 on NTFS: after a rename and after a move to another folder
// the ID opened and the final path of the handle was the new path, and after the
// delete OpenFileById failed with ERROR_INVALID_PARAMETER, also for the ID of a
// directory recreated at the same path.

var (
	procGetFileInformationByHandleEx = modKernel32.NewProc("GetFileInformationByHandleEx")
	procOpenFileById                 = modKernel32.NewProc("OpenFileById")
)

const (
	fileIDInfoClass    = 18 // FileIdInfo
	extendedFileIDType = 2  // ExtendedFileIdType: a FILE_ID_128
	fileReadAttrs      = 0x80
	errInvalidParam    = syscall.Errno(87)
)

// fileIDInfo mirrors FILE_ID_INFO.
type fileIDInfo struct {
	VolumeSerialNumber uint64
	FileID             [16]byte
}

// fileIDDescriptor mirrors FILE_ID_DESCRIPTOR for an ExtendedFileIdType.
type fileIDDescriptor struct {
	Size uint32
	Type uint32
	ID   [16]byte
}

// openForIdentity opens a directory for its attributes only.
func openForIdentity(path string) (syscall.Handle, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	return syscall.CreateFile(p, fileReadAttrs,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
}

func identityOfHandle(h syscall.Handle) (fileIDInfo, bool) {
	var info fileIDInfo
	r, _, _ := procGetFileInformationByHandleEx.Call(uintptr(h), fileIDInfoClass, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
	return info, r != 0
}

// directoryIdentity is the identity of the directory at path, as the string kept
// beside a grant, or "" when the path is not there or its volume gives no ID.
func directoryIdentity(path string) string {
	h, err := openForIdentity(path)
	if err != nil {
		return ""
	}
	defer syscall.CloseHandle(h)
	info, ok := identityOfHandle(h)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%x:%x", info.VolumeSerialNumber, info.FileID)
}

// locateGrantedDirectory says where the directory a grant was recorded for is
// now, going by its identity.
func locateGrantedDirectory(g readExecGrant) (string, directoryLocation) {
	var serial uint64
	vol, idHex, ok := strings.Cut(g.ID, ":")
	if !ok {
		return "", locationUnknown
	}
	id, err := hex.DecodeString(idHex)
	if _, serr := fmt.Sscanf(vol, "%x", &serial); err != nil || serr != nil || len(id) != 16 {
		return "", locationUnknown
	}

	// Any open file on the volume will do for the hint, and its root is one.
	root := filepath.VolumeName(g.Path)
	if root == "" {
		return "", locationUnknown
	}
	hv, err := openForIdentity(root + `\`)
	if err != nil {
		return "", locationUnknown
	}
	defer syscall.CloseHandle(hv)
	// Another disk at the same letter says nothing about this directory.
	if cur, ok := identityOfHandle(hv); !ok || cur.VolumeSerialNumber != serial {
		return "", locationUnknown
	}

	d := fileIDDescriptor{Size: uint32(unsafe.Sizeof(fileIDDescriptor{})), Type: extendedFileIDType}
	copy(d.ID[:], id)
	h, _, errno := procOpenFileById.Call(uintptr(hv), uintptr(unsafe.Pointer(&d)),
		fileReadAttrs, uintptr(syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE), 0,
		uintptr(syscall.FILE_FLAG_BACKUP_SEMANTICS))
	if syscall.Handle(h) == syscall.InvalidHandle {
		if errno == errInvalidParam { // there is no such object
			return "", locationGone
		}
		return "", locationUnknown
	}
	defer syscall.CloseHandle(syscall.Handle(h))

	path, err := finalPathOfHandle(syscall.Handle(h))
	if err != nil {
		return "", locationUnknown
	}
	switch {
	case strings.HasPrefix(path, `\\?\UNC\`):
		path = `\\` + strings.TrimPrefix(path, `\\?\UNC\`)
	case strings.HasPrefix(path, `\\?\`):
		path = strings.TrimPrefix(path, `\\?\`)
	}
	return filepath.Clean(path), locationHere
}
