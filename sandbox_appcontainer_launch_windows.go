//go:build windows

package main

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

type startupInfoEx struct {
	syscall.StartupInfo
	lpAttributeList uintptr
}

type securityCapabilities struct {
	appContainerSid uintptr
	capabilities    uintptr
	capabilityCount uint32
	reserved        uint32
}

type processInformation struct {
	hProcess    syscall.Handle
	hThread     syscall.Handle
	dwProcessID uint32
	dwThreadID  uint32
}

// launchAppContainerProcess starts cmdPath with AppContainer isolation via
// CreateProcessAsUserW + PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITIES. When
// lowILToken is non-zero, Low IL is stacked on the AppContainer boundary.
func launchAppContainerProcess(
	cmdPath string,
	args []string,
	env []string,
	workDir string,
	appContainerSID uintptr,
	lowILToken syscall.Token,
) (exitCode int, err error) {
	attrBuf, attrList, err := initProcThreadAttributeList(1)
	if err != nil {
		return 1, err
	}
	defer deleteProcThreadAttributeList(attrList)

	secCaps := securityCapabilities{
		appContainerSid: appContainerSID,
		capabilityCount: 0,
	}
	if err := updateProcThreadAttribute(
		attrList,
		PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITIES,
		unsafe.Pointer(&secCaps),
		unsafe.Sizeof(secCaps),
	); err != nil {
		return 1, err
	}

	stdin, err := syscall.GetStdHandle(syscall.STD_INPUT_HANDLE)
	if err != nil {
		return 1, fmt.Errorf("stdin handle: %w", err)
	}
	stdout, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	if err != nil {
		return 1, fmt.Errorf("stdout handle: %w", err)
	}
	stderr, err := syscall.GetStdHandle(syscall.STD_ERROR_HANDLE)
	if err != nil {
		return 1, fmt.Errorf("stderr handle: %w", err)
	}

	var si startupInfoEx
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Flags = STARTF_USESTDHANDLES
	si.StdInput = stdin
	si.StdOutput = stdout
	si.StdErr = stderr
	si.lpAttributeList = attrList

	cmdLine := buildWindowsCommandLine(cmdPath, args)
	cmdLineUTF16, err := syscall.UTF16FromString(cmdLine)
	if err != nil {
		return 1, fmt.Errorf("command line: %w", err)
	}

	appName, err := syscall.UTF16PtrFromString(cmdPath)
	if err != nil {
		return 1, fmt.Errorf("application name: %w", err)
	}

	var workDirPtr *uint16
	if workDir != "" {
		workDirPtr, err = syscall.UTF16PtrFromString(workDir)
		if err != nil {
			return 1, fmt.Errorf("working directory: %w", err)
		}
	}

	envBlock, err := buildWindowsEnvironmentBlock(env)
	if err != nil {
		return 1, err
	}

	creationFlags := uintptr(
		EXTENDED_STARTUPINFO_PRESENT |
			CREATE_UNICODE_ENVIRONMENT |
			syscall.CREATE_NEW_PROCESS_GROUP,
	)

	var pi processInformation
	var createOK uintptr
	var createErr error
	// lpApplicationName NULL — executable is the first token in lpCommandLine.
	if lowILToken != 0 {
		createOK, _, createErr = procCreateProcessAsUserW.Call(
			uintptr(lowILToken),
			uintptr(unsafe.Pointer(appName)),
			uintptr(unsafe.Pointer(&cmdLineUTF16[0])),
			0, 0,
			1, // inherit std handles
			creationFlags,
			uintptr(unsafe.Pointer(envBlock)),
			uintptr(unsafe.Pointer(workDirPtr)),
			uintptr(unsafe.Pointer(&si)),
			uintptr(unsafe.Pointer(&pi)),
		)
	} else {
		createOK, _, createErr = procCreateProcessW.Call(
			uintptr(unsafe.Pointer(appName)),
			uintptr(unsafe.Pointer(&cmdLineUTF16[0])),
			0, 0,
			1,
			creationFlags,
			uintptr(unsafe.Pointer(envBlock)),
			uintptr(unsafe.Pointer(workDirPtr)),
			uintptr(unsafe.Pointer(&si)),
			uintptr(unsafe.Pointer(&pi)),
		)
	}

	// Keep attribute buffer alive through CreateProcess.
	_ = attrBuf

	if createOK == 0 {
		return 1, fmt.Errorf("CreateProcess(AppContainer) exe=%q cwd=%q: %v", cmdPath, workDir, createErr)
	}
	defer syscall.CloseHandle(pi.hProcess)
	defer syscall.CloseHandle(pi.hThread)

	waitRet, _, waitErr := procWaitForSingleObject.Call(uintptr(pi.hProcess), INFINITE)
	if waitRet == 0xFFFFFFFF {
		return 1, fmt.Errorf("WaitForSingleObject: %w", waitErr)
	}

	var code uint32
	ret, _, exitErr := procGetExitCodeProcess.Call(
		uintptr(pi.hProcess),
		uintptr(unsafe.Pointer(&code)),
	)
	if ret == 0 {
		return 1, fmt.Errorf("GetExitCodeProcess: %w", exitErr)
	}
	return int(code), nil
}

func initProcThreadAttributeList(count uint32) ([]byte, uintptr, error) {
	var size uintptr
	ret, _, err := procInitializeProcThreadAttributeList.Call(
		0, uintptr(count), 0, uintptr(unsafe.Pointer(&size)),
	)
	if ret != 0 || size == 0 {
		return nil, 0, fmt.Errorf("InitializeProcThreadAttributeList(size): %v", err)
	}

	buf := make([]byte, size)
	list := uintptr(unsafe.Pointer(&buf[0]))
	ret, _, err = procInitializeProcThreadAttributeList.Call(
		list, uintptr(count), 0, uintptr(unsafe.Pointer(&size)),
	)
	if ret == 0 {
		return nil, 0, fmt.Errorf("InitializeProcThreadAttributeList: %v", err)
	}
	return buf, list, nil
}

func updateProcThreadAttribute(list uintptr, attr uintptr, value unsafe.Pointer, size uintptr) error {
	ret, _, err := procUpdateProcThreadAttribute.Call(
		list,
		0,
		attr,
		uintptr(value),
		size,
		0,
		0,
	)
	if ret == 0 {
		return fmt.Errorf("UpdateProcThreadAttribute: %v", err)
	}
	return nil
}

func deleteProcThreadAttributeList(list uintptr) {
	if list != 0 {
		procDeleteProcThreadAttributeList.Call(list)
	}
}

func buildWindowsCommandLine(exePath string, args []string) string {
	all := append([]string{exePath}, args...)
	return buildWindowsArgString(all)
}

func buildWindowsArgString(args []string) string {
	var b strings.Builder
	for i, arg := range args {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(quoteWindowsArg(arg))
	}
	return b.String()
}

func quoteWindowsArg(s string) string {
	if s == "" {
		return `""`
	}
	needsQuotes := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ' ', '\t', '"':
			needsQuotes = true
		}
	}
	if !needsQuotes {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			slashes++
		case '"':
			for ; slashes > 0; slashes-- {
				b.WriteByte('\\')
			}
			b.WriteString(`\"`)
		default:
			for ; slashes > 0; slashes-- {
				b.WriteByte('\\')
			}
			b.WriteByte(s[i])
		}
	}
	for ; slashes > 0; slashes-- {
		b.WriteByte('\\')
	}
	b.WriteByte('"')
	return b.String()
}

func buildWindowsEnvironmentBlock(env []string) (*uint16, error) {
	if len(env) == 0 {
		return nil, nil
	}
	var size int
	for _, entry := range env {
		size += len(entry) + 1
	}
	block := make([]uint16, size+1)
	offset := 0
	for _, entry := range env {
		for _, r := range entry {
			block[offset] = uint16(r)
			offset++
		}
		offset++
	}
	return &block[0], nil
}
