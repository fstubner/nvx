//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"
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

// Well-known capability SIDs (see winnt.h / app-capability docs).
const (
	capabilityInternetClientSID             = "S-1-15-3-1"
	capabilityPrivateNetworkClientServerSID = "S-1-15-3-3"
	seGroupEnabled                          = 0x00000004
)

// buildCapabilitySIDAttrs converts capability SID strings into an enabled
// SID_AND_ATTRIBUTES array for a securityCapabilities struct. The returned free
// func releases the allocated SIDs and must be called after CreateProcess.
func buildCapabilitySIDAttrs(sidStrings []string) ([]SID_AND_ATTRIBUTES, func(), error) {
	var attrs []SID_AND_ATTRIBUTES
	var sids []*syscall.SID
	free := func() {
		for _, s := range sids {
			// #nosec G104 -- LocalFree gives the caller nothing actionable, and cleanup must continue for every SID
			procLocalFree.Call(uintptr(unsafe.Pointer(s)))
		}
	}
	for _, str := range sidStrings {
		p, err := syscall.UTF16PtrFromString(str)
		if err != nil {
			free()
			return nil, nil, err
		}
		var sid *syscall.SID
		if err := convertStringSidToSid(p, &sid); err != nil {
			free()
			return nil, nil, fmt.Errorf("convert capability SID %s: %w", str, err)
		}
		sids = append(sids, sid)
		attrs = append(attrs, SID_AND_ATTRIBUTES{
			Sid:        uintptr(unsafe.Pointer(sid)),
			Attributes: seGroupEnabled,
		})
	}
	return attrs, free, nil
}

// launchAppContainerProcess starts cmdPath with AppContainer isolation via
// CreateProcessAsUserW + PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITIES. The
// lowILToken path is retained for legacy callers, but native launch passes 0.
// capabilitySIDs grants AppContainer capabilities (e.g. internetClient).
func launchAppContainerProcess(
	cmdPath string,
	args []string,
	env []string,
	workDir string,
	appContainerSID uintptr,
	lowILToken syscall.Token,
	capabilitySIDs []string,
) (exitCode int, err error) {
	exitCode, err = launchAppContainerProcessOnce(cmdPath, args, env, workDir, appContainerSID, lowILToken, capabilitySIDs)
	if err == nil || !isCreateProcessMissingFile(err) {
		return exitCode, err
	}

	sysRoot := os.Getenv("SystemRoot")
	if sysRoot == "" {
		sysRoot = `C:\Windows`
	}
	cmdExe := filepath.Join(sysRoot, "System32", "cmd.exe")
	if grantErr := grantAppContainerPathReadExec(appContainerSID, cmdExe); grantErr != nil {
		return exitCode, err
	}
	wrapped := append([]string{"/c", cmdPath}, args...)
	return launchAppContainerProcessOnce(cmdExe, wrapped, env, workDir, appContainerSID, lowILToken, capabilitySIDs)
}

func isCreateProcessMissingFile(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "cannot find the file") || strings.Contains(msg, "the system cannot find")
}

func launchAppContainerProcessOnce(
	cmdPath string,
	args []string,
	env []string,
	workDir string,
	appContainerSID uintptr,
	lowILToken syscall.Token,
	capabilitySIDs []string,
) (exitCode int, err error) {
	attrBuf, attrList, err := initProcThreadAttributeList(1)
	if err != nil {
		return 1, err
	}
	defer deleteProcThreadAttributeList(attrList)

	capAttrs, freeCaps, err := buildCapabilitySIDAttrs(capabilitySIDs)
	if err != nil {
		return 1, err
	}
	defer freeCaps()

	secCaps := securityCapabilities{
		appContainerSid: appContainerSID,
	}
	if len(capAttrs) > 0 {
		secCaps.capabilities = uintptr(unsafe.Pointer(&capAttrs[0]))
		// #nosec G115 -- capAttrs holds the capability SIDs nvx itself passes in, currently at most two
		secCaps.capabilityCount = uint32(len(capAttrs))
	}
	if err := updateProcThreadAttribute(
		attrList,
		PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITIES,
		unsafe.Pointer(&secCaps),
		unsafe.Sizeof(secCaps),
	); err != nil {
		return 1, err
	}

	var si startupInfoEx
	si.Cb = uint32(unsafe.Sizeof(si))
	si.lpAttributeList = attrList

	// Standard handles reach the child only when STARTF_USESTDHANDLES and
	// bInheritHandles are BOTH set; setting just one is worse than neither (the
	// child fails to start). See prepareInheritableStdio.
	stdio := prepareInheritableStdio()
	var inheritHandles uintptr
	if stdio.inheritable {
		si.Flags = STARTF_USESTDHANDLES
		si.StdInput = stdio.in
		si.StdOutput = stdio.out
		si.StdErr = stdio.err
		inheritHandles = 1
	} else {
		// Some console handles cannot be made inheritable (the original reason this
		// code passed FALSE). A console-attached child still inherits the console,
		// so interactive use works -- but a piped child may get nothing, so say so
		// rather than letting an MCP server fail mysteriously.
		//
		// "may not" rather than "will not": measured 2026-08-20 on Windows 11, a
		// contained child still received piped stdio with this fallback taken, in
		// both proxy and open mode. So the flat prediction was wrong on at least
		// one build. It is kept as a warning because F46 was a real measured
		// failure elsewhere -- the honest statement is that it might break, not
		// that it has.
		LogWarn("Standard handles are not inheritable here; a sandboxed process that communicates over pipes (e.g. an MCP server) may not receive stdio.")
	}

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
			CREATE_BREAKAWAY_FROM_JOB |
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
			inheritHandles,
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
			inheritHandles,
			creationFlags,
			uintptr(unsafe.Pointer(envBlock)),
			uintptr(unsafe.Pointer(workDirPtr)),
			uintptr(unsafe.Pointer(&si)),
			uintptr(unsafe.Pointer(&pi)),
		)
	}

	// Keep the attribute buffer and capability SID array alive through CreateProcess.
	_ = attrBuf
	_ = capAttrs

	if createOK == 0 {
		// %w, not %v: the caller distinguishes a corrupted staged image from other
		// launch failures, and must do it by error code. Matching the message text
		// would work only on an English Windows.
		return 1, fmt.Errorf("CreateProcess(AppContainer) exe=%q cwd=%q: %w", cmdPath, workDir, createErr)
	}
	defer func() {
		_ = syscall.CloseHandle(pi.hProcess)
	}()
	defer func() {
		_ = syscall.CloseHandle(pi.hThread)
	}()

	// The child was created with CREATE_BREAKAWAY_FROM_JOB (needed so a
	// restrictive CI job object doesn't block CreateProcess), so it starts with
	// no job membership at all. Assign it to a job of our own, configured to
	// kill everything in it the moment the job's last handle closes -- which
	// happens automatically if this process is killed before reaching
	// WaitForSingleObject below. Without this, a client that gives up on a slow
	// sandbox setup (e.g. an MCP client's connection timeout) and kills nvx
	// leaves the already-launched child running forever; this is what actually
	// reaps it, and job membership covers anything the child spawns too.
	// Best-effort: job objects are a defense-in-depth safety net, not a
	// containment guarantee, so a failure here logs rather than aborting a
	// command the user is waiting on.
	defer superviseProcessTree(pi.hProcess)()

	// Let the hangup watchdog end this wait. Terminating the child returns
	// control here so every deferred cleanup below and in runSandbox runs --
	// notably removing the guest profile, which an os.Exit from the watchdog
	// used to skip, leaving a directory behind per abandoned run.
	setActiveChildKiller(func() {
		procTerminateProcess.Call(uintptr(pi.hProcess), uintptr(exitParentHungUp))
	})
	defer setActiveChildKiller(nil)

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
		_, _, _ = procDeleteProcThreadAttributeList.Call(list)
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
		b.WriteString(`\\`)
	}
	b.WriteByte('"')
	return b.String()
}

// buildWindowsEnvironmentBlock renders env as the NUL-separated, double-NUL
// terminated UTF-16 block CreateProcess expects.
//
// The previous implementation wrote uint16(r) for each rune, which silently
// corrupted anything outside the BMP: a rune above U+FFFF was truncated to its low
// 16 bits, so U+1F600 (an emoji) became U+F600, a private-use character. It also
// sized the buffer from len(entry) -- a BYTE count -- while writing one unit per
// RUNE, so the two disagreed for any non-ASCII input. The existing test used only
// ASCII, where bytes and runes coincide and the truncation cannot show up.
//
// utf16.Encode emits a correct surrogate pair for non-BMP characters, and sizing
// follows the encoded result rather than being computed separately.
func buildWindowsEnvironmentBlock(env []string) (*uint16, error) {
	if len(env) == 0 {
		return nil, nil
	}
	var block []uint16
	for _, entry := range env {
		// A NUL inside an entry would terminate the block early and silently drop
		// every variable after it. Windows cannot represent one in an environment
		// value anyway, so refuse rather than truncate.
		if strings.ContainsRune(entry, 0) {
			return nil, fmt.Errorf("environment entry contains a NUL character: %q", entry)
		}
		block = append(block, utf16.Encode([]rune(entry))...)
		block = append(block, 0)
	}
	block = append(block, 0)
	return &block[0], nil
}
