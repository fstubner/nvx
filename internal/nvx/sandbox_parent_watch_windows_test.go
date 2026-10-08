//go:build windows

package nvx

import (
	"os/exec"
	"strings"
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

// A pid that names a process newer than this one is not our parent. The parent
// pid comes from a snapshot, and if the parent exits before it is opened the pid
// can be reused. The watchdog then waited on an unrelated process. A child this
// test starts stands in for the reused pid, since it is newer by construction.
func TestAParentNewerThanThisProcessIsRefused(t *testing.T) {
	cmd := exec.Command("powershell", "-NoProfile", "-Command", "Start-Sleep -Seconds 30")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start a helper process: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	if h, ok := openIfOlderThanSelf(uint32(cmd.Process.Pid)); ok {
		syscall.CloseHandle(h)
		t.Fatal("a process created after this one was accepted as its parent")
	}
}

// The watchdog has to say why it declined, and a non-pipe stdin must not arm it.
//
// A console or a file is not evidence that anyone is waiting on us, so arming
// there could kill an interactive `nvx npm test`. A separate test used to check
// that by waiting for the watchdog to fire and looking at once, which could not
// fail, because the first poll is 15 seconds away. The not-armed record below is what
// shows it.
//
// It used to log only when it fired, so "declined" and "never armed" looked
// identical from outside. That left 15 processes which outlived their client
// with no way to tell which had happened, and the difference decides whether
// there is a bug at all.
func TestHangupWatchRecordsWhyItDidNotArm(t *testing.T) {
	nul, err := syscall.Open("NUL", syscall.O_RDWR, 0)
	if err != nil {
		t.Skipf("cannot open NUL to stand in for a non-pipe stdin: %v", err)
	}
	defer syscall.CloseHandle(nul)
	if fileType, _, _ := procGetFileType.Call(uintptr(nul)); fileType == fileTypePipe {
		t.Skip("NUL reported itself as a pipe on this host")
	}

	prev, _ := syscall.GetStdHandle(syscall.STD_INPUT_HANDLE)
	const stdInputHandle = uintptr(0xFFFFFFF6)
	procSetStdHandleTest.Call(stdInputHandle, uintptr(nul))
	defer procSetStdHandleTest.Call(stdInputHandle, uintptr(prev))

	// Silent unless someone is investigating: this is a debugging aid, and the
	// default must not write a record on every invocation.
	quiet := tempDir(t)
	t.Setenv(nvxTraceEnvVar, "")
	watchStdinForHangup(quiet, func() {})
	if entries, err := readAuditEntries(quiet); err != nil {
		t.Fatal(err)
	} else if len(entries) != 0 {
		t.Errorf("the watchdog wrote records with %s unset: %v", nvxTraceEnvVar, entries)
	}

	home := tempDir(t)
	t.Setenv(nvxTraceEnvVar, "1")
	watchStdinForHangup(home, func() {})

	entries, err := readAuditEntries(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("want one hangup_watch record, got %d: %v", len(entries), entries)
	}
	e := entries[0]
	if e["event"] != "hangup_watch" || e["state"] != "not-armed" {
		t.Errorf("unexpected record: %v", e)
	}
	if !strings.Contains(e["reason"], "not a pipe") {
		t.Errorf("the reason must name what stopped it arming, got %q", e["reason"])
	}
}

// Finding the parent must not list every process on the machine. Arming the
// watch took a process snapshot twice, 19 to 27 ms a call on the development
// machine, at the start of every shimmed command whose stdin is a pipe. The
// kernel keeps the parent's pid in the process's own record, so the snapshot is
// there only for a kernel that gives no answer.
func TestFindingTheParentDoesNotListEveryProcess(t *testing.T) {
	orig := parentProcessIDFromSnapshot
	t.Cleanup(func() { parentProcessIDFromSnapshot = orig })
	parentProcessIDFromSnapshot = func() (uint32, bool) {
		t.Error("the process list was snapshotted to find the parent")
		return 0, false
	}

	if _, ok := parentProcessID(); !ok {
		t.Fatal("no parent found")
	}
	h, pid, ok := openParentProcessWithID()
	if !ok {
		t.Fatal("could not open the parent")
	}
	defer syscall.CloseHandle(h)
	if pid == 0 {
		t.Error("openParentProcessWithID opened a parent and reported pid 0")
	}
}

// The two ways of asking must agree, or the watchdog would wait on the wrong
// process.
func TestTheKernelAndTheProcessListNameTheSameParent(t *testing.T) {
	fromKernel, ok := parentProcessIDFromKernel()
	if !ok {
		t.Fatal("the kernel gave no parent")
	}
	fromList, ok := parentProcessIDFromSnapshot()
	if !ok {
		t.Fatal("the process list gave no parent")
	}
	if fromKernel != fromList {
		t.Errorf("the kernel says the parent is %d and the process list says %d", fromKernel, fromList)
	}
}

// A kernel that does not answer leaves the process list to do it, so the
// watchdog still arms.
func TestTheProcessListAnswersWhenTheKernelDoesNot(t *testing.T) {
	origKernel, origList := parentProcessIDFromKernel, parentProcessIDFromSnapshot
	t.Cleanup(func() { parentProcessIDFromKernel, parentProcessIDFromSnapshot = origKernel, origList })
	parentProcessIDFromKernel = func() (uint32, bool) { return 0, false }
	parentProcessIDFromSnapshot = func() (uint32, bool) { return 4242, true }

	if pid, ok := parentProcessID(); !ok || pid != 4242 {
		t.Errorf("parentProcessID() = %d, %v, want the process list's 4242", pid, ok)
	}
}
