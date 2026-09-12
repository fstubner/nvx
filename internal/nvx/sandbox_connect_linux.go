//go:build linux

package nvx

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
)

// Letting a contained process on Linux reach one named service on your machine.
//
// The third shape of the same feature, and the one closest to Windows. In every
// mode but `open` the sandbox runs in a network namespace of its own, so your
// loopback is not merely denied to it: 127.0.0.1 inside that namespace is a
// different 127.0.0.1, and no permission grants a route to yours. The traffic
// has to cross the boundary the way the egress proxy's already does, over a
// UNIX socket, which is a filesystem object and so is not namespaced.
//
//	contained tool → 127.0.0.1:<inside>   (a listener the supervisor runs, inside the netns)
//	                    ↓ AF_UNIX in the guest home
//	                 nvx, outside → 127.0.0.1:<host>   (the real service)
//
// The socket lives in the guest home, which already carries full Landlock
// read/write rights and belongs to this run alone, so it needs no extra rule and
// no peer check: another sandbox has its own namespace and its own guest home,
// and cannot see either half of this.
//
// Not available in `offline` or `loopback` mode, and connectUnsupportedForMode
// says why.

// linuxConnectSocketPath is where the parent accepts tunnel connections for one
// host port, alongside the egress socket and for the same reason.
func linuxConnectSocketPath(guestHome string, hostPort int) string {
	return filepath.Join(guestHome, fmt.Sprintf(".nvx-connect-%d.sock", hostPort))
}

// openConnectSockets is the parent's half: one UNIX socket per host port,
// outside the namespace, each joining the sandbox's tunnel to the real service.
//
// It resolves the in-sandbox port too, which must happen here rather than in the
// supervisor: `--connect 9222` with no second number has no port until one is
// picked, and the contained side must never be the one to choose where or how it
// can dial.
func openConnectSockets(guestHome string, netCtx *NetworkLaunchContext) (env []string, stop func(), err error) {
	noop := func() {}
	if netCtx == nil || len(netCtx.ConnectPorts) == 0 {
		return nil, noop, nil
	}

	var listeners []net.Listener
	stopAll := func() {
		for _, ln := range listeners {
			_ = ln.Close() // removes the socket file: Go unlinks it on close
		}
	}

	for i, m := range netCtx.ConnectPorts {
		sock := linuxConnectSocketPath(guestHome, m.Host)
		// A socket file left by a run that was killed rather than closed would
		// make Listen fail with EADDRINUSE, and the guest home is this run's own.
		_ = os.Remove(sock)
		ln, lerr := net.Listen("unix", sock)
		if lerr != nil {
			stopAll()
			return nil, noop, fmt.Errorf("tunnel socket for host port %d: %w", m.Host, lerr)
		}
		listeners = append(listeners, ln)

		if netCtx.ConnectPorts[i].Inside == 0 {
			netCtx.ConnectPorts[i].Inside = freeLoopbackPort()
		}
		inside := netCtx.ConnectPorts[i].Inside
		go serveConnectSocket(ln, m.Host)

		LogWarn("The sandbox may reach 127.0.0.1:%d on this machine, as 127.0.0.1:%d inside it (%s).",
			m.Host, inside, connectEnvVar(m.Host))
		env = append(env, fmt.Sprintf("%s=%d", connectEnvVar(m.Host), inside))
	}
	return env, stopAll, nil
}

// serveConnectSocket joins each tunnel connection to the real service.
func serveConnectSocket(ln net.Listener, hostPort int) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return // closed: the command has exited
		}
		go func(c net.Conn) {
			svc, derr := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(hostPort), connectDialTimeout)
			if derr != nil {
				LogWarn("The sandbox tried to reach 127.0.0.1:%d and nothing is listening there: %v", hostPort, derr)
				_ = c.Close()
				return
			}
			spliceConns(c, svc)
		}(conn)
	}
}

// startContainedConnectListeners is the supervisor's half, inside the namespace:
// listen on the in-sandbox port and forward every connection to the parent's
// socket. Both numbers arrive from the parent, so nothing here chooses either.
func startContainedConnectListeners(ctx context.Context, guestHome string, mappings []connectMapping) (stop func(), err error) {
	noop := func() {}
	if len(mappings) == 0 {
		return noop, nil
	}

	var listeners []net.Listener
	stopAll := func() {
		for _, ln := range listeners {
			_ = ln.Close()
		}
	}

	for _, m := range mappings {
		ln, lerr := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(m.Inside))
		if lerr != nil {
			stopAll()
			return noop, fmt.Errorf("in-sandbox listener for host port %d: %w", m.Host, lerr)
		}
		listeners = append(listeners, ln)
		sock := linuxConnectSocketPath(guestHome, m.Host)
		go func(ln net.Listener, sock string, hostPort int) {
			for {
				conn, aerr := ln.Accept()
				if aerr != nil {
					return
				}
				go forwardToParentSocket(ctx, conn, sock, hostPort)
			}
		}(ln, sock, m.Host)
	}

	go func() {
		<-ctx.Done()
		stopAll()
	}()
	return stopAll, nil
}

// forwardToParentSocket hands one contained-side connection to the parent.
//
// startProxyRelay's relayConn does the same job for the egress proxy and is not
// reused: its failure message names the proxy, and a developer whose --connect
// tunnel failed would be told to look at their egress allowlist.
func forwardToParentSocket(ctx context.Context, client net.Conn, sock string, hostPort int) {
	var d net.Dialer
	upstream, err := d.DialContext(ctx, "unix", sock)
	if err != nil {
		LogWarn("The tunnel to 127.0.0.1:%d is gone: %v", hostPort, err)
		_ = client.Close()
		return
	}
	spliceConns(client, upstream)
}
