//go:build windows

package main

import (
	"syscall"
	"testing"
)

// The orphan fix rests entirely on PeekNamedPipe telling a live pipe from a
// hung-up one without consuming anything. Both halves are asserted here,
// because getting either wrong is silent and severe in opposite directions: a
// missed hangup leaks processes until the machine runs out of commit charge (48
// of them, measured), and a false hangup kills a command the user is running.
func TestStdinPipeIsBrokenOnlyAfterTheWriterCloses(t *testing.T) {
	read, write := makeTestPipe(t)
	defer syscall.CloseHandle(read)

	if stdinPipeIsBroken(read) {
		t.Fatal("an open pipe with a live writer was reported as broken; this would kill running commands")
	}

	// Data waiting is still not a hangup. An idle-but-live client and a busy one
	// must both read as "still there".
	msg := []byte(`{"jsonrpc":"2.0"}`)
	var wrote uint32
	if err := syscall.WriteFile(write, msg, &wrote, nil); err != nil {
		t.Fatal(err)
	}
	if stdinPipeIsBroken(read) {
		t.Fatal("a pipe with unread data was reported as broken")
	}

	// The bytes must survive the check: nvx hands this same handle to the
	// contained child, so a peek that consumed would steal input from the
	// process it was meant for -- an MCP server losing its first request.
	buf := make([]byte, len(msg))
	var got uint32
	if err := syscall.ReadFile(read, buf, &got, nil); err != nil {
		t.Fatalf("reading after the check failed: %v", err)
	}
	if string(buf[:got]) != string(msg) {
		t.Fatalf("the check consumed input: read %q, wrote %q", string(buf[:got]), string(msg))
	}

	// The writer going away is the condition that stranded 38 processes.
	syscall.CloseHandle(write)
	if !stdinPipeIsBroken(read) {
		t.Fatal("a pipe whose writer has closed was not reported as broken; nvx would wait forever")
	}
}

// A finished pipeline must survive.
//
// This is the failure direction the watchdog's own comment called severe and
// that the first version shipped with anyway: `echo hi | nvx node -e "<long
// work>"` where the child drains stdin. The producer exits, the buffer empties,
// the pipe reads as broken, and a healthy command was killed at 15 seconds with
// exit 129. The pipe alone cannot tell that from an abandoned client -- the
// parent still being alive is what separates them, and that is asserted here.
func TestAFinishedPipelineIsNotTreatedAsAHangup(t *testing.T) {
	read, write := makeTestPipe(t)
	defer syscall.CloseHandle(read)

	// Exactly the state of `echo hi | nvx ...` once the child has consumed the
	// input: writer closed, nothing buffered.
	var wrote uint32
	if err := syscall.WriteFile(write, []byte("hi\n"), &wrote, nil); err != nil {
		t.Fatal(err)
	}
	syscall.CloseHandle(write)
	buf := make([]byte, 8)
	var got uint32
	_ = syscall.ReadFile(read, buf, &got, nil)

	if !stdinPipeIsBroken(read) {
		t.Fatal("the setup is wrong: a drained, writer-closed pipe should read as broken, " +
			"which is precisely why the pipe alone cannot be the whole signal")
	}

	// The second signal is what saves the pipeline: the shell that built it is
	// still running, and this process stands in for it.
	self, err := syscall.GetCurrentProcess()
	if err != nil {
		t.Fatal(err)
	}
	if processHasExited(self) {
		t.Fatal("a running process was reported as exited; the hangup check would fire on every pipeline")
	}
}

// The parent lookup has to actually find a parent, or the watchdog silently
// never arms and the orphan leak comes back with nothing to show for it.
func TestParentProcessIsIdentifiable(t *testing.T) {
	ppid, ok := parentProcessID()
	if !ok {
		t.Fatal("could not determine the parent process; the hangup watchdog would never arm")
	}
	if ppid == 0 || ppid == uint32(syscall.Getpid()) {
		t.Fatalf("implausible parent pid %d for self %d", ppid, syscall.Getpid())
	}
	h, ok := openParentProcess()
	if !ok {
		t.Fatal("could not open a handle to the parent process")
	}
	defer syscall.CloseHandle(h)
	if processHasExited(h) {
		t.Error("the live parent of this test was reported as exited")
	}
}

// A console or a file is not evidence that anyone is waiting on us, so the
// watchdog must not arm there -- otherwise an interactive `nvx npm test` could
// be killed by a check that was never meaningful for that handle shape.
func TestHangupWatchDoesNotArmOnANonPipeStdin(t *testing.T) {
	nul, err := syscall.Open("NUL", syscall.O_RDWR, 0)
	if err != nil {
		t.Skipf("cannot open NUL to stand in for a non-pipe stdin: %v", err)
	}
	defer syscall.CloseHandle(nul)

	if fileType, _, _ := procGetFileType.Call(uintptr(nul)); fileType == fileTypePipe {
		t.Skip("NUL reported itself as a pipe on this host, so it cannot stand in for a non-pipe")
	}

	prev, _ := syscall.GetStdHandle(syscall.STD_INPUT_HANDLE)
	const stdInputHandle = uintptr(0xFFFFFFF6)
	procSetStdHandleTest.Call(stdInputHandle, uintptr(nul))
	defer procSetStdHandleTest.Call(stdInputHandle, uintptr(prev))

	fired := make(chan struct{}, 1)
	watchStdinForHangup(func() { fired <- struct{}{} })

	select {
	case <-fired:
		t.Fatal("the watchdog armed on a non-pipe stdin and fired")
	default:
	}
}
