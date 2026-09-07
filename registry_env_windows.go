//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

// Registry constants for the two string types Windows uses for PATH, plus the
// access and notification bits the write below needs.
const (
	regSZ       = 1 // REG_SZ: the value is used exactly as stored
	regExpandSZ = 2 // REG_EXPAND_SZ: %VAR% in the value is expanded when read

	hkeyCurrentUser = 0x80000001
	keyWrite        = 0x20006
	keySetValue     = 0x0002

	wmSettingChange = 0x001A
	hwndBroadcast   = 0xFFFF
	smtoAbortIfHung = 0x0002
)

// advapi32 is already loaded in sandbox_windows.go; these are the registry
// entry points that file does not use.
var (
	procRegCreateKeyExW = modAdvapi32.NewProc("RegCreateKeyExW")
	procRegSetValueExW  = modAdvapi32.NewProc("RegSetValueExW")
	procRegCloseKey     = modAdvapi32.NewProc("RegCloseKey")

	modUser32               = syscall.NewLazyDLL("user32.dll")
	procSendMessageTimeoutW = modUser32.NewProc("SendMessageTimeoutW")
)

// setRegistryStringValue writes a string value under HKEY_CURRENT_USER,
// choosing REG_EXPAND_SZ or REG_SZ.
//
// The type is the point. `[Environment]::SetEnvironmentVariable(..., 'User')`,
// which this replaces, always writes REG_SZ, and Windows ships the User PATH as
// REG_EXPAND_SZ. So `nvx doctor --fix` converted the type on every machine it
// touched, and any entry written as %USERPROFILE%\bin or %JAVA_HOME%\bin
// stopped being expanded and silently stopped resolving. Nothing about the
// repair needed that conversion; it was a property of the API reached for.
func setRegistryStringValue(subkey, name, value string, expand bool) error {
	keyPath, err := syscall.UTF16PtrFromString(subkey)
	if err != nil {
		return err
	}
	var handle syscall.Handle
	var disposition uint32
	r, _, _ := procRegCreateKeyExW.Call(
		uintptr(hkeyCurrentUser),
		uintptr(unsafe.Pointer(keyPath)),
		0, 0, 0,
		uintptr(keyWrite|keySetValue),
		0,
		uintptr(unsafe.Pointer(&handle)),
		uintptr(unsafe.Pointer(&disposition)),
	)
	if r != 0 {
		return fmt.Errorf("open HKCU\\%s: %w", subkey, syscall.Errno(r))
	}
	defer procRegCloseKey.Call(uintptr(handle))

	valueName, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	data, err := syscall.UTF16FromString(value)
	if err != nil {
		return err
	}
	valueType := uintptr(regSZ)
	if expand {
		valueType = uintptr(regExpandSZ)
	}
	// Length in BYTES, including the terminating NUL that UTF16FromString adds.
	// A length that omits it leaves the value unterminated and readers see
	// trailing rubbish.
	r, _, _ = procRegSetValueExW.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(valueName)),
		0,
		valueType,
		uintptr(unsafe.Pointer(&data[0])),
		uintptr(len(data)*2),
	)
	if r != 0 {
		return fmt.Errorf("set HKCU\\%s\\%s: %w", subkey, name, syscall.Errno(r))
	}
	return nil
}

// broadcastEnvironmentChange tells running programs the environment moved.
//
// The .NET setter did this as part of its own work, so dropping it for a direct
// registry write would have been a quiet regression: without the broadcast,
// Explorer keeps handing its stale environment to everything launched from it
// until the next sign-in, and the repair looks like it did nothing.
//
// Best-effort by design. The value is already written at this point, and a
// window that will not answer within the timeout is not a reason to report the
// repair as failed.
func broadcastEnvironmentChange() {
	env, err := syscall.UTF16PtrFromString("Environment")
	if err != nil {
		return
	}
	var result uintptr
	_, _, _ = procSendMessageTimeoutW.Call(
		uintptr(hwndBroadcast),
		uintptr(wmSettingChange),
		0,
		uintptr(unsafe.Pointer(env)),
		uintptr(smtoAbortIfHung),
		5000,
		uintptr(unsafe.Pointer(&result)),
	)
}
