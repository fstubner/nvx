//go:build windows

package main

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// startupInfoWithFDs is STARTUPINFOW with the reserved pair reachable.
//
// Go's syscall.StartupInfo declares cbReserved2/lpReserved2 as blank fields, so
// the struct nvx already uses cannot carry them. They are how a Windows process
// hands a child more than three descriptors: the C runtime reads the blob they
// point at and rebuilds its fd table from it, which is what libuv does when a
// node stdio array is longer than three entries.
type startupInfoWithFDs struct {
	Cb            uint32
	Reserved      *uint16
	Desktop       *uint16
	Title         *uint16
	X             uint32
	Y             uint32
	XSize         uint32
	YSize         uint32
	XCountChars   uint32
	YCountChars   uint32
	FillAttribute uint32
	Flags         uint32
	ShowWindow    uint16
	CbReserved2   uint16
	LpReserved2   *byte
	StdInput      syscall.Handle
	StdOutput     syscall.Handle
	StdErr        syscall.Handle
}

// msvcrtFDBlob builds the lpReserved2 payload: a count, one flag byte per
// descriptor, then the handles.
//
// FOPEN|FPIPE marks a descriptor the runtime should treat as an open pipe;
// FOPEN|FDEV is what a console handle gets. Getting the flags wrong does not
// fail loudly -- the child simply finds the descriptor closed.
func msvcrtFDBlob(handles []syscall.Handle, flags []byte) []byte {
	n := len(handles)
	buf := make([]byte, 4+n+n*8)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(n))
	copy(buf[4:4+n], flags)
	for i, h := range handles {
		binary.LittleEndian.PutUint64(buf[4+n+i*8:], uint64(h))
	}
	return buf
}

// Does an OVERLAPPED pipe handle, injected as a descriptor the child never
// opened, work as node's IPC channel?
//
// This is the remaining question after
// TestNodeAdoptsAnExternallyCreatedPipeAsItsIPCChannel established that a pipe
// opened with fs.openSync cannot: libuv asserts an IPC pipe is not
// non-overlapped, and node offers no way to open one the other way. The escape
// is for the pipe never to be opened by the contained process at all -- nvx
// creates the client handle itself, overlapped, and injects it into the fd table
// at launch.
//
// Two things are being answered together, because separating them costs more
// than it buys: whether the fd-table injection works at all (nvx passes only the
// three standard handles today), and whether libuv accepts the resulting handle
// as a channel.
//
// UNCONTAINED on purpose, like the probe before it. Both constraints are the C
// runtime's and libuv's, not AppContainer's, so the cheap version is the whole
// question -- and if it fails here there is no point building the blob into
// nvx's launch path to watch it fail there.
//
// WHERE THIS STOPPED, 2026-09-05. It does not work yet, and the reason is NOT
// yet known to be a real obstacle:
//
//	RESULT raw-failed EBADF    fs.writeSync(3, ...) in the child
//	RESULT send-returned       process.send did not throw
//	Error: write EPIPE         ...and then failed, writing to a bad descriptor
//
// Read in the wrong order that looks like libuv adopting the handle and the pipe
// being at fault. It is the opposite: fd 3 does not exist in the child at all,
// and node built a channel object purely because NODE_CHANNEL_FD was set. So
// nothing here has yet tested what it was written to test, and the libuv
// question remains open rather than answered.
//
// THE NEXT MEASUREMENT, and it is cheap: inject a plain FILE at fd 4 alongside
// the pipe and have the child fs.readSync(4). That separates the two live
// hypotheses, which no evidence yet distinguishes:
//
//   - the blob this test builds is malformed (layout, flag bytes, or handle
//     inheritability), and fd injection would work if it were right; or
//   - node/UCRT does not populate its fd table from lpReserved2 when it is not
//     libuv doing the spawning, and the whole approach is dead.
//
// Only if a file-backed descriptor arrives is it worth returning to the pipe.
// Do not read the EPIPE above as evidence about pipes; it is evidence about a
// descriptor that was never there.
func TestAnInjectedOverlappedPipeWorksAsNodesIPCChannel(t *testing.T) {
	// An investigation harness, not a regression test, and it does not pass:
	// set NVX_IPC_SPIKE=1 to pick the work up. See the note above for exactly
	// where it stopped and what the next measurement is.
	if os.Getenv("NVX_IPC_SPIKE") != "1" {
		t.Skip("set NVX_IPC_SPIKE=1 to continue the brokered-IPC investigation (does not pass yet)")
	}
	nodeExe, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	userSID, err := currentUserSIDString()
	if err != nil {
		t.Skipf("cannot read this user's SID: %v", err)
	}

	// The SERVER stays as nvx creates them today (byte mode, blocking) so the
	// existing reader works. Overlapped-ness is a property of each handle, and
	// only the CLIENT -- the one libuv adopts -- has to have it.
	name := `\\.\pipe\nvx-ipc-inject-probe-` + stdioSessionID(t.Name()+time.Now().Format("150405.000"))
	server, err := createNamedPipeWithSecurity(name, "D:(A;;GA;;;"+userSID+")")
	if err != nil {
		t.Fatalf("create pipe: %v", err)
	}
	defer syscall.CloseHandle(server)

	const fileFlagOverlapped = 0x40000000
	pathPtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		t.Fatal(err)
	}
	sa := syscall.SecurityAttributes{InheritHandle: 1}
	sa.Length = uint32(unsafe.Sizeof(sa))
	client, err := syscall.CreateFile(pathPtr,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, &sa,
		syscall.OPEN_EXISTING, fileFlagOverlapped, 0)
	if err != nil {
		t.Fatalf("open pipe client overlapped: %v", err)
	}
	defer syscall.CloseHandle(client)

	// Accept BEFORE launching. A client that has opened a pipe whose server has
	// not yet called ConnectNamedPipe is in the listening state, and a write from
	// it fails -- which reaches node as EPIPE out of process.send() on the very
	// first message, and looks exactly like the channel having been rejected.
	if !acceptPipeClient(server) {
		t.Fatal("the server could not accept its own client handle")
	}

	script := filepath.Join(tempDir(t), "ipcchild.cjs")
	// Two writes, to tell two failures apart: a raw write proves the injected
	// descriptor is a usable pipe at all, and process.send proves libuv will
	// drive it as a channel. If the raw one works and send does not, the fd
	// injection is sound and the problem is inside libuv's IPC path.
	body := `
const fs = require('fs');
try { fs.writeSync(3, 'RAW-WRITE-OK\n'); console.log('RESULT raw-ok'); }
catch (e) { console.log('RESULT raw-failed ' + (e.code || e.message)); }
if (!process.send) { console.log('RESULT no-channel'); process.exit(1); }
try { process.send({ hello: 'injected-ipc' }); console.log('RESULT send-returned'); }
catch (e) { console.log('RESULT send-threw ' + (e.code || e.message)); }
setTimeout(() => process.exit(0), 1200);
`
	if err := os.WriteFile(script, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	stderr, _ := syscall.GetStdHandle(syscall.STD_ERROR_HANDLE)
	stdin, _ := syscall.GetStdHandle(syscall.STD_INPUT_HANDLE)
	for _, h := range []syscall.Handle{stdin, stdout, stderr} {
		_ = markHandleInheritable(h)
	}
	const fOpen, fPipe, fDev = 0x01, 0x08, 0x40
	blob := msvcrtFDBlob(
		[]syscall.Handle{stdin, stdout, stderr, client},
		[]byte{fOpen | fDev, fOpen | fDev, fOpen | fDev, fOpen | fPipe})

	var si startupInfoWithFDs
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Flags = 0x00000100 // STARTF_USESTDHANDLES
	si.StdInput, si.StdOutput, si.StdErr = stdin, stdout, stderr
	si.CbReserved2 = uint16(len(blob))
	si.LpReserved2 = &blob[0]

	cmdline, err := syscall.UTF16PtrFromString(`"` + nodeExe + `" "` + script + `"`)
	if err != nil {
		t.Fatal(err)
	}
	// NODE_CHANNEL_FD names the descriptor carrying the channel -- exactly what
	// node's own fork sets. Here it names one node never opened.
	envBlock, err := buildEnvBlockForProbe(append(os.Environ(), "NODE_CHANNEL_FD=3"))
	if err != nil {
		t.Fatal(err)
	}

	var pi processInformation
	const createUnicodeEnvironment = 0x00000400
	ok, _, callErr := procCreateProcessW.Call(
		0, uintptr(unsafe.Pointer(cmdline)), 0, 0, 1,
		uintptr(createUnicodeEnvironment),
		uintptr(unsafe.Pointer(&envBlock[0])), 0,
		uintptr(unsafe.Pointer(&si)), uintptr(unsafe.Pointer(&pi)),
	)
	if ok == 0 {
		t.Fatalf("CreateProcessW: %v", callErr)
	}
	defer syscall.CloseHandle(pi.hProcess)
	defer syscall.CloseHandle(pi.hThread)

	read := make(chan string, 1)
	go func() {
		buf := make([]byte, 4096)
		n, err := pipeReader{server}.Read(buf)
		if err != nil && n == 0 {
			read <- ""
			return
		}
		read <- string(buf[:n])
	}()

	var got string
	select {
	case got = <-read:
	case <-time.After(45 * time.Second):
	}
	_, _ = syscall.WaitForSingleObject(pi.hProcess, 5000)

	if !strings.Contains(got, "injected-ipc") {
		t.Fatalf("no message arrived on the injected channel (%q).\n"+
			"Either the fd-table injection did not reach node, or libuv would not adopt the handle. "+
			"Brokered IPC -- and a contained `npx vitest run` -- stays out of reach.", got)
	}
	t.Logf("WORKS: node used an injected overlapped pipe as its IPC channel and wrote %q.\n"+
		"Brokered fork() is therefore possible: nvx creates the client handle and injects it, "+
		"rather than contained code opening it.", got)
}

// buildEnvBlockForProbe renders environment strings as a UTF-16 block.
func buildEnvBlockForProbe(env []string) ([]uint16, error) {
	var out []uint16
	for _, kv := range env {
		if strings.IndexByte(kv, 0) >= 0 {
			continue
		}
		u, err := syscall.UTF16FromString(kv)
		if err != nil {
			return nil, err
		}
		out = append(out, u...)
	}
	out = append(out, 0)
	return out, nil
}
