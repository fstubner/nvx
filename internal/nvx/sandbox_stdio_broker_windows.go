//go:build windows

package nvx

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

	// reverse turns the channel around for stdin: node writes, the grandchild
	// reads. The plumbing is identical -- two pipes, nvx holding both server
	// ends -- and only the direction of the copy differs, because the reason for
	// two pipes (a named pipe joins a server to a client, so two clients cannot
	// talk to each other) is symmetric.
	//
	//	stdout: grandchild --> childPipe --> nvx --> nodePipe --> node
	//	stdin:  node --> nodePipe --> nvx --> childPipe --> grandchild
	reverse bool

	// sddl is what every instance is created with, kept so that retiring a
	// used instance can create the next one under the same name with the same
	// access.
	sddl string

	// The instances currently behind the two names. Guarded by mu: the pump
	// snapshots them before blocking in I/O, retire replaces them at the end
	// of a use, and Close invalidates them from another goroutine at the end
	// of the session.
	//
	// One use per instance, many uses per name. A named-pipe instance is
	// single-shot -- once its client disconnects nothing can connect to it
	// again -- and the preload puts a name back on its free list when the
	// child that used it closes. Serving each name exactly once, as this used
	// to, left a dead pipe behind every recycled name.
	mu          sync.Mutex
	closed      bool
	childServer syscall.Handle
	nodeServer  syscall.Handle
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

// nvxStdinChannelsEnv carries the reverse-direction pool.
const nvxStdinChannelsEnv = "NVX_STDIN_CHANNELS"

// provisionStdioChannels creates the pool for one sandbox session.
//
// Best-effort by design: if any of it fails the caller carries on without the
// channels and contained streaming keeps hanging exactly as it does today. A
// failure to provide a workaround must not become a failure to run the command.
func provisionStdioChannels(containerSID string, sessionID string) (*stdioBroker, string, string) {
	if containerSID == "" {
		return nil, "", ""
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
		return nil, "", "" // no channels rather than a pipe open to everyone
	}
	sddl := "D:(A;;GA;;;" + userSID + ")(A;;GA;;;" + containerSID + ")"

	broker := &stdioBroker{}
	var names, stdinNames []string
	for i := 0; i < stdioChannelPoolSize; i++ {
		ch, err := newStdioChannel(sessionID, i, sddl, false)
		if err != nil {
			broker.Close()
			return nil, "", ""
		}
		broker.channels = append(broker.channels, ch)
		names = append(names, ch.childPipe+"|"+ch.nodePipe)
	}
	for i := 0; i < stdinChannelPoolSize; i++ {
		ch, err := newStdioChannel(sessionID, i, sddl, true)
		if err != nil {
			broker.Close()
			return nil, "", ""
		}
		broker.channels = append(broker.channels, ch)
		stdinNames = append(stdinNames, ch.childPipe+"|"+ch.nodePipe)
	}

	for _, ch := range broker.channels {
		go ch.pump()
	}
	return broker, strings.Join(names, ";"), strings.Join(stdinNames, ";")
}

// stdinChannelPoolSize is how many contained children can be WRITTEN to at once.
//
// Half the stream pool, because a `stdio:'pipe'` spawn takes two output streams
// and one input, so anything beyond this could not have its output streamed
// anyway. A spawn that finds the pool empty falls back to the empty-file stdin
// that was the only behaviour before, so running out costs the old limitation
// rather than a new failure.
const stdinChannelPoolSize = stdioChannelPoolSize / 2

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

func newStdioChannel(sessionID string, index int, sddl string, reverse bool) (*stdioChannel, error) {
	kind := "o"
	if reverse {
		kind = "i"
	}
	base := fmt.Sprintf(`\\.\pipe\nvx-stdio-%s-%s%d`, sessionID, kind, index)
	ch := &stdioChannel{childPipe: base + "-c", nodePipe: base + "-n", reverse: reverse, sddl: sddl}
	if err := ch.createInstances(); err != nil {
		return nil, err
	}
	return ch, nil
}

// createInstances puts a fresh server instance behind each name. The caller
// holds c.mu, or owns the channel outright as newStdioChannel does.
//
// Both or neither. A child instance with no node instance behind it would
// accept a client the pump could never serve, and that client's process would
// wait on it forever.
func (c *stdioChannel) createInstances() error {
	child, err := createNamedPipeWithSecurity(c.childPipe, c.sddl)
	if err != nil {
		return err
	}
	node, err := createNamedPipeWithSecurity(c.nodePipe, c.sddl)
	if err != nil {
		syscall.CloseHandle(child)
		return err
	}
	c.childServer, c.nodeServer = child, node
	return nil
}

// pump serves this channel for the life of the session: accept the two
// clients, copy until the writer leaves, retire the used instances, put fresh
// ones behind the same names, and go round again.
//
// It used to do that once and return, and the preload recycles names: the
// ninth piped child in a process -- the first to draw a name whose child had
// closed -- opened a dead pipe, and the preload's failure path was the raw
// spawn that blocks inside libuv. Measured: ten children run one after
// another, the first eight streamed and the ninth hung to the deadline.
//
// Blocking waits on both ends: a channel nobody uses parks in the first accept
// until the session ends and Close cancels it.
func (c *stdioChannel) pump() {
	for {
		child, node, ok := c.current()
		if !ok {
			return
		}
		if !acceptPipeClient(child) || !acceptPipeClient(node) {
			return // cancelled by Close, or the handle is gone
		}
		if c.reverse {
			// stdin: what node writes reaches the grandchild. When node closes
			// its end this returns, and the grandchild then has to see EOF -- a
			// tool that feeds a child input and waits for the answer hangs
			// forever otherwise, which is the failure this direction exists to
			// remove.
			_, _ = io.Copy(pipeWriter{child}, pipeReader{node})
		} else {
			_, _ = io.Copy(pipeWriter{node}, pipeReader{child})
		}
		if !c.retire(child, node) {
			return
		}
	}
}

// current returns the instances to serve next, or false once Close has run.
func (c *stdioChannel) current() (child, node syscall.Handle, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.childServer == syscall.InvalidHandle || c.nodeServer == syscall.InvalidHandle {
		return 0, 0, false
	}
	return c.childServer, c.nodeServer, true
}

// retire ends one use of the channel and readies the next.
//
// Closed, not disconnected. DisconnectNamedPipe tears the connection down
// under the client, which reaches node as EPIPE on a read -- an error event
// that killed the contained process outright. Closing the last server handle
// on the destination ends the reader's stream the way the end of any stream
// should look, so the destination goes first; the source's client has already
// left. Then fresh instances behind the same names, so the name the preload
// is about to hand back to its free list has a live pipe behind it again.
//
// False once the session is over, or if the names cannot be re-created. In
// the second case this channel simply drops out: its name stays in the
// preload's list, opens on it fail, and the preload moves to the next one.
func (c *stdioChannel) retire(child, node syscall.Handle) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	first, second := node, child
	if c.reverse {
		first, second = child, node
	}
	for _, h := range []syscall.Handle{first, second} {
		// Compare against the fields rather than closing unconditionally: Close
		// may have taken them already, from its own goroutine.
		switch h {
		case c.childServer:
			syscall.CloseHandle(h)
			c.childServer = syscall.InvalidHandle
		case c.nodeServer:
			syscall.CloseHandle(h)
			c.nodeServer = syscall.InvalidHandle
		}
	}
	if c.closed {
		return false
	}
	return c.createInstances() == nil
}

// Close tears the pool down. Cancelling pending I/O first: a pump parked in
// ConnectNamedPipe is not released by closing the handle, which is how an
// earlier probe hung for ten minutes.
//
// Everything under the channel's mutex, because the pump's goroutine reads
// and replaces the same fields. An earlier shape with a sync.Once and one
// field read outside it raced the pump; `go test -race` caught it every time
// under load and about 3% of the time in isolation.
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
		ch.mu.Lock()
		ch.closed = true
		for _, h := range []*syscall.Handle{&ch.childServer, &ch.nodeServer} {
			if *h != 0 && *h != syscall.InvalidHandle {
				procCancelIoExBroker.Call(uintptr(*h), 0)
				syscall.CloseHandle(*h)
			}
			*h = syscall.InvalidHandle
		}
		ch.mu.Unlock()
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

// addStdinChannelsEnv puts the reverse-direction names where the preload can
// find them, under their own variable so an older preload -- which knows only
// the output pool -- ignores them instead of taking an input channel for an
// output one.
func addStdinChannelsEnv(env []string, names string) []string {
	if names == "" {
		return env
	}
	return append(env, nvxStdinChannelsEnv+"="+names)
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
