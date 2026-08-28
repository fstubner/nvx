//go:build windows

package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Letting a contained process reach one named service on the host.
//
// The sandbox has no route to your machine, and that is the point: Windows
// refuses an AppContainer's loopback connections, and the egress relay
// deliberately declines host loopback destinations
// (TestRelayDoesNotExposeHostLoopbackServices). A contained tool that needs to
// talk to something you are already running -- a browser with remote debugging
// on, a local database, a device emulator -- has no way to do it.
//
// This is the narrow way through, and it is the mirror of --expose. Where
// --expose carries a connection from the host INTO the sandbox, this carries one
// from the sandbox OUT to a single named port:
//
//	contained tool → 127.0.0.1:<inside>   (a listener the supervisor runs)
//	                    ↓ AF_UNIX, which crosses the boundary
//	                 nvx, outside → 127.0.0.1:<host>   (the real service)
//
// nvx dials the host end itself. The sandbox never gets a general route out; it
// gets a pipe to one port that someone named.
//
// This is NOT the loopback exemption that was removed in 0.5.0. That opened
// every service on 127.0.0.1 to every sandbox on the machine, permanently, via a
// machine-wide Windows setting nvx could not revoke without elevation. This is
// one port, for one run, over a socket nvx owns and closes.
//
// Simpler than the --expose tunnels, and worth saying why: there is no pool of
// parked connections here. --expose needs them because the connection has to be
// established outward before the host has anything to send. Here the contained
// side initiates, so it can dial when it actually has traffic.

// windowsConnectSocketPath is where the parent accepts tunnel connections for
// one host port. Inside the guest home, like the egress and expose sockets, so
// it needs no extra ACL work.
func windowsConnectSocketPath(guestHome string, hostPort int) string {
	return filepath.Join(guestHome, fmt.Sprintf(".nvx-connect-%d.sock", hostPort))
}

// connectHostListener is the parent's half: it accepts tunnel connections from
// inside the sandbox and joins each to the real service on the host.
type connectHostListener struct {
	mapping  connectMapping
	tunnelL  net.Listener
	hostAddr string
	warnOnce sync.Once
}

// refuseOnce reports a rejected peer a single time. A port scanner would
// otherwise turn a real warning into a flood that buries it.
func (c *connectHostListener) refuseOnce(err error) {
	c.warnOnce.Do(func() {
		// A nil error is the ordinary rejection: the peer was identified and simply
		// is not ours. An error means the question could not be answered, which
		// fails closed too but is worth distinguishing -- one is another sandbox
		// reaching for the port, the other is nvx unable to tell.
		if err == nil {
			LogWarn("Refused a connection to 127.0.0.1:%d: it came from a process outside this sandbox.", c.mapping.Host)
			return
		}
		LogWarn("Refused a connection to 127.0.0.1:%d: could not confirm it came from this sandbox (%v).", c.mapping.Host, err)
	})
}

// peerHeader is what the supervisor sends before the tunnelled bytes: the
// loopback source and destination ports of the connection it accepted, big
// endian. Four bytes, fixed -- it is read by the parent, written by nvx's own
// supervisor, and never seen by the contained tool.
const peerHeaderLen = 4

func writePeerHeader(w net.Conn, srcPort, dstPort uint16) error {
	var b [peerHeaderLen]byte
	binary.BigEndian.PutUint16(b[0:2], srcPort)
	binary.BigEndian.PutUint16(b[2:4], dstPort)
	_, err := w.Write(b[:])
	return err
}

func readPeerHeader(r net.Conn) (srcPort, dstPort uint16, err error) {
	var b [peerHeaderLen]byte
	_ = r.SetReadDeadline(time.Now().Add(exposeDialTimeout))
	if _, err = io.ReadFull(r, b[:]); err != nil {
		return 0, 0, err
	}
	_ = r.SetReadDeadline(time.Time{})
	return binary.BigEndian.Uint16(b[0:2]), binary.BigEndian.Uint16(b[2:4]), nil
}

// maxWindowsUnixSocketPath is how long an AF_UNIX path may be on Windows.
// Measured 2026-08-28 on Windows 11 26200: 107 characters bind, 108 fails.
//
// Named because the failure is otherwise undiagnosable -- the syscall returns a
// bare "bind: invalid argument" against a path that plainly exists and is
// writable, with nothing pointing at its length. A long NVX_HOME is all it takes
// (a guest home here is ~50 characters, so there is room, but not a lot).
const maxWindowsUnixSocketPath = 107

// openConnectPort sets up the parent side for one mapping.
func openConnectPort(ctx context.Context, guestHome string, m connectMapping) (*connectHostListener, error) {
	sock := windowsConnectSocketPath(guestHome, m.Host)
	if len(sock) > maxWindowsUnixSocketPath {
		return nil, fmt.Errorf(
			"the tunnel socket path is %d characters and Windows allows %d: %s\n"+
				"Set NVX_HOME to a shorter directory", len(sock), maxWindowsUnixSocketPath, sock)
	}
	_ = os.Remove(sock)
	tunnelL, err := net.Listen("unix", sock)
	if err != nil {
		return nil, fmt.Errorf("tunnel socket for host port %d: %w", m.Host, err)
	}

	c := &connectHostListener{
		mapping:  m,
		tunnelL:  tunnelL,
		hostAddr: fmt.Sprintf("127.0.0.1:%d", m.Host),
	}
	go c.accept(ctx)
	return c, nil
}

func (c *connectHostListener) accept(ctx context.Context) {
	for {
		inbound, err := c.tunnelL.Accept()
		if err != nil {
			return
		}
		go func(inbound net.Conn) {
			if ctx.Err() != nil {
				_ = inbound.Close()
				return
			}
			// Who is actually on the other end? The supervisor reports the loopback
			// port it accepted from; this side resolves it to a process and checks
			// it belongs to this sandbox. See sandbox_connect_peer_windows.go for
			// why the question has to be asked and why it is answered out here.
			srcPort, dstPort, herr := readPeerHeader(inbound)
			if herr != nil {
				_ = inbound.Close()
				return
			}
			ok, verr := verifyTunnelPeer(srcPort, dstPort)
			if !ok {
				// Refuse, and say so once. Silence here would look exactly like the
				// host service being down, and this is the case worth knowing about:
				// something outside this sandbox reached for a port it was not given.
				c.refuseOnce(verr)
				_ = inbound.Close()
				return
			}
			// Loopback only, and only the port that was named. The contained side
			// chooses when to connect, never where.
			host, derr := net.DialTimeout("tcp", c.hostAddr, exposeDialTimeout)
			if derr != nil {
				// Nothing listening on the host. Closing gives the contained tool a
				// refusal rather than a hang, which is what it would have got had it
				// been able to dial the port itself.
				_ = inbound.Close()
				return
			}
			spliceConns(inbound, host)
		}(inbound)
	}
}

func (c *connectHostListener) Close() { _ = c.tunnelL.Close() }

// startConnectListeners is the contained side: listen on the in-sandbox port and
// forward each connection to the parent over the AF_UNIX socket.
func startConnectListeners(ctx context.Context, guestHome string, m connectMapping) (int, error) {
	sock := windowsConnectSocketPath(guestHome, m.Host)

	// Inside 0 asks the OS for a free port. It cannot default to the host's own
	// number: the container shares the host's network stack, so binding it here
	// would collide with the very service being reached.
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", m.Inside))
	if err != nil {
		return 0, fmt.Errorf("in-sandbox listener for host port %d: %w", m.Host, err)
	}
	inside := ln.Addr().(*net.TCPAddr).Port

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	go func() {
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go func(conn net.Conn) {
				tun, derr := net.DialTimeout("unix", sock, exposeDialTimeout)
				if derr != nil {
					_ = conn.Close()
					return
				}
				// Report who connected before anything is forwarded. The parent
				// resolves these ports to a process and refuses the tunnel if it is
				// not one this sandbox launched -- this side cannot do that itself
				// (GetExtendedTcpTable is ACCESS_DENIED in an AppContainer).
				src, sok := conn.RemoteAddr().(*net.TCPAddr)
				dst, dok := conn.LocalAddr().(*net.TCPAddr)
				if !sok || !dok || writePeerHeader(tun, uint16(src.Port), uint16(dst.Port)) != nil {
					_ = conn.Close()
					_ = tun.Close()
					return
				}
				spliceConns(conn, tun)
			}(conn)
		}
	}()
	return inside, nil
}

// connectEnvVar names the variable that tells the contained tool where to dial.
//
// The in-sandbox port cannot be the host's, so a tool cannot simply use the
// number its documentation gives. Publishing it in the environment means a
// wrapper script or a tool that reads its endpoint from configuration needs
// nothing hardcoded: NVX_CONNECT_9222=19222 for `--connect 9222`.
func connectEnvVar(hostPort int) string {
	return "NVX_CONNECT_" + strconv.Itoa(hostPort)
}

// freeLoopbackPort asks the OS for a port and returns it. Racy in principle, and
// the same approach the expose listener uses; the window is microseconds and the
// alternative is guessing a number that might already be taken.
func freeLoopbackPort() int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}
