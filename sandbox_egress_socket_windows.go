//go:build windows

package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// windowsEgressSocketName is the UNIX socket, inside the guest home, that the
// parent's egress proxy listens on in addition to its TCP listeners. The guest
// home is already granted to the AppContainer, so no extra ACL is needed to reach
// it -- and the name is kept short because of unixSocketPathMax below.
const windowsEgressSocketName = "egress.sock"

// unixSocketPathMax is the size of sockaddr_un.sun_path. Windows uses the same
// 108-byte field as Unix, and afunix.sys rejects anything longer with
// WSAEINVAL -- which surfaces from Go as "bind: invalid argument", a message
// indistinguishable from a permissions failure. The relay probe hit exactly this
// and it cost a wrong diagnosis, so the length is checked up front and reported
// as what it is.
const unixSocketPathMax = 108

// egressSocketPathFits reports whether path can be bound as an AF_UNIX socket.
// One byte is reserved for the terminating NUL.
func egressSocketPathFits(path string) bool {
	return path != "" && len(path) < unixSocketPathMax
}

func windowsEgressSocketPath(guestHome string) string {
	return filepath.Join(guestHome, windowsEgressSocketName)
}

// windowsEgressNeedsRelay reports whether this network mode routes the contained
// process through the parent's proxy.
//
//   - offline/loopback grant no capabilities and get no relay: with no
//     internetClient the AppContainer cannot reach the network at all, which is
//     the enforcement those modes ask for.
//   - open grants internetClient and connects directly, by request.
//   - everything else (proxy, the default) uses the relay.
func windowsEgressNeedsRelay(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "offline", "loopback", "open":
		return false
	default:
		return true
	}
}

// prepareEgressSocket exposes the parent's egress proxy on a UNIX socket so the
// contained process can reach it without holding any network capability.
//
// This is what makes the Windows egress allowlist enforced rather than advisory.
// The sandbox previously ran with the internetClient capability and HTTP_PROXY
// set, so honouring the proxy was the target's choice -- a package that called
// connect() directly reached anything it liked. Dropping internetClient closes
// that, but it also closes the route to the proxy's own loopback listener, which
// Windows blocks for AppContainers without an elevated exemption.
//
// AF_UNIX is the one channel that survives: it is a filesystem object rather than
// a network endpoint, so the AppContainer network restriction does not cover it.
// Measured with no capabilities granted at all (see the egress primitives probe):
// direct TCP to 1.1.1.1:443 is refused and DNS does not resolve, while this socket
// is reachable. The contained side then re-exposes it as loopback TCP for tools
// that only understand host:port -- see runAppContainerExecChild.
func prepareEgressSocket(egress *EgressProxy, guestHome string, netCtx *NetworkLaunchContext) error {
	if egress == nil || netCtx == nil || guestHome == "" {
		return nil
	}
	if !windowsEgressNeedsRelay(netCtx.Mode) {
		return nil
	}
	sock := windowsEgressSocketPath(guestHome)
	if !egressSocketPathFits(sock) {
		return fmt.Errorf(
			"the egress socket path is %d bytes, over the %d-byte AF_UNIX limit: %s\n"+
				"Set NVX_HOME to a shorter directory, or set network.mode to \"open\" to run without the egress allowlist",
			len(sock), unixSocketPathMax-1, sock)
	}
	if err := egress.ListenUnix(sock); err != nil {
		return err
	}
	netCtx.EgressSocketPath = sock
	return nil
}
