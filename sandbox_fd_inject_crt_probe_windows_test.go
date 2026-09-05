//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

// Does the fd-injection blob work for ANY program, or is node the odd one out?
//
// This is the control for TestAnInjectedOverlappedPipeWorksAsNodesIPCChannel,
// which builds a blob that matches libuv's format byte for byte and yet leaves
// node reporting EBADF for every injected descriptor. Two explanations survived
// every measurement there, and nothing distinguished them:
//
//   - the CreateProcessW call in that harness is subtly wrong, so no child would
//     get the descriptors; or
//   - node's runtime does not populate its fd table from lpReserved2 when libuv
//     is not the spawner, which would end the brokered-IPC approach.
//
// So: launch a plain C program built against the UCRT -- the same runtime node
// uses -- with the same blob, and ask it directly. Nothing about pipes or IPC is
// involved; the descriptors are ordinary files, because the question is only
// whether the fd table is populated at all.
//
// _get_osfhandle is the narrowest possible probe: it reads the CRT's table and
// returns -1 for a descriptor that is not open.
func TestTheFDInjectionBlobWorksForANonNodeCRTProgram(t *testing.T) {
	if os.Getenv("NVX_IPC_SPIKE") != "1" {
		t.Skip("set NVX_IPC_SPIKE=1 to continue the brokered-IPC investigation")
	}
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("no gcc to build the control program with")
	}

	dir := tempDir(t)
	src := filepath.Join(dir, "fdreport.c")
	exe := filepath.Join(dir, "fdreport.exe")
	program := `#include <stdio.h>
#include <io.h>
#include <stdint.h>
int main(void) {
    for (int fd = 3; fd <= 4; fd++) {
        intptr_t h = _get_osfhandle(fd);
        if (h == -1) printf("FD %d MISSING\n", fd);
        else         printf("FD %d PRESENT\n", fd);
    }
    fflush(stdout);
    return 0;
}
`
	if err := os.WriteFile(src, []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(gcc, "-o", exe, src).CombinedOutput(); err != nil {
		t.Skipf("cannot build the control program: %v\n%s", err, out)
	}

	sa := syscall.SecurityAttributes{InheritHandle: 1}
	sa.Length = uint32(unsafe.Sizeof(sa))

	openInheritable := func(path string, access uint32, create uint32) syscall.Handle {
		p, err := syscall.UTF16PtrFromString(path)
		if err != nil {
			t.Fatal(err)
		}
		h, err := syscall.CreateFile(p, access, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE,
			&sa, create, syscall.FILE_ATTRIBUTE_NORMAL, 0)
		if err != nil {
			t.Fatalf("open %s: %v", path, err)
		}
		t.Cleanup(func() { syscall.CloseHandle(h) })
		return h
	}

	markerB := filepath.Join(dir, "b.txt")
	for _, p := range []string{markerB} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	outPath := filepath.Join(dir, "out.txt")

	nul := openInheritable("NUL", syscall.GENERIC_READ|syscall.GENERIC_WRITE, syscall.OPEN_EXISTING)
	outFile := openInheritable(outPath, syscall.GENERIC_WRITE, syscall.CREATE_ALWAYS)
	fdB := openInheritable(markerB, syscall.GENERIC_READ, syscall.OPEN_EXISTING)

	// fd 3 is an OVERLAPPED PIPE marked FOPEN|FPIPE, mirroring the node probe's
	// blob exactly rather than an easier all-files version. With five plain files
	// this control passed while node failed, which left the pipe entry itself as
	// the only untested difference between them -- and a CRT that gives up on the
	// whole blob when one entry displeases it would look exactly like node
	// ignoring the mechanism.
	const fileFlagOverlapped = 0x40000000
	userSID, err := currentUserSIDString()
	if err != nil {
		t.Skipf("cannot read this user's SID: %v", err)
	}
	pipeName := `\\.\pipe\nvx-fd-control-` + stdioSessionID(t.Name())
	server, err := createNamedPipeWithSecurity(pipeName, "D:(A;;GA;;;"+userSID+")")
	if err != nil {
		t.Fatalf("create pipe: %v", err)
	}
	defer syscall.CloseHandle(server)
	pipePtr, err := syscall.UTF16PtrFromString(pipeName)
	if err != nil {
		t.Fatal(err)
	}
	fdA, err := syscall.CreateFile(pipePtr, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, &sa,
		syscall.OPEN_EXISTING, fileFlagOverlapped, 0)
	if err != nil {
		t.Fatalf("open pipe client: %v", err)
	}
	defer syscall.CloseHandle(fdA)

	const fOpen, fPipe = 0x01, 0x08
	blob := msvcrtFDBlob(
		[]syscall.Handle{nul, outFile, outFile, fdA, fdB},
		[]byte{fOpen, fOpen, fOpen, fOpen | fPipe, fOpen})

	var si startupInfoWithFDs
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Flags = 0x00000100 // STARTF_USESTDHANDLES
	si.StdInput, si.StdOutput, si.StdErr = nul, outFile, outFile
	si.CbReserved2 = uint16(len(blob))
	si.LpReserved2 = &blob[0]

	cmdline, err := syscall.UTF16PtrFromString(`"` + exe + `"`)
	if err != nil {
		t.Fatal(err)
	}
	var pi processInformation
	ok, _, callErr := procCreateProcessW.Call(
		0, uintptr(unsafe.Pointer(cmdline)), 0, 0, 1, 0, 0, 0,
		uintptr(unsafe.Pointer(&si)), uintptr(unsafe.Pointer(&pi)),
	)
	if ok == 0 {
		t.Fatalf("CreateProcessW: %v", callErr)
	}
	defer syscall.CloseHandle(pi.hProcess)
	defer syscall.CloseHandle(pi.hThread)
	if _, err := syscall.WaitForSingleObject(pi.hProcess, 20000); err != nil {
		t.Fatalf("waiting for the control program: %v", err)
	}

	said, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("the control program wrote nothing: %v", err)
	}
	got := string(said)
	t.Logf("control program said:\n%s", got)

	switch {
	case strings.Contains(got, "FD 3 PRESENT") && strings.Contains(got, "FD 4 PRESENT"):
		t.Log("BLOB WORKS for a UCRT program: this launch is sound, so the node probe's EBADF is not " +
			"a malformed blob, a bad CreateProcessW call, or handle inheritance.\n" +
			"What it does NOT establish is that node ignores lpReserved2 in general -- node spawned " +
			"BY NODE does receive an extra fd through the same mechanism. Both are measured, so the " +
			"descriptors are being discarded somewhere inside node's own startup, by something this " +
			"launch does differently from libuv's and that is not the blob, the environment, the " +
			"creation flags, or the handle kinds. That is where a next session would look, and it is " +
			"a question about node rather than about nvx or Windows.")
	case strings.Contains(got, "MISSING"):
		t.Log("BLOB DOES NOT WORK even for a plain UCRT program, so the harness is at fault rather " +
			"than node, and brokered IPC is still open. Fix this launch and re-run the node probe.")
	default:
		t.Fatalf("the control program said something unexpected:\n%s", got)
	}
}
