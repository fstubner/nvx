package nvx

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
)

// startProxyRelay listens on loopback inside the caller's network namespace and
// forwards every accepted connection to the UNIX socket at sockPath. It returns
// the local address to advertise as HTTP_PROXY, plus a stop func.
//
// Why this indirection exists: proxy mode places the contained process in a
// loopback-only network namespace, which by construction has no route to any
// allowlisted host. The egress proxy must therefore live *outside* that namespace,
// where it still has real network access -- previously it was started inside,
// so allowlisted hosts were unreachable and proxy mode could not work at all.
//
// A UNIX socket crosses a network-namespace boundary, because it is a filesystem
// object rather than a network endpoint. But npm, node and curl accept only
// host:port in HTTP_PROXY, so the contained side needs a loopback TCP endpoint
// that forwards to that socket. That is this relay.
func startProxyRelay(ctx context.Context, sockPath string) (addr string, stop func(), err error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, fmt.Errorf("egress relay listen: %w", err)
	}

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // listener closed
			}
			go relayConn(ctx, conn, sockPath)
		}
	}()

	return ln.Addr().String(), func() { _ = ln.Close() }, nil
}

// inSandboxNoProxy names the loopback ports nvx itself opened inside the sandbox,
// as NO_PROXY entries: the in-sandbox end of each --connect tunnel and each port
// published with --expose.
//
// They are endpoints in the sandbox, so a request to one that went to the proxy
// would be asking nvx to dial the same number on this machine, and could only be
// refused or reach something else. A client that follows the proxy variables
// needs telling. Node's fetch and http do since nvx sets NODE_USE_ENV_PROXY=1.
// Measured on Linux with Node 22.23.2, a --connect service answered 200 before that
// and 405 (http.get) or a rejection (fetch) after. Node reads host:port entries.
//
// Nothing else on loopback is listed, so every other loopback destination still
// goes to the proxy, which is how allow_hosts and network.mode loopback reach a
// service on this machine.
func inSandboxNoProxy(a supervisorExecArgs) string {
	var ports []int
	for _, m := range a.ConnectPorts {
		ports = append(ports, m.Inside)
	}
	ports = append(ports, a.ExposePorts...)
	var entries []string
	for _, port := range ports {
		if port <= 0 {
			continue
		}
		n := strconv.Itoa(port)
		entries = append(entries, "127.0.0.1:"+n, "localhost:"+n)
	}
	return strings.Join(entries, ",")
}

// applyRelayProxyEnv points the standard proxy variables at the relay address.
// An empty addr means no relay is in use (network.mode=open), in which case any
// inherited proxy settings are stripped rather than left to leak host config in.
// noProxy is what NO_PROXY becomes when there is a relay, and "" leaves it unset.
func applyRelayProxyEnv(env []string, addr, noProxy string) []string {
	// The parent's proxy URL carries this session's credential. Carry it across to
	// the relay address, or the target would talk to the relay anonymously and the
	// parent proxy would answer 407. It is read from the environment rather than
	// passed as an argument on purpose: command lines are readable machine-wide.
	cred := proxyCredentialFromEnv(env)

	out := make([]string, 0, len(env)+5)
	for _, e := range env {
		switch strings.ToUpper(strings.SplitN(e, "=", 2)[0]) {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", nodeUseEnvProxy:
			continue
		}
		out = append(out, e)
	}
	if addr == "" {
		return out
	}
	url := "http://" + cred + addr
	out = append(out,
		"HTTP_PROXY="+url,
		"HTTPS_PROXY="+url,
		"http_proxy="+url,
		"https_proxy="+url,
		// With the variables Node reads. See nodeUseEnvProxy.
		nodeUseEnvProxy+"=1",
	)
	if noProxy != "" {
		out = append(out, "NO_PROXY="+noProxy, "no_proxy="+noProxy)
	}
	return out
}

// proxyCredentialFromEnv pulls the "user:pass@" userinfo out of whichever proxy
// variable carries it, returning "" when there is none.
func proxyCredentialFromEnv(env []string) string {
	for _, e := range env {
		key, value, ok := strings.Cut(e, "=")
		if !ok {
			continue
		}
		switch strings.ToUpper(key) {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY":
		default:
			continue
		}
		// http://user:pass@host:port -> "user:pass@"
		_, rest, ok := strings.Cut(value, "://")
		if !ok {
			rest = value
		}
		if at := strings.LastIndex(rest, "@"); at != -1 {
			return rest[:at+1]
		}
	}
	return ""
}

// relayConn splices one contained-side connection to the parent's proxy socket.
func relayConn(ctx context.Context, client net.Conn, sockPath string) {
	defer client.Close()

	var d net.Dialer
	upstream, err := d.DialContext(ctx, "unix", sockPath)
	if err != nil {
		// The parent proxy is gone or the socket is not reachable; dropping the
		// connection surfaces to the caller as a failed request, which is the
		// fail-closed outcome we want rather than a silent direct connection.
		LogWarn("Egress relay could not reach the proxy socket: %v", err)
		return
	}
	defer upstream.Close()

	// Half-close each direction as it drains, so a response is never truncated by
	// the request side finishing first (plain "close both on first EOF" does that).
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(upstream, client)
		if u, ok := upstream.(*net.UnixConn); ok {
			_ = u.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, upstream)
		if c, ok := client.(*net.TCPConn); ok {
			_ = c.CloseWrite()
		}
		done <- struct{}{}
	}()
	<-done
	<-done
}
