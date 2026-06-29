// +build windows

package main

import (
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"
)

var (
	modAdvapi32                   = syscall.NewLazyDLL("advapi32.dll")
	modKernel32                   = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcessToken          = modAdvapi32.NewProc("OpenProcessToken")
	procDuplicateTokenEx          = modAdvapi32.NewProc("DuplicateTokenEx")
	procSetTokenInformation       = modAdvapi32.NewProc("SetTokenInformation")
	procGetCurrentProcess         = modKernel32.NewProc("GetCurrentProcess")
	procCreateProcessWithTokenW   = modAdvapi32.NewProc("CreateProcessWithTokenW")
	procLocalFree                 = modKernel32.NewProc("LocalFree")
)


const (
	TOKEN_DUPLICATE          = 0x0002
	TOKEN_QUERY              = 0x0008
	TOKEN_ADJUST_DEFAULT     = 0x0080
	TOKEN_ASSIGN_PRIMARY     = 0x0001
	TOKEN_ALL_ACCESS         = 0xF01FF
	SecurityImpersonation    = 2
	TokenPrimary             = 1
	TokenIntegrityLevel      = 25
	SECURITY_MANDATORY_LOW_RID = 0x1000
)

// SID_AND_ATTRIBUTES for Low Integrity level
type SID_AND_ATTRIBUTES struct {
	Sid        uintptr
	Attributes uint32
}

// TOKEN_MANDATORY_LABEL for setting integrity level
type TOKEN_MANDATORY_LABEL struct {
	Label SID_AND_ATTRIBUTES
}

// applySandboxIsolation applies Windows-specific process isolation.
// On Windows, we attempt to create a Low Integrity token to prevent the sandboxed
// process from writing to Medium-integrity locations (most user folders).
//
// If token manipulation fails (e.g., insufficient privileges), we fall back to
// environment-only isolation which still provides credential scrubbing and home
// redirection, but without OS-level write protection.
func applySandboxIsolation(cmd *exec.Cmd, guestHome string) {
	// On Windows, we use the CREATE_SUSPENDED flag along with a reduced-integrity
	// token. However, Go's exec.Cmd doesn't natively support token assignment
	// pre-spawn. Instead, we set the process creation flags to create a new
	// console and job object for isolation.
	//
	// For full Low Integrity enforcement, the recommended approach is to use
	// CreateProcessWithTokenW via syscall, but this requires elevated privileges
	// for token duplication. We attempt it and fall back gracefully.

	cmd.SysProcAttr = &syscall.SysProcAttr{
		// CREATE_NEW_PROCESS_GROUP prevents Ctrl+C from propagating to the
		// sandboxed process, giving nvx control over termination.
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}

	// Attempt Low Integrity token assignment via the Windows API.
	// This is a best-effort operation — if it fails, env scrubbing and home
	// redirection still provide meaningful isolation.
	if err := tryApplyLowIntegrity(cmd); err != nil {
		LogWarn("Low Integrity isolation unavailable: %v (environment isolation still active)", err)
	} else {
		LogInfo("Windows Low Integrity token applied successfully")
	}
}

// tryApplyLowIntegrity attempts to duplicate the current process token,
// lower its integrity level to Low (S-1-16-4096), and assign it to the
// child process via SysProcAttr.Token.
func tryApplyLowIntegrity(cmd *exec.Cmd) error {
	// Get the current process token
	var processToken syscall.Token
	currentProcess, _, _ := procGetCurrentProcess.Call()
	ret, _, err := procOpenProcessToken.Call(
		currentProcess,
		uintptr(TOKEN_DUPLICATE|TOKEN_QUERY|TOKEN_ADJUST_DEFAULT|TOKEN_ASSIGN_PRIMARY),
		uintptr(unsafe.Pointer(&processToken)),
	)
	if ret == 0 {
		return fmt.Errorf("OpenProcessToken failed: %v", err)
	}
	defer syscall.CloseHandle(syscall.Handle(processToken))

	// Duplicate the token as a primary token
	var newToken syscall.Token
	ret, _, err = procDuplicateTokenEx.Call(
		uintptr(processToken),
		uintptr(TOKEN_ALL_ACCESS),
		0, // default security attributes
		uintptr(SecurityImpersonation),
		uintptr(TokenPrimary),
		uintptr(unsafe.Pointer(&newToken)),
	)
	if ret == 0 {
		return fmt.Errorf("DuplicateTokenEx failed: %v", err)
	}

	// Build the Low Integrity SID: S-1-16-4096
	var lowSid *syscall.SID
	sidStr := "S-1-16-4096"
	sidPtr, err := syscall.UTF16PtrFromString(sidStr)
	if err != nil {
		syscall.CloseHandle(syscall.Handle(newToken))
		return fmt.Errorf("UTF16PtrFromString failed: %v", err)
	}

	err = convertStringSidToSid(sidPtr, &lowSid)
	if err != nil {
		syscall.CloseHandle(syscall.Handle(newToken))
		return fmt.Errorf("ConvertStringSidToSidW failed: %v", err)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(lowSid)))


	// Set the token integrity level to Low
	tml := TOKEN_MANDATORY_LABEL{
		Label: SID_AND_ATTRIBUTES{
			Sid:        uintptr(unsafe.Pointer(lowSid)),
			Attributes: 0x00000020, // SE_GROUP_INTEGRITY
		},
	}

	ret, _, err = procSetTokenInformation.Call(
		uintptr(newToken),
		uintptr(TokenIntegrityLevel),
		uintptr(unsafe.Pointer(&tml)),
		uintptr(unsafe.Sizeof(tml)),
	)
	if ret == 0 {
		syscall.CloseHandle(syscall.Handle(newToken))
		return fmt.Errorf("SetTokenInformation failed: %v", err)
	}

	// Assign the low-integrity token to the child process
	cmd.SysProcAttr.Token = newToken

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


func closeTokenHandle(cmd *exec.Cmd) {
	if cmd.SysProcAttr != nil && cmd.SysProcAttr.Token != 0 {
		syscall.CloseHandle(syscall.Handle(cmd.SysProcAttr.Token))
	}
}

