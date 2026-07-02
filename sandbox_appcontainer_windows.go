//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

const (
	appContainerName = "nvx.sandbox"
)

var (
	modUserenv = syscall.NewLazyDLL("userenv.dll")

	procCreateAppContainerProfile                 = modUserenv.NewProc("CreateAppContainerProfile")
	procDeriveAppContainerSidFromAppContainerName = modUserenv.NewProc("DeriveAppContainerSidFromAppContainerName")
)

// prepareAppContainerFilesystem grants the AppContainer SID write access to
// guestHome and workDir and applies Low integrity labels so stacked Low IL
// tokens can write there as well.
func prepareAppContainerFilesystem(sid uintptr, guestHome, workDir string) error {
	for _, dir := range []string{guestHome, workDir} {
		if dir == "" {
			continue
		}
		if err := grantAppContainerPath(sid, dir); err != nil {
			return err
		}
		if err := labelLowIntegrity(dir); err != nil {
			return fmt.Errorf("integrity label for %q: %w", dir, err)
		}
	}
	return nil
}

func ensureAppContainerSID() (uintptr, error) {
	name, err := syscall.UTF16PtrFromString(appContainerName)
	if err != nil {
		return 0, err
	}

	var sid uintptr
	hr, _, _ := procDeriveAppContainerSidFromAppContainerName.Call(
		uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(&sid)),
	)
	if hr == 0 && sid != 0 {
		return sid, nil
	}

	display, _ := syscall.UTF16PtrFromString("nvx sandbox")
	desc, _ := syscall.UTF16PtrFromString("Ephemeral nvx execution sandbox")
	var newSid uintptr
	hr, _, callErr := procCreateAppContainerProfile.Call(
		uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(display)),
		uintptr(unsafe.Pointer(desc)),
		0, 0,
		uintptr(unsafe.Pointer(&newSid)),
	)
	if hr == 0 && newSid != 0 {
		return newSid, nil
	}

	// 0x80073D10 = HRESULT_FROM_WIN32(ERROR_ALREADY_EXISTS)
	if hr == 0x80073D10 {
		hr, _, callErr = procDeriveAppContainerSidFromAppContainerName.Call(
			uintptr(unsafe.Pointer(name)),
			uintptr(unsafe.Pointer(&sid)),
		)
		if hr == 0 && sid != 0 {
			return sid, nil
		}
	}

	return 0, fmt.Errorf("DeriveAppContainerSid failed (hr=0x%X): %v", hr, callErr)
}

func grantAppContainerPath(sid uintptr, path string) error {
	sidStr, err := appContainerSidToString(sid)
	if err != nil {
		return err
	}
	grantArg := fmt.Sprintf("*%s:(OI)(CI)(M)", sidStr)
	out, err := exec.Command("icacls", path, "/grant", grantArg, "/t", "/c", "/q").CombinedOutput()
	if err != nil {
		return fmt.Errorf("icacls grant for AppContainer: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// grantAppContainerExecutable grants read/execute on the sandboxed binary and its directory.
func grantAppContainerExecutable(sid uintptr, cmdPath string) error {
	if cmdPath == "" {
		return nil
	}
	cmdPath = filepath.Clean(cmdPath)
	dir := filepath.Dir(cmdPath)
	if err := grantAppContainerPathReadExec(sid, dir); err != nil {
		return err
	}
	if dir != cmdPath {
		if err := grantAppContainerPathReadExec(sid, cmdPath); err != nil {
			return err
		}
	}
	return nil
}

func grantAppContainerPathReadExec(sid uintptr, path string) error {
	sidStr, err := appContainerSidToString(sid)
	if err != nil {
		return err
	}
	grantArg := fmt.Sprintf("*%s:(RX)", sidStr)
	out, err := exec.Command("icacls", path, "/grant", grantArg, "/c", "/q").CombinedOutput()
	if err != nil {
		return fmt.Errorf("icacls RX grant for AppContainer: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func appContainerSidToString(sid uintptr) (string, error) {
	var strPtr *uint16
	ret, _, err := modAdvapi32.NewProc("ConvertSidToStringSidW").Call(
		sid,
		uintptr(unsafe.Pointer(&strPtr)),
	)
	if ret == 0 {
		return "", fmt.Errorf("ConvertSidToStringSidW: %v", err)
	}
	defer syscall.LocalFree(syscall.Handle(unsafe.Pointer(strPtr)))
	return utf16PtrToString(strPtr), nil
}

func utf16PtrToString(p *uint16) string {
	if p == nil {
		return ""
	}
	var chars []uint16
	for i := 0; ; i++ {
		c := *(*uint16)(unsafe.Pointer(uintptr(unsafe.Pointer(p)) + uintptr(i*2)))
		if c == 0 {
			break
		}
		chars = append(chars, c)
	}
	return syscall.UTF16ToString(chars)
}
