//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Node will NOT adopt a pipe opened with fs.open as its IPC channel, and this
// pins the reason so the dead end is not walked twice.
//
// It is the one question that decides whether child_process.fork can ever work
// inside an AppContainer. A container cannot create a named pipe, so the only
// way an IPC channel could exist is for nvx to create it outside and hand it in
// -- the trick that already carries stdout, stderr and stdin. Those are ordinary
// descriptors; an IPC channel is adopted by libuv and driven with overlapped
// I/O.
//
// The answer, measured 2026-09-04:
//
//	Assertion failed: !(pipe->flags & UV_HANDLE_NON_OVERLAPPED_PIPE)
//	file src\win\pipe.c, line 2492
//
// An IPC channel must be an OVERLAPPED pipe, and fs.openSync cannot produce one
// -- CreateFile without FILE_FLAG_OVERLAPPED gives a synchronous handle and node
// exposes no way to ask for the other kind. So the preload cannot broker an IPC
// channel however nvx creates the pipe: the constraint is on the handle the
// CONTAINED process opens, not on the server end. Creating the server overlapped
// would not move it.
//
// What remains possible, and is not attempted: nvx creates the pipe overlapped
// and injects the client handle into the contained process's fd table at launch,
// so node never opens it. That needs nvx to build the MSVCRT stdio inheritance
// blob -- it currently passes only the three standard handles -- and carries a
// further unknown, whether the handle survives as overlapped through node's own
// spawn into the grandchild. That is a project, not a patch.
//
// Deliberately UNCONTAINED: the constraint is libuv's, not AppContainer's, so
// the cheap version of the question is the whole question. Answering it this way
// cost minutes instead of the hours the parent-side implementation would have.
//
// This asserts the CURRENT limitation. If a future libuv drops the assertion it
// fails, which is the signal that brokered IPC is worth revisiting -- and the
// reason it is kept rather than deleted along with the approach.
func TestNodeAdoptsAnExternallyCreatedPipeAsItsIPCChannel(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}

	userSID, err := currentUserSIDString()
	if err != nil {
		t.Skipf("cannot read this user's SID: %v", err)
	}
	name := `\\.\pipe\nvx-ipc-adopt-probe-` + stdioSessionID(t.Name()+time.Now().Format("150405.000"))
	server, err := createNamedPipeWithSecurity(name, "D:(A;;GA;;;"+userSID+")")
	if err != nil {
		t.Fatalf("create pipe: %v", err)
	}
	defer syscall.CloseHandle(server)

	// The parent opens the pipe and hands it to its child as fd 3, which is what
	// nvx's preload would do. NODE_CHANNEL_FD tells the child which descriptor
	// carries the channel -- the same mechanism node's own fork uses.
	parent := `
const fs = require('fs'), cp = require('child_process');
const fd = fs.openSync(process.env.PROBE_PIPE, 'r+');
const child = cp.spawn(process.execPath,
  ['-e', 'process.send({hello:"ipc"}); setTimeout(() => process.exit(0), 1500);'],
  { stdio: ['ignore', 'inherit', 'inherit', fd],
    env: Object.assign({}, process.env, { NODE_CHANNEL_FD: '3' }) });
child.on('exit', c => { console.log('CHILD-EXIT ' + c); process.exit(0); });
setTimeout(() => { console.log('CHILD-TIMEOUT'); process.exit(1); }, 20000);
`
	script := filepath.Join(tempDir(t), "ipcparent.cjs")
	if err := os.WriteFile(script, []byte(parent), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("node", script)
	cmd.Env = append(os.Environ(), "PROBE_PIPE="+name)
	done := make(chan string, 1)
	go func() {
		out, _ := cmd.CombinedOutput()
		done <- string(out)
	}()

	read := make(chan string, 1)
	go func() {
		if !acceptPipeClient(server) {
			read <- ""
			return
		}
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
	said := <-done

	if strings.Contains(got, "hello") {
		t.Fatalf("node ADOPTED an fs.open'd pipe as its IPC channel, contradicting the libuv assertion "+
			"this test pins. That means brokered IPC -- and a contained `npx vitest run` -- may now be "+
			"possible; revisit sandbox_stdio_shim.js, which currently refuses fork() outright.\n"+
			"channel carried: %q", got)
	}
	if !strings.Contains(said, "UV_HANDLE_NON_OVERLAPPED_PIPE") {
		t.Errorf("the channel carried nothing, as expected, but not for the reason on record. "+
			"Expected libuv's non-overlapped assertion.\nnode said:\n%s", said)
	}
}
