//go:build windows

package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

// Pipes for contained code that streams a child's output.
//
// A process inside an AppContainer cannot CREATE a named pipe -- CreateNamedPipeW
// returns ERROR_ACCESS_DENIED -- and Windows builds piped child stdio out of
// named pipes. So `spawn(cmd, {stdio: 'pipe'})` blocks inside libuv before the
// child exists. Synchronous capture was solved with temp files, whose contract a
// file satisfies exactly; a stream is not a file, and this is what has been
// stranding a process on this machine every time a contained `npx vitest` ran.
//
// Opening an existing pipe is a different access check from creating one, and it
// is permitted when the pipe's DACL names both the user and the container's
// package identity. Two probes established the rest:
// TestAppContainerCanConnectToAParentCreatedNamedPipe (a contained process can
// open one and move data) and TestContainedChildCanGiveAHostPipeToItsOwnChild
// (it can hand that handle to its own child as that child's stdout).
//
// So nvx creates the pipes and contained code only ever opens them.
//
// Two pipes per stream, not one, and the reason is structural rather than a
// choice: a named pipe joins a server to a client, so two clients cannot talk to
// each other. The grandchild writes into pipe A whose server end nvx holds
// outside the container; node cannot read its sibling's bytes from its own
// handle on A, because on a duplex pipe that handle carries what the SERVER
// wrote. nvx therefore pumps A into pipe B, which node opens and reads.
//
//	grandchild --> A --> nvx --> B --> node
//
// Nothing here weakens containment. Both endpoints are inside the same sandbox;
// nvx is moving bytes between two of its own children, and it is already their
// parent.

// stdioChannel is one stream's worth of plumbing.
type stdioChannel struct {
	// childPipe is opened by the contained node process and handed to its child
	// as that child's stdout/stderr. The grandchild writes; nvx reads.
	childPipe string
	// nodePipe is opened and read by the contained node process, and carries
	// what nvx read from childPipe.
	nodePipe string

	childServer syscall.Handle
	nodeServer  syscall.Handle
	// closeOnce guards nodeServer, which both the pump (at end of stream) and
	// Close (at end of session) want to close.
	closeOnce sync.Once
}

// stdioBroker owns the provisioned channels and the goroutines pumping them.
type stdioBroker struct {
	channels []*stdioChannel
	closeMu  sync.Mutex
	closed   bool
}

// stdioChannelPoolSize is how many streams can be captured at once.
//
// Pre-provisioned rather than requested at runtime, which removes a whole
// request/response protocol between the preload and nvx: the names go over in
// the environment and the preload takes the next free pair. A command that wants
// more streams than this falls back to the existing behaviour rather than
// failing, so the cost of the cap is the old limitation, not a new error.
//
// Counted in STREAMS, and a `stdio:'pipe'` spawn takes two of them, so this is
// half as many children as it looks. Eight streams meant four concurrent piped
// children while every document said eight -- measured by an acceptance review,
// which found the fifth child wedging the whole process.
//
// Sixteen because a test runner with a worker per core is the workload that
// exists, and eight children covers it on this machine. The number still only
// moves the cliff; what removes it is the preload falling back to temp files
// when the pool is empty, so exhaustion costs streaming rather than the command.
const stdioChannelPoolSize = 16

// nvxStdioChannelsEnv carries the provisioned names to the preload.
const nvxStdioChannelsEnv = "NVX_STDIO_CHANNELS"

// provisionStdioChannels creates the pool for one sandbox session.
//
// Best-effort by design: if any of it fails the caller carries on without the
// channels and contained streaming keeps hanging exactly as it does today. A
// failure to provide a workaround must not become a failure to run the command.
func provisionStdioChannels(containerSID string, sessionID string) (*stdioBroker, string) {
	if containerSID == "" {
		return nil, ""
	}
	// Both identities have to be granted: an AppContainer's access check is
	// satisfied only when the DACL allows the user the process runs as AND its
	// package identity, and either ACE alone reads as a flat denial. That is
	// measured, and it is the reason an earlier probe nearly concluded the whole
	// approach was impossible.
	//
	// The user ACE names THIS user. It said `WD` -- Everyone -- until an
	// acceptance review opened one of these pipes from an ordinary process and
	// showed the whole set enumerable by any local principal. That value was
	// copied out of the probe, where Everyone was deliberately the upper-bound
	// case, and it should never have left it.
	userSID, err := currentUserSIDString()
	if err != nil {
		return nil, "" // no channels rather than a pipe open to everyone
	}
	sddl := "D:(A;;GA;;;" + userSID + ")(A;;GA;;;" + containerSID + ")"

	broker := &stdioBroker{}
	var names []string
	for i := 0; i < stdioChannelPoolSize; i++ {
		ch, err := newStdioChannel(sessionID, i, sddl)
		if err != nil {
			broker.Close()
			return nil, ""
		}
		broker.channels = append(broker.channels, ch)
		names = append(names, ch.childPipe+"|"+ch.nodePipe)
	}

	for _, ch := range broker.channels {
		go ch.pump()
	}
	return broker, strings.Join(names, ";")
}

// currentUserSIDString returns the SID of the user this process runs as.
//
// Note what this does and does not buy. It stops another local account reaching
// these pipes. It cannot stop another process running as the SAME user, because
// the contained process's own token carries this user's identity, so the ACE
// that lets the sandbox in necessarily lets the user in. Anything already
// running as this user could read the project and the audit log anyway; the
// pipes are inside that existing boundary, not outside it. SECURITY.md says so
// in those words rather than the stronger thing it used to claim.
func currentUserSIDString() (string, error) {
	var token syscall.Token
	proc, _, _ := procGetCurrentProcess.Call()
	if r, _, err := procOpenProcessToken.Call(
		proc, uintptr(TOKEN_QUERY), uintptr(unsafe.Pointer(&token)),
	); r == 0 {
		return "", fmt.Errorf("OpenProcessToken: %v", err)
	}
	defer syscall.CloseHandle(syscall.Handle(token))

	user, err := token.GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("GetTokenUser: %w", err)
	}
	s, err := user.User.Sid.String()
	if err != nil {
		return "", fmt.Errorf("SID to string: %w", err)
	}
	return s, nil
}

func newStdioChannel(sessionID string, index int, sddl string) (*stdioChannel, error) {
	base := fmt.Sprintf(`\\.\pipe\nvx-stdio-%s-%d`, sessionID, index)
	ch := &stdioChannel{childPipe: base + "-c", nodePipe: base + "-n"}

	var err error
	if ch.childServer, err = createNamedPipeWithSecurity(ch.childPipe, sddl); err != nil {
		return nil, err
	}
	if ch.nodeServer, err = createNamedPipeWithSecurity(ch.nodePipe, sddl); err != nil {
		syscall.CloseHandle(ch.childServer)
		return nil, err
	}
	return ch, nil
}

// pump copies what the grandchild wrote into the pipe the contained node reads.
//
// Blocking waits on both ends: neither connects until the contained process
// actually uses this channel, and a channel nobody uses simply parks here until
// the session ends and Close cancels it.
func (c *stdioChannel) pump() {
	if !acceptPipeClient(c.childServer) {
		return
	}
	if !acceptPipeClient(c.nodeServer) {
		return
	}
	_, _ = io.Copy(pipeWriter{c.nodeServer}, pipeReader{c.childServer})

	// Closed, not disconnected. DisconnectNamedPipe tears the connection down
	// under the client, which reaches node as EPIPE on a read -- an error event
	// that killed the contained process outright. Closing the last server handle
	// ends the stream the way the end of any stream should look.
	c.closeOnce.Do(func() {
		syscall.CloseHandle(c.nodeServer)
		c.nodeServer = syscall.InvalidHandle
	})
}

// Close tears the pool down. Cancelling pending I/O first: a pump parked in
// ConnectNamedPipe is not released by closing the handle, which is how an
// earlier probe hung for ten minutes.
func (b *stdioBroker) Close() {
	if b == nil {
		return
	}
	b.closeMu.Lock()
	defer b.closeMu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for _, ch := range b.channels {
		if h := ch.childServer; h != 0 && h != syscall.InvalidHandle {
			procCancelIoExBroker.Call(uintptr(h), 0)
			syscall.CloseHandle(h)
		}
		// Read INSIDE the Do, not before it. sync.Once serialises the bodies,
		// not a read that happens outside one -- so lifting this out raced the
		// pump goroutine's write to the same field. `go test -race` caught it
		// every time under load and about 3% of the time in isolation, and CI
		// runs -race, which is what made a suite that passes ten times in a row
		// fail once with nothing to point at.
		ch.closeOnce.Do(func() {
			if node := ch.nodeServer; node != 0 && node != syscall.InvalidHandle {
				procCancelIoExBroker.Call(uintptr(node), 0)
				syscall.CloseHandle(node)
			}
		})
	}
}

// acceptPipeClient waits for the contained side to open this pipe.
func acceptPipeClient(server syscall.Handle) bool {
	ret, _, callErr := procConnectNamedPipeBroker.Call(uintptr(server), 0)
	if ret != 0 {
		return true
	}
	// ERROR_PIPE_CONNECTED means the client got there first, which is success.
	const errPipeConnected = 535
	errno, ok := callErr.(syscall.Errno)
	return ok && uintptr(errno) == errPipeConnected
}

// pipeReader / pipeWriter adapt a pipe handle to io.Reader / io.Writer so the
// copy is the standard library's rather than a hand-rolled loop.
type pipeReader struct{ h syscall.Handle }

func (r pipeReader) Read(p []byte) (int, error) {
	var n uint32
	if err := syscall.ReadFile(r.h, p, &n, nil); err != nil {
		return 0, io.EOF
	}
	if n == 0 {
		return 0, io.EOF
	}
	return int(n), nil
}

type pipeWriter struct{ h syscall.Handle }

func (w pipeWriter) Write(p []byte) (int, error) {
	var n uint32
	if err := syscall.WriteFile(w.h, p, &n, nil); err != nil {
		return int(n), err
	}
	return int(n), nil
}

// createNamedPipeWithSecurity creates a byte-mode server end carrying sddl.
func createNamedPipeWithSecurity(name, sddl string) (syscall.Handle, error) {
	const (
		pipeAccessDuplex = 0x00000003
		pipeTypeByte     = 0x00000000
		pipeWait         = 0x00000000
		pipeUnlimited    = 255
	)
	sd, err := securityDescriptorFromSDDLBroker(sddl)
	if err != nil {
		return syscall.InvalidHandle, err
	}
	defer syscall.LocalFree(syscall.Handle(sd))

	sa := brokerSecAttrs{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(sa))

	p, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return syscall.InvalidHandle, err
	}
	h, _, callErr := procCreateNamedPipeBroker.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(pipeAccessDuplex),
		uintptr(pipeTypeByte|pipeWait),
		uintptr(pipeUnlimited),
		65536, 65536, 0,
		uintptr(unsafe.Pointer(&sa)),
	)
	if h == uintptr(syscall.InvalidHandle) {
		return syscall.InvalidHandle, fmt.Errorf("CreateNamedPipeW(%s): %w", name, callErr)
	}
	return syscall.Handle(h), nil
}

func securityDescriptorFromSDDLBroker(sddl string) (uintptr, error) {
	p, err := syscall.UTF16PtrFromString(sddl)
	if err != nil {
		return 0, err
	}
	var sd uintptr
	const sddlRevision1 = 1
	ret, _, callErr := procConvertStringSDToSDBroker.Call(
		uintptr(unsafe.Pointer(p)), sddlRevision1,
		uintptr(unsafe.Pointer(&sd)), 0,
	)
	if ret == 0 {
		return 0, fmt.Errorf("ConvertStringSecurityDescriptorToSecurityDescriptorW: %w", callErr)
	}
	return sd, nil
}

type brokerSecAttrs struct {
	Length             uint32
	SecurityDescriptor uintptr
	InheritHandle      uint32
}

// addStdioChannelsEnv puts the provisioned names where the preload can find them.
func addStdioChannelsEnv(env []string, names string) []string {
	if names == "" {
		return env
	}
	return append(env, nvxStdioChannelsEnv+"="+names)
}

// stdioSessionID derives a short, filesystem-safe id for the pipe names.
func stdioSessionID(sandboxID string) string {
	id := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return -1
	}, strings.ToLower(sandboxID))
	if id == "" {
		id = fmt.Sprintf("%d", os.Getpid())
	}
	if len(id) > 16 {
		id = id[:16]
	}
	return id
}

var (
	procCreateNamedPipeBroker     = modKernel32.NewProc("CreateNamedPipeW")
	procConnectNamedPipeBroker    = modKernel32.NewProc("ConnectNamedPipe")
	procCancelIoExBroker          = modKernel32.NewProc("CancelIoEx")
	procConvertStringSDToSDBroker = modAdvapi32.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW")
)
