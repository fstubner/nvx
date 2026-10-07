//go:build linux

package nvx

import (
	"context"
	"fmt"
	"path/filepath"
)

// --expose on Linux, the parent's half. sandbox_expose.go has the tunnel.
//
// Outside network.mode open the contained process lives in a network namespace of
// its own, with a loopback that is not the host's. A server it starts listens in
// there, and a browser on the host has no route to it. --expose publishes one
// port on the host's loopback for one run.
//
//	browser → 127.0.0.1:<host>    (a listener nvx runs, outside the namespace)
//	             ↓ AF_UNIX in the guest home
//	          supervisor, inside → 127.0.0.1:<port>   (the contained server)
//
// The supervisor dials out to the socket and the parent never dials in, so the
// parent never connects to a path the contained process could have replaced. The
// socket is in the guest home, which belongs to this run alone. A contained
// process that swaps it for its own can only cut its own server off.

// linuxExposeSocketPath is where the parent listens for tunnels for one port,
// beside the egress and --connect sockets and for the same reason.
func linuxExposeSocketPath(guestHome string, port int) string {
	return filepath.Join(guestHome, fmt.Sprintf(".nvx-expose-%d.sock", port))
}

// exposeSocketPath is the name sandbox_expose.go gives it. On Linux the prefix it
// is handed is the guest home.
func exposeSocketPath(guestHome string, port int) string {
	return linuxExposeSocketPath(guestHome, port)
}

// publishExposedPorts opens the host listener and the tunnel socket for each
// mapping in netCtx and says where each is published. It returns an error when one
// cannot be opened. A developer who asked for a port and did not get it should be
// told, and not left wondering why the browser hangs.
//
// network.mode open has no namespace, so a contained server is already on the
// host's loopback and there is nothing to publish. The mappings are cleared so the
// supervisor is not told about them either.
func publishExposedPorts(guestHome, nvxHome string, netCtx *NetworkLaunchContext) (stop func(), err error) {
	noop := func() {}
	if netCtx == nil || len(netCtx.ExposePorts) == 0 {
		return noop, nil
	}
	if !networkModeRequiresNamespace(netCtx.Mode) {
		LogInfo("--expose is not needed with network.mode open. The sandbox shares your network, so a server in it is already reachable on its own port.")
		netCtx.ExposePorts = nil
		return noop, nil
	}
	// The mode that denies the sandbox every IP socket leaves a server nothing to
	// listen with, and connectRefusalFor says the same of --connect.
	if connectUnsupportedForMode(netCtx.Mode) {
		LogWarn("--expose cannot be honoured in network.mode %q on Linux. That mode denies the sandbox every IP socket, so a server in it cannot listen.", netCtx.Mode)
		netCtx.ExposePorts = nil
		return noop, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	var published []*exposedPortListener
	stopAll := func() {
		cancel()
		for _, e := range published {
			e.Close()
		}
	}
	for _, m := range netCtx.ExposePorts {
		sock := linuxExposeSocketPath(guestHome, m.Container)
		if err := linuxSocketTooLong("tunnel socket", sock, guestHome, nvxHome, netCtx); err != nil {
			stopAll()
			return noop, err
		}
		e, perr := publishExposedPort(ctx, guestHome, m)
		if perr != nil {
			stopAll()
			return noop, fmt.Errorf("port %d: %w", m.Container, perr)
		}
		published = append(published, e)
		// The host port is the one the developer types, and it is deliberately not
		// the one their dev server prints, so say both.
		LogInfo("Sandbox port %d is published at http://127.0.0.1:%d (the URL the server prints is only valid inside)",
			m.Container, e.hostPort)
	}
	return stopAll, nil
}
