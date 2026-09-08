//go:build darwin

package main

import (
	"fmt"
	"net"
	"strconv"
	"sync"
)

// Letting a contained process on macOS reach one named service on your machine.
//
// The same feature as the Windows --connect, reached a different way, because
// what blocks the sandbox differs. Windows refuses an AppContainer's loopback
// connections outright, so the traffic has to cross the boundary over AF_UNIX.
// macOS shares your loopback with the sandbox and blocks it in the Seatbelt
// profile instead: in the default proxy mode the only permitted outbound
// destinations are the egress proxy's own ports, so a contained tool cannot
// reach your database or your browser's debugging port.
//
//	contained tool → 127.0.0.1:<inside>   (a listener nvx runs, outside the sandbox)
//	                    ↓
//	                 nvx → 127.0.0.1:<host>   (the real service)
//
// Two numbers, for the reason parseConnectSpec gives: nvx cannot put its
// listener on the port the real service already holds.
//
// The profile permits <inside> and nothing else, so the sandbox reaches nvx's
// listener rather than the service. That is worth the indirection even though
// macOS could simply permit <host> directly, because it keeps one meaning for
// --connect across platforms: the same command, the same NVX_CONNECT_<host>
// variable, and the same property that nvx decides where a contained process
// can dial while the contained process decides when.
//
// No peer check, unlike Windows. There the relay had to verify that the
// connecting process belongs to this sandbox, because every nvx sandbox on the
// machine shared one package identity and could otherwise reach another's
// tunnel. Here, a process outside any sandbox can already open 127.0.0.1:<host>
// itself -- the relay hands it nothing new -- and another sandbox cannot reach
// <inside>, because its own profile permits only its own proxy ports. The one
// mode where a sandbox may dial all of loopback is `loopback`, whose entire
// meaning is that it may.

// connectRelay is one host service the sandbox may reach, and the listener that
// carries traffic to it.
type connectRelay struct {
	hostPort int
	listener net.Listener
	warnOnce sync.Once
}

// startSeatbeltConnectRelays opens a listener for each --connect mapping and
// resolves the in-sandbox port, which must happen BEFORE the Seatbelt profile
// is rendered: the profile names the resolved port, and `--connect 9222` with
// no second number does not have one until the listener is bound.
//
// Returns the environment additions that tell the contained tool where to dial,
// and a stop function that closes every listener.
func startSeatbeltConnectRelays(netCtx *NetworkLaunchContext) (env []string, stop func(), err error) {
	noop := func() {}
	if netCtx == nil || len(netCtx.ConnectPorts) == 0 {
		return nil, noop, nil
	}

	var relays []*connectRelay
	stopAll := func() {
		for _, r := range relays {
			_ = r.listener.Close()
		}
	}

	for i, m := range netCtx.ConnectPorts {
		// Inside 0 asks the OS for a free port; the bound address is read back
		// below either way, so the resolved number is always the real one.
		ln, lerr := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(m.Inside))
		if lerr != nil {
			stopAll()
			return nil, noop, fmt.Errorf("in-sandbox listener for host port %d: %w", m.Host, lerr)
		}
		r := &connectRelay{hostPort: m.Host, listener: ln}
		relays = append(relays, r)

		inside := ln.Addr().(*net.TCPAddr).Port
		netCtx.ConnectPorts[i].Inside = inside
		go r.serve()

		// Loud, like the Windows path: this is a hole in the sandbox that
		// someone asked for, and a hole nobody sees is one nobody removes.
		LogWarn("The sandbox may reach 127.0.0.1:%d on this machine, as 127.0.0.1:%d inside it (%s).",
			m.Host, inside, connectEnvVar(m.Host))
		env = append(env, fmt.Sprintf("%s=%d", connectEnvVar(m.Host), inside))
	}
	return env, stopAll, nil
}

// serve joins each accepted connection to the real service.
func (r *connectRelay) serve() {
	for {
		conn, err := r.listener.Accept()
		if err != nil {
			return // the listener was closed: the command has exited
		}
		go r.forward(conn)
	}
}

func (r *connectRelay) forward(conn net.Conn) {
	svc, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(r.hostPort), connectDialTimeout)
	if err != nil {
		// Once. A tool that polls for its service to come up would otherwise
		// bury every other line of output, and the first one says everything the
		// later ones would.
		r.warnOnce.Do(func() {
			LogWarn("The sandbox tried to reach 127.0.0.1:%d and nothing is listening there: %v", r.hostPort, err)
		})
		_ = conn.Close()
		return
	}
	spliceConns(conn, svc)
}
