//go:build windows

package main

import (
	"fmt"
	"os"
	"syscall"
	"testing"
	"unsafe"
)

// Why `npm install esbuild` hangs, established against the OS rather than
// inferred from a library's errno.
//
// node reports EADDRINUSE when a contained process creates a child-stdio pipe,
// which reads like a name collision and is not one: libuv maps
// ERROR_ACCESS_DENIED from CreateNamedPipeW onto that errno. This probe calls the
// Win32 function directly inside a real AppContainer and prints the raw error, so
// the diagnosis rests on a number from the kernel.
//
// It also answers the question that decides whether nvx can fix this at all: is
// the refusal about the NAME (something nvx could route around by choosing a
// different one) or about the DEVICE (\Device\NamedPipe, which nvx cannot grant
// per-container)? It tries several name shapes for exactly that reason.
func TestAppContainerCannotCreateNamedPipes(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (creates a throwaway AppContainer profile)")
	}

	if os.Getenv("NVX_PIPE_CHILD") == "1" {
		runNamedPipeChild()
		os.Exit(0)
	}

	const probeProfile = "nvx.sandbox.pipeprobe"
	sid, err := ensureAppContainerSID(probeProfile)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	defer deleteAppContainerProfile(probeProfile)

	guestHome := tempDir(t)
	workDir := tempDir(t)
	scopeCaps, err := prepareAppContainerFilesystem(sid, "", guestHome, workDir)
	if err != nil {
		t.Fatalf("filesystem prep: %v", err)
	}

	childExe := stageProbeChild(t, guestHome, "probe.exe")

	read, write := makeTestPipe(t)
	defer syscall.CloseHandle(read)
	prevOut, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	const stdOutputHandle = uintptr(0xFFFFFFF5)
	procSetStdHandleTest.Call(stdOutputHandle, uintptr(write))

	env := append(scrubEnvironment(guestHome), "NVX_PROBE=1", "NVX_PIPE_CHILD=1")
	exitCode, launchErr := launchAppContainerProcess(
		childExe,
		[]string{"-test.run=TestAppContainerCannotCreateNamedPipes"},
		env, workDir, sid, 0, scopeCaps,
	)

	procSetStdHandleTest.Call(stdOutputHandle, uintptr(prevOut))
	syscall.CloseHandle(write)
	out := readWithTimeout(t, read)

	t.Logf("child exit=%d err=%v", exitCode, launchErr)
	t.Logf("contained results:\n%s", out)
	requireAppContainerLaunch(t, launchErr)

	// Same calls outside the container, so the comparison is like-for-like and a
	// machine where this simply does not work cannot read as a finding.
	t.Logf("uncontained results:\n%s", capturedNamedPipeResults())

	if out == "" {
		t.Fatal("no output from the contained probe")
	}
}

// namedPipeNames covers the shapes that would distinguish a name problem from a
// device problem: libuv's own layout, a flat name, and one under a directory
// nobody else uses.
func namedPipeNames(tag string) []string {
	return []string{
		`\\.\pipe\uv\` + tag,
		`\\.\pipe\` + tag,
		`\\.\pipe\nvx\` + tag,
	}
}

func runNamedPipeChild() {
	fmt.Print(capturedNamedPipeResults())
}

func capturedNamedPipeResults() string {
	var b []byte
	for _, name := range namedPipeNames(fmt.Sprintf("probe%d", os.Getpid())) {
		b = append(b, []byte("  "+name+" -> "+tryCreateNamedPipe(name)+"\n")...)
	}
	return string(b)
}

// tryCreateNamedPipe calls CreateNamedPipeW and reports the raw Win32 result.
func tryCreateNamedPipe(name string) string {
	const (
		pipeAccessDuplex   = 0x00000003
		fileFlagOverlapped = 0x40000000
		pipeTypeByte       = 0x00000000
		pipeUnlimited      = 255
	)
	p, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return "bad name: " + err.Error()
	}
	h, _, callErr := procCreateNamedPipeW.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(pipeAccessDuplex|fileFlagOverlapped),
		uintptr(pipeTypeByte),
		uintptr(pipeUnlimited),
		65536, 65536, 0, 0,
	)
	if h == uintptr(syscall.InvalidHandle) {
		errno, _ := callErr.(syscall.Errno)
		return fmt.Sprintf("FAILED err=%d (%v)", uintptr(errno), callErr)
	}
	syscall.CloseHandle(syscall.Handle(h))
	return "CREATED"
}

var procCreateNamedPipeW = modKernel32.NewProc("CreateNamedPipeW")
