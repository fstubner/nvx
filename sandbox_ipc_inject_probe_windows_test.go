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
// WHERE THIS STOPPED, 2026-09-05. It does not work, and the cause is narrowed
// but not found. The pipe and libuv are NOT implicated -- a plain file injected
// beside it fails identically:
//
//	RESULT file-fd-failed EBADF   fs.readSync(4, ...) on an ordinary file
//	RESULT raw-failed EBADF       fs.writeSync(3, ...) on the pipe
//	RESULT send-returned          process.send did not throw...
//	Error: write EPIPE            ...then failed, writing to a bad descriptor
//
// Note the trap in that order: read top-down it looks like libuv adopting the
// handle and the pipe misbehaving. It is the opposite. Neither descriptor exists
// in the child, and node builds a channel object purely because NODE_CHANNEL_FD
// is set, so the EPIPE is a write to nothing and says nothing about pipes.
//
// What has been ruled OUT, each by measurement rather than reasoning:
//
//   - The blob layout. Verified byte for byte against libuv's format:
//     count=05 00 00 00, flags=01 01 01 09 01, then five 8-byte handles.
//     cbReserved2=49 = 4 + 5 + 5*8, and sizeof(STARTUPINFOW)=104.
//   - The struct offsets. The child's stdout reached the file named in
//     si.StdOutput, which sits AFTER CbReserved2/LpReserved2, so those fields
//     are where Windows expects them.
//   - Handle inheritance. Same evidence: an inherited handle demonstrably
//     reached the child.
//   - An invalid handle poisoning the blob. NUL is used for stdin instead of
//     this process's handles, and the result is unchanged.
//   - Node being unable to receive extra descriptors at all. node -> node with
//     stdio [0,1,2,fd] delivers fd 3 and reads the file: measured, works.
//
// So the mechanism works for node, and this harness's blob looks right, and the
// child still sees neither descriptor. The gap is between those two facts.
//
// THE CONTROL WAS RUN, and it passes: see
// TestTheFDInjectionBlobWorksForANonNodeCRTProgram. A C program built against
// the UCRT, launched by this same code with a byte-identical blob -- including
// an overlapped pipe at fd 3 marked FOPEN|FPIPE -- reports both descriptors
// PRESENT. So the launch is sound, and node's EBADF is not a malformed blob, a
// bad CreateProcessW call, or handle inheritance.
//
// Which leaves a contradiction that is the actual state of this investigation:
//
//	C program, this launch      fd 3 PRESENT, fd 4 PRESENT
//	node, this launch           fd 3 EBADF,   fd 4 EBADF
//	node, spawned by node       fd 3 readable
//
// Node does receive an inherited descriptor when libuv is the spawner, and does
// not here, with the blob and the CreateProcessW arguments matched as closely as
// they can be. Ruled out along the way, each by measurement: the environment
// block (inheriting the parent's changes nothing), the creation flags, whether
// NODE_CHANNEL_FD is set at all, an invalid handle poisoning the blob, and the
// pipe entry itself.
//
// So the descriptors are discarded inside node's own startup by something this
// launch does differently from libuv's, still unidentified. That is a question
// about node, not about nvx or Windows, and it is where the next session starts
// -- reading libuv's uv_spawn against this call, or bisecting node's startup.
// Until it is answered, brokered IPC is neither possible nor ruled out.
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
try { const b = Buffer.alloc(32); const n = fs.readSync(4, b, 0, 32, 0);
      console.log('RESULT file-fd-ok ' + b.slice(0, n).toString()); }
catch (e) { console.log('RESULT file-fd-failed ' + (e.code || e.message)); }
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

	// NUL for the standard three rather than this process's own handles. Under
	// `go test` stdin is not necessarily a valid handle, and the C runtime parses
	// the blob as a unit -- one bad entry and the whole fd table is discarded,
	// which presents as every injected descriptor missing rather than one.
	nulPtr, err := syscall.UTF16PtrFromString("NUL")
	if err != nil {
		t.Fatal(err)
	}
	nul, err := syscall.CreateFile(nulPtr, syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, &sa,
		syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("open NUL: %v", err)
	}
	defer syscall.CloseHandle(nul)

	// The child's own words are the entire diagnostic, so they go to a file. NUL
	// would lose them and the console is not readable back from a test binary.
	outPath := filepath.Join(tempDir(t), "child-output.txt")
	outPtr, err := syscall.UTF16PtrFromString(outPath)
	if err != nil {
		t.Fatal(err)
	}
	outFile, err := syscall.CreateFile(outPtr, syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ, &sa, syscall.CREATE_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("create child output file: %v", err)
	}
	defer syscall.CloseHandle(outFile)

	stdin, stdout, stderr := nul, outFile, outFile
	// A plain FILE at fd 4, holding known bytes. This is the discriminator: if
	// even an ordinary file does not arrive, the blob is not being honoured and
	// nothing about pipes or libuv is implicated. A file cannot be non-overlapped
	// in a way libuv objects to, and cannot be in the wrong pipe state.
	markerPath := filepath.Join(tempDir(t), "marker.txt")
	if err := os.WriteFile(markerPath, []byte("MARKER-CONTENT"), 0o600); err != nil {
		t.Fatal(err)
	}
	markerPtr, err := syscall.UTF16PtrFromString(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	marker, err := syscall.CreateFile(markerPtr, syscall.GENERIC_READ,
		syscall.FILE_SHARE_READ, &sa, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("open marker file: %v", err)
	}
	defer syscall.CloseHandle(marker)

	const fOpen, fPipe, fDev = 0x01, 0x08, 0x40
	blob := msvcrtFDBlob(
		[]syscall.Handle{stdin, stdout, stderr, client, marker},
		[]byte{fOpen, fOpen, fOpen, fOpen | fPipe, fOpen})

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
	// With NVX_IPC_SPIKE_NO_CHANNEL=1 the channel variable is left unset. Node
	// then does no IPC setup, which isolates a question the first runs could not
	// answer: whether the descriptors never arrive, or arrive and are consumed by
	// uv_pipe_open taking ownership of fd 3 during channel setup.
	childEnv := os.Environ()
	if os.Getenv("NVX_IPC_SPIKE_NO_CHANNEL") != "1" {
		childEnv = append(childEnv, "NODE_CHANNEL_FD=3")
	}
	envBlock, err := buildEnvBlockForProbe(childEnv)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("STARTUPINFOW size=%d (want 104 on x64), cbReserved2=%d (want %d), blob=% x",
		unsafe.Sizeof(si), si.CbReserved2, 4+5+5*8, blob)
	t.Logf("handles: nul=%#x out=%#x pipe=%#x marker=%#x", nul, outFile, client, marker)

	// In NO_CHANNEL mode the environment is inherited rather than rebuilt, which
	// makes this call identical to the one the UCRT control program gets -- the
	// only remaining difference between a launch node ignores and one a C program
	// honours.
	var pi processInformation
	const createUnicodeEnvironment = 0x00000400
	envArg, flagsArg := uintptr(unsafe.Pointer(&envBlock[0])), uintptr(createUnicodeEnvironment)
	if os.Getenv("NVX_IPC_SPIKE_NO_CHANNEL") == "1" {
		envArg, flagsArg = 0, 0
	}
	ok, _, callErr := procCreateProcessW.Call(
		0, uintptr(unsafe.Pointer(cmdline)), 0, 0, 1,
		flagsArg, envArg, 0,
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

	if childSaid, readErr := os.ReadFile(outPath); readErr == nil {
		t.Logf("child said:\n%s", childSaid)
	}
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
