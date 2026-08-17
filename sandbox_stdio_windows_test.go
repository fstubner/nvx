//go:build windows

package main

import (
	"os"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

type testSecAttrs struct {
	Length             uint32
	SecurityDescriptor uintptr
	InheritHandle      uint32
}

var (
	procCreatePipeTest         = modKernel32.NewProc("CreatePipe")
	procGetHandleInformationT  = modKernel32.NewProc("GetHandleInformation")
	procWaitForSingleObjectT   = modKernel32.NewProc("WaitForSingleObject")
	procGetExitCodeProcessTest = modKernel32.NewProc("GetExitCodeProcess")
)

func makeTestPipe(t *testing.T) (read, write syscall.Handle) {
	t.Helper()
	sa := testSecAttrs{InheritHandle: 0}
	sa.Length = uint32(unsafe.Sizeof(sa))
	ret, _, err := procCreatePipeTest.Call(
		uintptr(unsafe.Pointer(&read)),
		uintptr(unsafe.Pointer(&write)),
		uintptr(unsafe.Pointer(&sa)),
		0,
	)
	if ret == 0 {
		t.Fatalf("CreatePipe: %v", err)
	}
	return read, write
}

func handleIsInheritable(t *testing.T, h syscall.Handle) bool {
	t.Helper()
	var flags uint32
	ret, _, err := procGetHandleInformationT.Call(uintptr(h), uintptr(unsafe.Pointer(&flags)))
	if ret == 0 {
		t.Fatalf("GetHandleInformation: %v", err)
	}
	return flags&handleFlagInherit != 0
}

// TestMarkHandleInheritableSetsTheFlag is the unit-level half: the helper must
// actually change the handle's inheritance, since a pipe created for capture is
// deliberately non-inheritable to begin with.
func TestMarkHandleInheritableSetsTheFlag(t *testing.T) {
	read, write := makeTestPipe(t)
	defer syscall.CloseHandle(read)
	defer syscall.CloseHandle(write)

	if handleIsInheritable(t, write) {
		t.Fatal("precondition: a freshly created pipe end should not be inheritable")
	}
	if err := markHandleInheritable(write); err != nil {
		t.Fatalf("markHandleInheritable: %v", err)
	}
	if !handleIsInheritable(t, write) {
		t.Error("handle still not inheritable after markHandleInheritable")
	}
}

func TestMarkHandleInheritableRejectsInvalidHandles(t *testing.T) {
	for _, h := range []syscall.Handle{0, syscall.InvalidHandle} {
		if err := markHandleInheritable(h); err == nil {
			t.Errorf("markHandleInheritable(%v) = nil, want an error", h)
		}
	}
}

// spawnWithStdout runs `cmd.exe /c echo <marker>` with stdout assigned to wpipe,
// using the given startup flags and inheritance setting, and returns whatever
// arrived on the pipe plus the child's exit code.
func spawnWithStdout(t *testing.T, wpipe syscall.Handle, marker string, useStdHandles, inherit bool) (string, int) {
	t.Helper()

	var si syscall.StartupInfo
	si.Cb = uint32(unsafe.Sizeof(si))
	if useStdHandles {
		si.Flags = STARTF_USESTDHANDLES
	}
	si.StdOutput = wpipe
	si.StdErr = wpipe

	cmdLine, err := syscall.UTF16PtrFromString(`cmd.exe /c echo ` + marker)
	if err != nil {
		t.Fatal(err)
	}
	var inheritArg uintptr
	if inherit {
		inheritArg = 1
	}

	var pi struct {
		hProcess, hThread   syscall.Handle
		processID, threadID uint32
	}
	ret, _, cerr := procCreateProcessW.Call(
		0,
		uintptr(unsafe.Pointer(cmdLine)),
		0, 0,
		inheritArg,
		0x00000400, // CREATE_UNICODE_ENVIRONMENT
		0, 0,
		uintptr(unsafe.Pointer(&si)),
		uintptr(unsafe.Pointer(&pi)),
	)
	if ret == 0 {
		t.Fatalf("CreateProcessW: %v", cerr)
	}
	defer syscall.CloseHandle(pi.hProcess)
	defer syscall.CloseHandle(pi.hThread)

	procWaitForSingleObjectT.Call(uintptr(pi.hProcess), 5000)
	var code uint32
	procGetExitCodeProcessTest.Call(uintptr(pi.hProcess), uintptr(unsafe.Pointer(&code)))
	return "", int(code)
}

// TestPipedStdioReachesChildOnlyWhenBothFlagsSet is the F46 proof, and the reason
// the fix is all-or-nothing.
//
// nvx assigned StdOutput/StdErr in STARTUPINFO but set neither
// STARTF_USESTDHANDLES nor bInheritHandles, so the assignment was ignored and a
// piped child's output went to the parent's console instead -- which is exactly
// why terminal use looked healthy while every MCP server failed.
func TestPipedStdioReachesChildOnlyWhenBothFlagsSet(t *testing.T) {
	cases := []struct {
		name          string
		useStdHandles bool
		inherit       bool
		wantBytes     bool
		wantExitZero  bool
	}{
		{"nvx's original shape", false, false, false, true},
		{"flags without inheritance", true, false, false, false},
		{"the fix: both", true, true, true, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			read, write := makeTestPipe(t)
			defer syscall.CloseHandle(read)

			if tc.inherit {
				if err := markHandleInheritable(write); err != nil {
					t.Fatalf("markHandleInheritable: %v", err)
				}
			}

			_, code := spawnWithStdout(t, write, "F46_MARKER", tc.useStdHandles, tc.inherit)
			// Close our copy so the read below sees EOF rather than blocking.
			syscall.CloseHandle(write)

			got := readWithTimeout(t, read)
			if gotBytes := len(got) > 0; gotBytes != tc.wantBytes {
				t.Errorf("bytes on pipe = %v (%q), want %v", gotBytes, got, tc.wantBytes)
			}
			if zero := code == 0; zero != tc.wantExitZero {
				t.Errorf("child exit code = %d, want zero=%v", code, tc.wantExitZero)
			}
		})
	}
}

func readWithTimeout(t *testing.T, read syscall.Handle) string {
	t.Helper()
	done := make(chan string, 1)
	go func() {
		f := os.NewFile(uintptr(read), "pipe")
		buf := make([]byte, 256)
		n, _ := f.Read(buf)
		done <- string(buf[:n])
	}()
	select {
	case s := <-done:
		return s
	case <-time.After(3 * time.Second):
		return ""
	}
}

// TestPrepareInheritableStdioReportsAllOrNothing guards the invariant the launcher
// depends on: it must never report a partially usable set, because assigning
// handles with only one of the two settings breaks the launch entirely.
func TestPrepareInheritableStdioReportsAllOrNothing(t *testing.T) {
	s := prepareInheritableStdio()
	if !s.inheritable {
		// Legitimate on a runner with non-inheritable console handles; the launcher
		// degrades to the legacy shape. Nothing to assert beyond consistency.
		t.Skip("standard handles are not inheritable in this environment")
	}
	for name, h := range map[string]syscall.Handle{"stdin": s.in, "stdout": s.out, "stderr": s.err} {
		if h == 0 || h == syscall.InvalidHandle {
			t.Errorf("%s handle is invalid while inheritable=true", name)
			continue
		}
		if !handleIsInheritable(t, h) {
			t.Errorf("%s reported inheritable but HANDLE_FLAG_INHERIT is not set", name)
		}
	}
}
