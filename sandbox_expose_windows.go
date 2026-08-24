//go:build windows

package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Publishing a port from inside the sandbox to the host's loopback.
//
// Windows refuses connections INTO an AppContainer. A dev server started in the
// sandbox binds its port and reports itself listening, and nothing on the host
// can reach it -- `nvx npx vite` serving nobody is this, and it is not fixable by
// granting a capability, because the refusal is on the inbound direction rather
// than on anything the container holds.
//
// Nothing needs to connect inward if the connection is established outward and
// reused. The contained side dials the parent over AF_UNIX, which crosses the
// boundary (TestAppContainerCanReachAFUnixSocket), and parks the connection. The
// parent listens on the host's loopback and splices each arriving request onto a
// parked tunnel. Inside the container the supervisor bridges the other end to
// 127.0.0.1:<port>, which works because intra-container loopback does
// (TestAppContainerIntraContainerLoopback).
//
// The load-bearing property, and the reason this is worth building rather than
// swapping AppContainer for a restricted token: it needs NO network capability.
// Egress stays exactly as restricted as it was. That was measured before any of
// this was written -- see TestReverseRelayReachesAServerInsideTheContainer, which
// asserts both that the host reaches the contained server and that the container
// still cannot reach 1.1.1.1.
const (
	// Tunnels parked per exposed port. Enough that a page load's parallel
	// requests do not queue behind a dial, small enough to be unremarkable to the
	// server on the other end.
	exposeTunnelPoolSize = 8

	// How long an inbound host connection waits for a free tunnel before being
	// dropped. Generous: the alternative to waiting is a failed page load, and
	// the pool refills in milliseconds.
	exposeTunnelWait = 20 * time.Second

	exposeDialTimeout = 5 * time.Second
)

// windowsExposeSocketPath is where the parent listens for tunnels for one port.
// Mirrors windowsEgressSocketPath: inside the guest home, which already carries
// the grants the container needs, so no extra ACL work is required.
func windowsExposeSocketPath(guestHome string, port int) string {
	return filepath.Join(guestHome, fmt.Sprintf(".nvx-expose-%d.sock", port))
}

// exposedPortListener is the parent's half: a host loopback listener plus the
// AF_UNIX socket the contained side dials.
type exposedPortListener struct {
	mapping  exposeMapping
	hostPort int
	hostLn   net.Listener
	tunnelL  net.Listener
	tunnels  chan net.Conn
}

// publishExposedPort sets up the parent side for one mapping. It returns an
// error rather than warning and continuing: a developer who asked for a port and
// did not get it should be told, not left wondering why the browser hangs.
func publishExposedPort(ctx context.Context, guestHome string, m exposeMapping) (*exposedPortListener, error) {
	sock := windowsExposeSocketPath(guestHome, m.Container)
	// A leftover file from a previous run makes bind fail with "address already
	// in use" even though nothing holds it.
	_ = os.Remove(sock)
	tunnelL, err := net.Listen("unix", sock)
	if err != nil {
		return nil, fmt.Errorf("tunnel socket for port %d: %w", m.Container, err)
	}

	// Loopback only. Publishing a sandboxed server on every interface would put
	// it on the network of whatever coffee shop the laptop is in, which is not
	// what "reachable from the host" is meant to mean.
	//
	// Host port 0 asks the OS for a free one, which is the default because the
	// obvious choice -- the same number the server uses inside -- cannot work:
	// the container shares this network stack, so binding it here is what stops
	// the contained server binding it there.
	hostLn, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", m.Host))
	if err != nil {
		_ = tunnelL.Close()
		return nil, fmt.Errorf("host port %d is not available: %w", m.Host, err)
	}

	e := &exposedPortListener{
		mapping:  m,
		hostPort: hostLn.Addr().(*net.TCPAddr).Port,
		hostLn:   hostLn,
		tunnelL:  tunnelL,
		tunnels:  make(chan net.Conn, exposeTunnelPoolSize),
	}
	go e.acceptTunnels()
	go e.acceptHost(ctx)
	return e, nil
}

func (e *exposedPortListener) acceptTunnels() {
	for {
		c, err := e.tunnelL.Accept()
		if err != nil {
			return
		}
		select {
		case e.tunnels <- c:
		default:
			// Pool full. Closing is correct rather than wasteful: the contained
			// side dials again as soon as one is consumed, so the pool tracks
			// demand instead of growing to match a burst that has passed.
			_ = c.Close()
		}
	}
}

func (e *exposedPortListener) acceptHost(ctx context.Context) {
	for {
		inbound, err := e.hostLn.Accept()
		if err != nil {
			return
		}
		go func(inbound net.Conn) {
			select {
			case tun := <-e.tunnels:
				spliceConns(inbound, tun)
			case <-time.After(exposeTunnelWait):
				// No tunnel arrived. The contained process is gone, or never
				// listened. Closing gives the client a refusal instead of a hang.
				_ = inbound.Close()
			case <-ctx.Done():
				_ = inbound.Close()
			}
		}(inbound)
	}
}

func (e *exposedPortListener) Close() {
	_ = e.hostLn.Close()
	_ = e.tunnelL.Close()
	for {
		select {
		case c := <-e.tunnels:
			_ = c.Close()
		default:
			return
		}
	}
}

// startExposeTunnels is the contained side: keep tunnels parked with the parent,
// and bridge each one to the server inside the container when it carries a
// request.
func startExposeTunnels(ctx context.Context, guestHome string, containerPort int) {
	sock := windowsExposeSocketPath(guestHome, containerPort)
	local := fmt.Sprintf("127.0.0.1:%d", containerPort)
	for i := 0; i < exposeTunnelPoolSize; i++ {
		go maintainExposeTunnel(ctx, sock, local)
	}
}

func maintainExposeTunnel(ctx context.Context, sock, local string) {
	for ctx.Err() == nil {
		tun, err := net.DialTimeout("unix", sock, exposeDialTimeout)
		if err != nil {
			// The parent has not created it yet, or has gone. Either way there is
			// nothing to do but wait; the target's lifetime bounds this loop.
			select {
			case <-ctx.Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
			continue
		}

		// Wait for the parent to send something before dialling the local server.
		//
		// The prototype dialled immediately on parking a tunnel, which left one
		// idle connection per pool slot open against the user's dev server from
		// the moment it started. Servers that time out idle connections would
		// close them, the tunnel would collapse, and the pool would churn. Reading
		// first means a connection to the server exists only when a request is
		// actually in flight.
		first := make([]byte, 4096)
		n, rerr := tun.Read(first)
		if rerr != nil || n == 0 {
			_ = tun.Close()
			continue
		}

		conn, derr := net.DialTimeout("tcp", local, exposeDialTimeout)
		if derr != nil {
			// Nothing is listening on that port in here yet. The request is lost,
			// which is the honest outcome -- the alternative is holding a browser
			// open against a server that may never start.
			_ = tun.Close()
			continue
		}
		if _, werr := conn.Write(first[:n]); werr != nil {
			_ = conn.Close()
			_ = tun.Close()
			continue
		}
		spliceConns(tun, conn)
	}
}

// spliceConns copies in both directions until either side is done, then closes
// both. Half-close is deliberately not preserved: an HTTP client that finishes
// its request and waits for a response needs the other direction to stay open,
// and closing both on the first EOF would cut the response short -- so each
// direction runs to completion before anything is closed.
func spliceConns(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(a, b)
		closeWrite(a)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(b, a)
		closeWrite(b)
	}()
	wg.Wait()
	_ = a.Close()
	_ = b.Close()
}

// closeWrite signals end-of-stream to the peer without tearing down the other
// direction, where the connection type supports it.
func closeWrite(c net.Conn) {
	type writeCloser interface{ CloseWrite() error }
	if wc, ok := c.(writeCloser); ok {
		_ = wc.CloseWrite()
	}
}
