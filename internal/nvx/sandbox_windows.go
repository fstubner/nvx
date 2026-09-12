//go:build windows
// +build windows

package nvx

import (
	"fmt"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var (
	modAdvapi32                           = syscall.NewLazyDLL("advapi32.dll")
	modKernel32                           = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcessToken                  = modAdvapi32.NewProc("OpenProcessToken")
	procGetCurrentProcess                 = modKernel32.NewProc("GetCurrentProcess")
	procCreateProcessAsUserW              = modAdvapi32.NewProc("CreateProcessAsUserW")
	procLocalFree                         = modKernel32.NewProc("LocalFree")
	procInitializeProcThreadAttributeList = modKernel32.NewProc("InitializeProcThreadAttributeList")
	procUpdateProcThreadAttribute         = modKernel32.NewProc("UpdateProcThreadAttribute")
	procDeleteProcThreadAttributeList     = modKernel32.NewProc("DeleteProcThreadAttributeList")
	procCreateProcessW                    = modKernel32.NewProc("CreateProcessW")
	procWaitForSingleObject               = modKernel32.NewProc("WaitForSingleObject")
	procGetExitCodeProcess                = modKernel32.NewProc("GetExitCodeProcess")
)

const (
	TOKEN_DUPLICATE      = 0x0002
	TOKEN_QUERY          = 0x0008
	TOKEN_ADJUST_DEFAULT = 0x0080
	TOKEN_ASSIGN_PRIMARY = 0x0001

	EXTENDED_STARTUPINFO_PRESENT                = 0x00080000
	CREATE_UNICODE_ENVIRONMENT                  = 0x00000400
	STARTF_USESTDHANDLES                        = 0x00000100
	CREATE_BREAKAWAY_FROM_JOB                   = 0x01000000
	PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITIES = 0x20009
	PROC_THREAD_ATTRIBUTE_HANDLE_LIST           = 0x20002
	INFINITE                                    = 0xFFFFFFFF
)

// SID_AND_ATTRIBUTES for Windows mandatory integrity labels.
type SID_AND_ATTRIBUTES struct {
	Sid        uintptr
	Attributes uint32
}

// The low-integrity TOKEN path that used to live here is gone.
//
// It duplicated this process's token, lowered its mandatory integrity level and
// handed the result to CreateProcessAsUser -- the containment nvx had before
// AppContainers, and nothing called any of it. What replaced it is the
// AppContainer itself: the profile SID and per-project capabilities are passed
// as security capabilities at CreateProcess time, which is a stronger boundary
// and a different mechanism, so there was no path back to the token version.
//
// Deleted rather than kept "in case": five functions and six constants that
// compile, look like the security model, and are not the security model are a
// worse thing to leave in a file someone reads to find out how containment
// works. The DIRECTORY label below is a different thing and is still applied.
//
// labelLowIntegrity applies a low mandatory integrity label to dir and its
// contents. Time-boxed like every other icacls call: this one previously had no
// timeout at all, and it walks the tree (/t), so a large persistent tool profile
// or a slow filter driver could stall a launch indefinitely. It measured at 0.03s
// on a fresh guest home, so the bound is generous rather than tight.
func labelLowIntegrity(dir string) error {
	out, err := runWinCmd(20*time.Second, "icacls", dir, "/setintegritylevel", "(OI)(CI)Low", "/t", "/c", "/q")
	if err != nil {
		return fmt.Errorf("icacls failed: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// convertStringSidToSid wraps the Windows ConvertStringSidToSidW API.
func convertStringSidToSid(stringSid *uint16, sid **syscall.SID) error {
	modAdvapi32 := syscall.NewLazyDLL("advapi32.dll")
	proc := modAdvapi32.NewProc("ConvertStringSidToSidW")
	ret, _, err := proc.Call(
		uintptr(unsafe.Pointer(stringSid)),
		uintptr(unsafe.Pointer(sid)),
	)
	if ret == 0 {
		return err
	}
	return nil
}
