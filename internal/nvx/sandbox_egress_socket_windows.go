//go:build windows

package nvx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Where one session's AF_UNIX sockets go.
//
// Every path is a prefix plus a short name. The prefix is the guest home, which
// the AppContainer is already granted. When NVX_HOME is too long for that, it is
// the AppContainer's own folder, %LOCALAPPDATA%\Packages\<package>\AC, which
// Windows grants the package itself when it creates the profile. Neither one
// widens what the sandbox can reach. Measured 2026-10-01: a 132-character
// NVX_HOME needed a 174-byte egress socket path, and every proxied run refused.
//
// The package is one per project, so its folder is shared by that project's
// concurrent sessions, and the prefix there carries a session tag.

// windowsEgressSocketName is the socket the parent's egress proxy listens on in
// addition to its TCP listeners.
const windowsEgressSocketName = "egress.sock"

func windowsEgressSocketPath(prefix string) string {
	return prefix + windowsEgressSocketName
}

// guestHomeSocketPrefix puts the sockets directly in the guest home.
func guestHomeSocketPrefix(guestHome string) string {
	return guestHome + string(filepath.Separator)
}

// windowsSessionSockets lists the socket paths this session binds under prefix.
func windowsSessionSockets(prefix string, netCtx NetworkLaunchContext) []string {
	var socks []string
	if netCtx.egress != nil {
		socks = append(socks, windowsEgressSocketPath(prefix))
	}
	for _, m := range netCtx.ExposePorts {
		socks = append(socks, windowsExposeSocketPath(prefix, m.Container))
	}
	for _, m := range netCtx.ConnectPorts {
		socks = append(socks, windowsConnectSocketPath(prefix, m.Host))
	}
	return socks
}

// windowsSocketTooLong returns the first of this session's socket paths under
// prefix that will not bind, or "" when they all fit.
func windowsSocketTooLong(prefix string, netCtx NetworkLaunchContext) string {
	for _, sock := range windowsSessionSockets(prefix, netCtx) {
		if !egressSocketPathFits(sock) {
			return sock
		}
	}
	return ""
}

// windowsSocketPrefix chooses the guest home when this session's sockets fit
// there, and otherwise containerDir when they fit there. With neither, it
// returns the guest home, so the refusal names NVX_HOME, the setting that fixes
// it.
func windowsSocketPrefix(guestHome, containerDir string, netCtx NetworkLaunchContext) string {
	home := guestHomeSocketPrefix(guestHome)
	if containerDir == "" || windowsSocketTooLong(home, netCtx) == "" {
		return home
	}
	// Eight characters of the session id. Concurrent sessions of one project
	// differ there, and the full id would cost eight more bytes of the limit.
	tag := filepath.Base(guestHome)
	if len(tag) > 8 {
		tag = tag[:8]
	}
	shared := filepath.Join(containerDir, tag+"-")
	if windowsSocketTooLong(shared, netCtx) != "" {
		return home
	}
	return shared
}

// appContainerFolder is the AC folder Windows created for the package, or ""
// when it is not there.
func appContainerFolder(pkgName string) string {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" || pkgName == "" {
		return ""
	}
	dir := filepath.Join(local, "Packages", pkgName, "AC")
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return ""
	}
	return dir
}

// windowsSocketRoomError returns nil when this session's sockets fit under
// prefix, and otherwise an error naming the longest NVX_HOME that would work.
func windowsSocketRoomError(prefix, nvxHome, guestHome string, netCtx NetworkLaunchContext) error {
	sock := windowsSocketTooLong(prefix, netCtx)
	if sock == "" {
		return nil
	}
	inHome := windowsSocketTooLong(guestHomeSocketPrefix(guestHome), netCtx)
	if inHome == "" {
		inHome = sock
	}
	maxHome := unixSocketPathMax - 1 - (len(inHome) - len(nvxHome))
	return fmt.Errorf("a socket path is %d bytes, over the %d-byte AF_UNIX limit: %s\n"+
		"Set NVX_HOME to a directory of at most %d characters (it is %d)",
		len(sock), unixSocketPathMax-1, sock, maxHome, len(nvxHome))
}

// windowsEgressNeedsRelay reports whether this network mode routes the contained
// process through the parent's proxy.
//
//   - offline grants no capabilities and gets no relay: with no internetClient
//     the AppContainer cannot reach the network at all, which is the enforcement
//     that mode asks for.
//   - open grants internetClient and connects directly, by request.
//   - everything else -- proxy, the default, and loopback -- uses the relay.
//
// loopback sat with offline until 2026-09-08, which made it a synonym for it on
// this platform: the mode's meaning is "reach the services on 127.0.0.1", and a
// sandbox with no capability and no relay reached nothing. It gets the relay for
// the same reason proxy does, and reaches no further: the AppContainer still
// holds no network capability, the parent proxy decides every destination, and
// what makes this mode different from proxy is one rule there
// (EgressProxy.allowed) permitting loopback destinations without an allow_hosts
// entry.
func windowsEgressNeedsRelay(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "offline", "open":
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
//
// On Windows this only records the proxy. bindWindowsEgressSocket puts it on the
// socket once platformLaunchNative knows where the socket goes.
func prepareEgressSocket(egress *EgressProxy, guestHome string, netCtx *NetworkLaunchContext) error {
	if egress == nil || netCtx == nil || guestHome == "" {
		return nil
	}
	if !windowsEgressNeedsRelay(netCtx.Mode) {
		return nil
	}
	netCtx.egress = egress
	return nil
}

// bindWindowsEgressSocket listens on the egress socket under prefix, when
// prepareEgressSocket recorded a proxy for this session.
func bindWindowsEgressSocket(netCtx *NetworkLaunchContext, prefix string) error {
	if netCtx.egress == nil {
		return nil
	}
	sock := windowsEgressSocketPath(prefix)
	if err := netCtx.egress.ListenUnix(sock); err != nil {
		return err
	}
	netCtx.EgressSocketPath = sock
	return nil
}
