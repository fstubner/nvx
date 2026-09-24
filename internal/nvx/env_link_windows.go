//go:build windows

package nvx

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

const (
	fsctlSetReparsePoint     = 0x000900A4
	ioReparseTagMountPoint   = 0xA0000003
	reparseMountPointHdrSize = 8 // four uint16 name offsets and lengths
)

// createDirLink makes link a directory junction to target.
//
// It writes the reparse point itself rather than running `cmd /c mklink /j`.
// Go quotes arguments for the C runtime, not for cmd.exe, so a link or target
// path holding & ^ | < > was split by cmd into a second command: a profile
// path with `&` ran whatever followed it, mklink failed on the truncated
// path, and cmd's exit status was the injected command's, so the failure was
// reported as success. A junction needs no privilege and no Developer Mode,
// which is why this is not os.Symlink.
func createDirLink(link, target string) error {
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("failed to create directory junction: %w", err)
	}
	if err := os.Mkdir(link, 0700); err != nil {
		return fmt.Errorf("failed to create directory junction: %w", err)
	}
	if err := setMountPoint(link, absTarget); err != nil {
		_ = os.Remove(link)
		return fmt.Errorf("failed to create directory junction: %w", err)
	}
	return nil
}

func setMountPoint(dir, target string) error {
	substitute := utf16.Encode([]rune(`\??\` + target))
	printName := utf16.Encode([]rune(target))

	// PathBuffer holds both names, each NUL-terminated; the lengths exclude
	// the NUL and are in bytes.
	pathBuf := make([]uint16, 0, len(substitute)+len(printName)+2)
	pathBuf = append(pathBuf, substitute...)
	pathBuf = append(pathBuf, 0)
	pathBuf = append(pathBuf, printName...)
	pathBuf = append(pathBuf, 0)

	dataLen := reparseMountPointHdrSize + 2*len(pathBuf)
	if dataLen > 0xFFFF {
		return fmt.Errorf("target path too long for a junction: %s", target)
	}
	buf := make([]byte, 8+dataLen)
	le := binary.LittleEndian
	le.PutUint32(buf[0:], ioReparseTagMountPoint)
	le.PutUint16(buf[4:], uint16(dataLen))
	// buf[6:8] Reserved
	le.PutUint16(buf[8:], 0)                              // SubstituteNameOffset
	le.PutUint16(buf[10:], uint16(2*len(substitute)))     // SubstituteNameLength
	le.PutUint16(buf[12:], uint16(2*(len(substitute)+1))) // PrintNameOffset
	le.PutUint16(buf[14:], uint16(2*len(printName)))      // PrintNameLength
	for i, c := range pathBuf {
		le.PutUint16(buf[16+2*i:], c)
	}

	p, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_EXISTING,
		syscall.FILE_FLAG_OPEN_REPARSE_POINT|syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(h)

	var returned uint32
	// #nosec G115 -- len(buf) is bounded by the 0xFFFF check above
	return syscall.DeviceIoControl(h, fsctlSetReparsePoint,
		(*byte)(unsafe.Pointer(&buf[0])), uint32(len(buf)), nil, 0, &returned, nil)
}
