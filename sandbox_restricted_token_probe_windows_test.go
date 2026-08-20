//go:build windows

package main

import (
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// Does a Win32 restricted token restrict the network? Measured, not assumed.
//
// `IMPLEMENTATION_PLAN.md` proposes replacing the AppContainer sandbox with
// restricted tokens to fix named-pipe streaming and inbound dev-server loopback.
// `docs/plan-review-2026-08-21.md` argues the trade removes OS-enforced egress,
// because AppContainer's guarantee comes from Windows filtering by package SID
// and restricted tokens gate access checks on securable objects instead -- and
// then flags that as the one load-bearing claim in the review that was reasoned
// from mechanism rather than measured. This measures it.
//
// Baseline for comparison, already measured elsewhere in this suite: inside an
// AppContainer with no network capability, direct TCP gives EACCES and DNS gives
// ENOTFOUND.
//
// The interesting subtlety is that these may not move together. Outbound TCP to a
// literal IP involves no securable object, so a restricted token has nothing to
// deny. DNS goes through the DNS Client service over ALPC, and an ALPC port IS
// securable -- so a heavily restricted token could plausibly lose name resolution
// while keeping raw connectivity. That would look like partial containment and be
// none: an attacker hard-codes an address.
func TestRestrictedTokenNetworkBehaviour(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (creates restricted tokens and makes outbound connections)")
	}
	if os.Getenv("NVX_RESTRICTED_CHILD") == "1" {
		runRestrictedTokenChild()
		os.Exit(0)
	}

	cases := []struct {
		name  string
		flags uint32
		// restrictSIDs are added as restricting SIDs, which narrow every access
		// check to what BOTH the token and this list allow.
		restrictSIDs []string
	}{
		{"DISABLE_MAX_PRIVILEGE (the plan's proposal)", disableMaxPrivilege, nil},
		{"LUA_TOKEN", luaToken, nil},
		{"WRITE_RESTRICTED + RESTRICTED_CODE", writeRestricted, []string{"S-1-5-12"}},
		{"restricting SID only (RESTRICTED_CODE)", 0, []string{"S-1-5-12"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runUnderRestrictedToken(t, tc.flags, tc.restrictSIDs)
			if err != nil {
				t.Skipf("could not launch under a restricted token here: %v", err)
			}
			t.Logf("child report:\n%s", out)

			switch {
			case strings.Contains(out, "tcp=CONNECTED"):
				t.Logf("RESULT: outbound TCP SUCCEEDS under this token. A restricted token does not " +
					"reproduce AppContainer's egress guarantee, so swapping the primitive would make the " +
					"allowlist cooperative again.")
			case processDidNotSurvive(out):
				// Distinguished deliberately. "The connection failed" and "the
				// process could not start" look identical in a pass/fail check and
				// mean opposite things: the first would be containment, the second
				// is a token so tight the runtime cannot initialise -- no use for
				// running npm.
				t.Logf("RESULT: outbound TCP failed, but so did loading Windows networking itself. This " +
					"token breaks the process rather than containing it; it is not evidence of enforced egress.")
			case strings.Contains(out, "tcp=BLOCKED"):
				t.Logf("RESULT: outbound TCP is BLOCKED under this token -- worth pursuing; confirm it is " +
					"the token doing it and not a local firewall before relying on it.")
			default:
				t.Logf("RESULT: inconclusive for this token shape")
			}
		})
	}
}

// Win32 CreateRestrictedToken flags (winnt.h).
const (
	disableMaxPrivilege = 0x1
	sandboxInert        = 0x2
	luaToken            = 0x4
	writeRestricted     = 0x8
)

var procCreateRestrictedToken = modAdvapi32.NewProc("CreateRestrictedToken")

// runUnderRestrictedToken launches this test binary as a child under a restricted
// token derived from the current process token, and returns what it reported.
func runUnderRestrictedToken(t *testing.T, flags uint32, restrictSIDs []string) (string, error) {
	t.Helper()

	var self syscall.Token
	proc, _, _ := procGetCurrentProcess.Call()
	if r, _, err := procOpenProcessToken.Call(
		proc,
		uintptr(TOKEN_DUPLICATE|TOKEN_QUERY|TOKEN_ADJUST_DEFAULT|TOKEN_ASSIGN_PRIMARY),
		uintptr(unsafe.Pointer(&self)),
	); r == 0 {
		return "", fmt.Errorf("OpenProcessToken: %v", err)
	}
	defer syscall.CloseHandle(syscall.Handle(self))

	// NOT buildCapabilitySIDAttrs: that sets SE_GROUP_ENABLED, and
	// CreateRestrictedToken documents that a restricting SID's Attributes member
	// must be zero. Passing the capability form fails with "The parameter is
	// incorrect", which reads like a bad SID rather than a bad attribute.
	attrs, freeSIDs, err := buildRestrictingSIDAttrs(restrictSIDs)
	if err != nil {
		return "", fmt.Errorf("restricting SIDs: %w", err)
	}
	defer freeSIDs()

	var restrictPtr uintptr
	if len(attrs) > 0 {
		restrictPtr = uintptr(unsafe.Pointer(&attrs[0]))
	}

	var restricted syscall.Token
	r, _, callErr := procCreateRestrictedToken.Call(
		uintptr(self),
		uintptr(flags),
		0, 0, // no SIDs to disable
		0, 0, // no privileges to delete beyond the flag
		uintptr(len(attrs)), restrictPtr,
		uintptr(unsafe.Pointer(&restricted)),
	)
	if r == 0 {
		return "", fmt.Errorf("CreateRestrictedToken: %v", callErr)
	}
	defer syscall.CloseHandle(syscall.Handle(restricted))

	exe, err := os.Executable()
	if err != nil {
		return "", err
	}

	read, write := makeTestPipe(t)
	defer syscall.CloseHandle(read)
	// makeTestPipe creates the pipe with InheritHandle=0, and the AppContainer
	// launcher normally fixes that via prepareInheritableStdio. This path does not
	// go through it, so the handle has to be marked here -- without it the child
	// runs and its output goes nowhere, which reads as "the probe produced no
	// result" rather than as a plumbing mistake.
	if err := markHandleInheritable(write); err != nil {
		return "", fmt.Errorf("mark pipe inheritable: %w", err)
	}
	prevOut, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	const stdOutputHandle = uintptr(0xFFFFFFF5)
	procSetStdHandleTest.Call(stdOutputHandle, uintptr(write))

	env := append(os.Environ(), "NVX_PROBE=1", "NVX_RESTRICTED_CHILD=1")
	// A plain CreateProcessAsUser, NOT launchAppContainerProcess: that one always
	// attaches a PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITIES list, and with no
	// AppContainer SID to put in it the call fails with "The parameter is
	// incorrect". The point here is a token with no AppContainer at all.
	launchErr := launchWithTokenOnly(restricted, exe,
		[]string{"-test.run=TestRestrictedTokenNetworkBehaviour"}, env, write)

	procSetStdHandleTest.Call(stdOutputHandle, uintptr(prevOut))
	syscall.CloseHandle(write)
	out := readWithTimeout(t, read)

	if launchErr != nil {
		return out, launchErr
	}
	return out, nil
}

// processDidNotSurvive spots the failure mode where the token stopped the
// runtime from starting rather than stopping it from reaching the network:
// WSANOTINITIALISED (10093) because Winsock never came up, or a panic loading
// iphlpapi.dll.
func processDidNotSurvive(out string) bool {
	return strings.Contains(out, "winapi error #10093") ||
		strings.Contains(out, "Failed to load iphlpapi.dll")
}

// runRestrictedTokenChild reports what it can reach. Printed rather than written
// to a file: whether the token can write anywhere is itself in question.
func runRestrictedTokenChild() {
	// A literal IP, so this is connectivity rather than name resolution.
	if c, err := net.DialTimeout("tcp", "1.1.1.1:443", 6*time.Second); err == nil {
		_ = c.Close()
		fmt.Println("tcp=CONNECTED 1.1.1.1:443")
	} else {
		fmt.Printf("tcp=BLOCKED %v\n", err)
	}

	// Separately, because the two can diverge -- see the note on ALPC above.
	if addrs, err := net.LookupHost("example.com"); err == nil && len(addrs) > 0 {
		fmt.Printf("dns=RESOLVED %s\n", addrs[0])
	} else {
		fmt.Printf("dns=BLOCKED %v\n", err)
	}

	// One filesystem probe, to show which half of containment the token provides.
	home, _ := os.UserHomeDir()
	if _, err := os.ReadFile(home + `\.npmrc`); err == nil {
		fmt.Println("read-npmrc=OK")
	} else if os.IsNotExist(err) {
		fmt.Println("read-npmrc=ABSENT (no file to read here)")
	} else {
		fmt.Printf("read-npmrc=BLOCKED %v\n", err)
	}
}

// buildRestrictingSIDAttrs builds a SID_AND_ATTRIBUTES array for
// CreateRestrictedToken's SidsToRestrict, whose Attributes member must be zero.
func buildRestrictingSIDAttrs(sidStrings []string) ([]SID_AND_ATTRIBUTES, func(), error) {
	var attrs []SID_AND_ATTRIBUTES
	var sids []*syscall.SID
	free := func() {
		for _, s := range sids {
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
			return nil, nil, fmt.Errorf("convert restricting SID %s: %w", str, err)
		}
		sids = append(sids, sid)
		attrs = append(attrs, SID_AND_ATTRIBUTES{Sid: uintptr(unsafe.Pointer(sid)), Attributes: 0})
	}
	return attrs, free, nil
}

// launchWithTokenOnly runs cmdPath under the given token and waits for it,
// wiring stdout to the supplied pipe handle.
func launchWithTokenOnly(token syscall.Token, cmdPath string, args, env []string, stdout syscall.Handle) error {
	cmdLine, err := syscall.UTF16FromString(buildWindowsCommandLine(cmdPath, args))
	if err != nil {
		return err
	}
	appName, err := syscall.UTF16PtrFromString(cmdPath)
	if err != nil {
		return err
	}
	envBlock, err := buildWindowsEnvironmentBlock(env)
	if err != nil {
		return err
	}

	var si syscall.StartupInfo
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Flags = STARTF_USESTDHANDLES
	si.StdOutput = stdout
	si.StdErr = stdout
	if in, gerr := syscall.GetStdHandle(syscall.STD_INPUT_HANDLE); gerr == nil {
		si.StdInput = in
	}

	var pi processInformation
	ok, _, callErr := procCreateProcessAsUserW.Call(
		uintptr(token),
		uintptr(unsafe.Pointer(appName)),
		uintptr(unsafe.Pointer(&cmdLine[0])),
		0, 0,
		1, // bInheritHandles
		uintptr(CREATE_UNICODE_ENVIRONMENT),
		uintptr(unsafe.Pointer(envBlock)),
		0,
		uintptr(unsafe.Pointer(&si)),
		uintptr(unsafe.Pointer(&pi)),
	)
	if ok == 0 {
		return fmt.Errorf("CreateProcessAsUser(restricted token): %w", callErr)
	}
	defer syscall.CloseHandle(pi.hThread)
	defer syscall.CloseHandle(pi.hProcess)

	if _, werr := syscall.WaitForSingleObject(pi.hProcess, 30000); werr != nil {
		return fmt.Errorf("wait: %w", werr)
	}
	return nil
}
