//go:build windows
// +build windows

package main

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

var (
	modAdvapi32                           = syscall.NewLazyDLL("advapi32.dll")
	modKernel32                           = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcessToken                  = modAdvapi32.NewProc("OpenProcessToken")
	procDuplicateTokenEx                  = modAdvapi32.NewProc("DuplicateTokenEx")
	procSetTokenInformation               = modAdvapi32.NewProc("SetTokenInformation")
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
	TOKEN_DUPLICATE            = 0x0002
	TOKEN_QUERY                = 0x0008
	TOKEN_ADJUST_DEFAULT       = 0x0080
	TOKEN_ASSIGN_PRIMARY       = 0x0001
	TOKEN_ALL_ACCESS           = 0xF01FF
	SecurityImpersonation      = 2
	TokenPrimary               = 1
	TokenIntegrityLevel        = 25
	SECURITY_MANDATORY_LOW_RID = 0x1000

	EXTENDED_STARTUPINFO_PRESENT                = 0x00080000
	CREATE_UNICODE_ENVIRONMENT                  = 0x00000400
	STARTF_USESTDHANDLES                        = 0x00000100
	CREATE_BREAKAWAY_FROM_JOB                   = 0x01000000
	PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITIES = 0x20009
	INFINITE                                    = 0xFFFFFFFF
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

// applySandboxIsolation is unused on the hardened native path (see
// applyWindowsNativeIsolation). Kept as a no-op stub for any legacy callers.
func applySandboxIsolation(cmd *exec.Cmd, guestHome string) {
	_ = cmd
	_ = guestHome
}

// labelLowIntegrity applies a Low mandatory integrity label (inherited by
// children) to the given directory so Low IL processes can write to it.
func labelLowIntegrity(dir string) error {
	out, err := exec.Command("icacls", dir, "/setintegritylevel", "(OI)(CI)Low", "/t", "/c", "/q").CombinedOutput()
	if err != nil {
		return fmt.Errorf("icacls failed: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// tryApplyLowIntegrity assigns a duplicated Low IL primary token to cmd.
func tryApplyLowIntegrity(cmd *exec.Cmd) error {
	token, err := createLowIntegrityPrimaryToken()
	if err != nil {
		return err
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
		Token:         token,
	}
	return nil
}

// createLowIntegrityPrimaryToken duplicates the current process token and
// lowers its mandatory integrity level to Low (S-1-16-4096).
func createLowIntegrityPrimaryToken() (syscall.Token, error) {
	var processToken syscall.Token
	currentProcess, _, _ := procGetCurrentProcess.Call()
	ret, _, err := procOpenProcessToken.Call(
		currentProcess,
		uintptr(TOKEN_DUPLICATE|TOKEN_QUERY|TOKEN_ADJUST_DEFAULT|TOKEN_ASSIGN_PRIMARY),
		uintptr(unsafe.Pointer(&processToken)),
	)
	if ret == 0 {
		return 0, fmt.Errorf("OpenProcessToken failed: %v", err)
	}
	defer syscall.CloseHandle(syscall.Handle(processToken))

	var newToken syscall.Token
	ret, _, err = procDuplicateTokenEx.Call(
		uintptr(processToken),
		uintptr(TOKEN_ALL_ACCESS),
		0,
		uintptr(SecurityImpersonation),
		uintptr(TokenPrimary),
		uintptr(unsafe.Pointer(&newToken)),
	)
	if ret == 0 {
		return 0, fmt.Errorf("DuplicateTokenEx failed: %v", err)
	}

	if err := applyLowIntegrityToToken(newToken); err != nil {
		syscall.CloseHandle(syscall.Handle(newToken))
		return 0, err
	}
	return newToken, nil
}

// applyLowIntegrityToToken lowers the mandatory integrity level on an existing token.
func applyLowIntegrityToToken(token syscall.Token) error {
	var lowSid *syscall.SID
	sidPtr, err := syscall.UTF16PtrFromString("S-1-16-4096")
	if err != nil {
		return fmt.Errorf("UTF16PtrFromString failed: %v", err)
	}

	err = convertStringSidToSid(sidPtr, &lowSid)
	if err != nil {
		return fmt.Errorf("ConvertStringSidToSidW failed: %v", err)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(lowSid)))

	tml := TOKEN_MANDATORY_LABEL{
		Label: SID_AND_ATTRIBUTES{
			Sid:        uintptr(unsafe.Pointer(lowSid)),
			Attributes: 0x00000020, // SE_GROUP_INTEGRITY
		},
	}

	ret, _, err := procSetTokenInformation.Call(
		uintptr(token),
		uintptr(TokenIntegrityLevel),
		uintptr(unsafe.Pointer(&tml)),
		uintptr(unsafe.Sizeof(tml)),
	)
	if ret == 0 {
		return fmt.Errorf("SetTokenInformation(integrity): %v", err)
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

func closeTokenHandle(cmd *exec.Cmd) {
	if cmd.SysProcAttr != nil && cmd.SysProcAttr.Token != 0 {
		syscall.CloseHandle(syscall.Handle(cmd.SysProcAttr.Token))
	}
}
