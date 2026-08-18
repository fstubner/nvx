package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
)

type hostPort struct {
	host string
	port uint16
}

type EgressProxy struct {
	httpAddr  string
	socksAddr string
	httpLn    net.Listener
	socksLn   net.Listener
	// unixLn serves the same HTTP CONNECT handler on a UNIX socket, so a process
	// in a different network namespace can reach this proxy (see ListenUnix).
	unixLn   net.Listener
	unixPath string
	ctx      context.Context
	// allow is built once before any connection is served and never mutated, so it
	// needs no lock. session and prompted are written while connections are in
	// flight; promptMu guards both.
	allow    map[string]bool
	policy   Policy
	nvxHome  string
	promptMu sync.Mutex
	session  map[string]bool
	prompted map[string]bool
	cancel   context.CancelFunc
}

func startEgressProxy(ctx context.Context, policy Policy, provider RuntimeProvider, nvxHome string) (*EgressProxy, error) {
	mode := strings.ToLower(policy.Isolation.Network.Mode)
	if mode == "open" {
		return nil, nil
	}

	allow := map[string]bool{}
	for _, entry := range policy.NetworkAllowlist(provider) {
		allow[normalizeAllowEntry(entry)] = true
	}

	p := &EgressProxy{
		allow:    allow,
		session:  map[string]bool{},
		policy:   policy,
		nvxHome:  nvxHome,
		prompted: map[string]bool{},
	}

	proxyCtx, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	p.ctx = proxyCtx

	httpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		cancel()
		return nil, fmt.Errorf("egress HTTP listen: %w", err)
	}
	p.httpAddr = httpLn.Addr().String()
	p.httpLn = httpLn

	socksLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = httpLn.Close()
		cancel()
		return nil, fmt.Errorf("egress SOCKS listen: %w", err)
	}
	p.socksAddr = socksLn.Addr().String()
	p.socksLn = socksLn

	go p.serveHTTP(proxyCtx, httpLn)
	go p.serveSOCKS(proxyCtx, socksLn)
	return p, nil
}

// ListenUnix additionally serves the HTTP CONNECT proxy on a UNIX socket at path.
//
// A network namespace does not contain UNIX sockets -- they are filesystem
// objects -- so this is how a process inside the sandbox's loopback-only netns
// reaches a proxy that stays outside it and therefore still has real egress.
// The TCP listeners remain for the platforms that do not use a netns.
func (p *EgressProxy) ListenUnix(path string) error {
	if p == nil {
		return nil
	}
	// A stale socket from a crashed run would make Listen fail with EADDRINUSE.
	_ = os.Remove(path)

	ln, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("egress proxy unix listen %s: %w", path, err)
	}
	// Only the sandbox's own user needs to reach it.
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return fmt.Errorf("egress proxy socket permissions: %w", err)
	}
	p.unixLn = ln
	p.unixPath = path

	ctx := p.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	go p.serveHTTP(ctx, ln)
	return nil
}

// UnixSocketPath returns the UNIX socket path, or "" if ListenUnix was not used.
func (p *EgressProxy) UnixSocketPath() string {
	if p == nil {
		return ""
	}
	return p.unixPath
}

func (p *EgressProxy) Close() {
	if p == nil || p.cancel == nil {
		return
	}
	p.cancel()
	if p.httpLn != nil {
		_ = p.httpLn.Close()
	}
	if p.socksLn != nil {
		_ = p.socksLn.Close()
	}
	if p.unixLn != nil {
		_ = p.unixLn.Close()
	}
	// The socket file outlives its listener; leaving it behind would make the
	// next run's Listen fail with EADDRINUSE.
	if p.unixPath != "" {
		_ = os.Remove(p.unixPath)
	}
}

func (p *EgressProxy) HTTProxyURL() string {
	if p == nil {
		return ""
	}
	return "http://" + p.httpAddr
}

func (p *EgressProxy) SOCKSProxyURL() string {
	if p == nil {
		return ""
	}
	return "socks5://" + p.socksAddr
}

func (p *EgressProxy) HTTPListenHostPort() (string, uint16) {
	return splitHostPort(p.httpAddr)
}

func (p *EgressProxy) SOCKSListenHostPort() (string, uint16) {
	return splitHostPort(p.socksAddr)
}

func splitHostPort(addr string) (string, uint16) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "127.0.0.1", 0
	}
	port, _ := strconv.ParseUint(portStr, 10, 16)
	return host, uint16(port)
}

func normalizeAllowEntry(entry string) string {
	entry = strings.TrimSpace(strings.ToLower(entry))
	if entry == "" {
		return ""
	}
	if !strings.Contains(entry, ":") {
		entry += ":*"
	}
	return entry
}

func parseHostPortSpec(host string, port uint16) hostPort {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "localhost" {
		host = "127.0.0.1"
	}
	return hostPort{host: host, port: port}
}

// sessionAllows reports whether this run has already approved any of keys.
//
// It exists so the lookup happens under promptMu. allowed() runs on every
// per-connection goroutine, so reading the map unlocked raced the prompt path's
// write and could abort the process with a concurrent map read/write.
func (p *EgressProxy) sessionAllows(keys []string) bool {
	p.promptMu.Lock()
	defer p.promptMu.Unlock()
	for _, k := range keys {
		if p.session[k] {
			return true
		}
	}
	return false
}

// allowKeysFor returns every allowlist entry a destination may legitimately
// match: the exact host:port and the host:* wildcard.
//
// Loopback expands to all three spellings, because parseHostPortSpec has already
// rewritten "localhost" to 127.0.0.1 by this point. Without that, a policy saying
// allow_hosts: ["localhost:3000"] would silently fail to match a request the user
// wrote as localhost, which is the shape most people reach for.
func allowKeysFor(hp hostPort) []string {
	hosts := []string{hp.host}
	if isLoopback(hp.host) {
		hosts = []string{"127.0.0.1", "localhost", "::1"}
	}
	keys := make([]string, 0, len(hosts)*2)
	for _, h := range hosts {
		keys = append(keys, fmt.Sprintf("%s:%d", h, hp.port), fmt.Sprintf("%s:*", h))
	}
	return keys
}

func (p *EgressProxy) allowed(hp hostPort) bool {
	mode := strings.ToLower(strings.TrimSpace(p.policy.Isolation.Network.Mode))

	// A loopback destination used to be permitted unconditionally, whatever the
	// policy said (F38). That was survivable only while the contained process had
	// no route to this proxy: on Windows an AppContainer with no network
	// capability reached nothing at all, and on Linux a loopback-only netns has
	// its own 127.0.0.1, not the host's.
	//
	// The egress relay removed that premise on both. The proxy now runs in the
	// parent, OUTSIDE the containment, and dials on the contained process's
	// behalf -- so "permit all loopback" started meaning "permit every service on
	// the developer's machine": databases, dev servers, other agents' MCP
	// servers. Measured: a contained process read a host loopback service with an
	// empty allowlist.
	//
	// So loopback is now allowlisted like any other destination, which is what
	// README.md already described ("host services on localhost remain reachable
	// via allow_hosts"). network.mode "loopback" is the exception, because
	// permitting exactly these is the entire definition of that mode.
	if isLoopback(hp.host) && mode == "loopback" {
		return true
	}

	keys := allowKeysFor(hp)
	for _, k := range keys {
		if p.allow[k] {
			return true
		}
	}
	if p.sessionAllows(keys) {
		return true
	}

	key := fmt.Sprintf("%s:%d", hp.host, hp.port)
	if mode == "offline" || mode == "loopback" {
		LogWarn("Blocked egress (network.mode=%s): %s", mode, key)
		auditLog(p.nvxHome, "egress_block_mode", map[string]string{"host": key, "mode": mode})
		return false
	}

	if !p.policy.Isolation.Network.PromptUnknown {
		LogWarn("Blocked egress: %s", key)
		auditLog(p.nvxHome, "egress_deny", map[string]string{"host": key})
		return false
	}

	p.promptMu.Lock()
	defer p.promptMu.Unlock()
	if p.prompted[key] {
		return p.session[key]
	}
	p.prompted[key] = true

	msg := fmt.Sprintf("Allow outbound connection to %s?", key)
	if !PromptYesNo(msg) {
		LogWarn("Blocked egress: %s", key)
		auditLog(p.nvxHome, "egress_deny", map[string]string{"host": key})
		return false
	}
	p.session[key] = true
	persistNetworkAllowHost(p.nvxHome, key)
	auditLog(p.nvxHome, "egress_allow_prompted", map[string]string{"host": key})
	return true
}

func isLoopback(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (p *EgressProxy) serveHTTP(ctx context.Context, ln net.Listener) {
	defer ln.Close()
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
			}
			continue
		}
		go p.handleHTTPConn(conn)
	}
}

func (p *EgressProxy) handleHTTPConn(client net.Conn) {
	defer client.Close()
	br := bufio.NewReader(client)
	req, err := br.ReadString('\n')
	if err != nil {
		return
	}
	parts := strings.Fields(req)
	if len(parts) < 3 {
		return
	}
	method := strings.ToUpper(parts[0])

	if method == "CONNECT" {
		target := parts[1]
		host, portStr, err := net.SplitHostPort(target)
		if err != nil {
			return
		}
		port, _ := strconv.ParseUint(portStr, 10, 16)
		hp := parseHostPortSpec(host, uint16(port))
		if !p.allowed(hp) {
			_, _ = fmt.Fprintf(client, "HTTP/1.1 403 Forbidden\r\n\r\n")
			return
		}
		// Drain the remaining CONNECT request headers up to the blank line.
		// Otherwise the buffered bytes (Host:, etc.) would be forwarded to the
		// remote ahead of the client's TLS ClientHello, corrupting the handshake
		// (ERR_SSL_PACKET_LENGTH_TOO_LONG).
		for {
			line, lerr := br.ReadString('\n')
			if lerr != nil {
				return
			}
			if line == "\r\n" || line == "\n" {
				break
			}
		}
		remote, err := net.Dial("tcp", target)
		if err != nil {
			_, _ = fmt.Fprintf(client, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
			return
		}
		defer remote.Close()
		_, _ = fmt.Fprintf(client, "HTTP/1.1 200 Connection Established\r\n\r\n")
		go func() {
			_, _ = io.Copy(remote, br)
		}()
		_, _ = io.Copy(client, remote)
		return
	}

	_, _ = fmt.Fprintf(client, "HTTP/1.1 405 Method Not Allowed\r\n\r\n")
}

func (p *EgressProxy) serveSOCKS(ctx context.Context, ln net.Listener) {
	defer ln.Close()
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
			}
			continue
		}
		go p.handleSOCKSConn(conn)
	}
}

func (p *EgressProxy) handleSOCKSConn(conn net.Conn) {
	defer conn.Close()
	buf := make([]byte, 262)
	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return
	}
	nMethods := int(buf[1])
	if _, err := io.ReadFull(conn, buf[:nMethods]); err != nil {
		return
	}
	_, _ = conn.Write([]byte{0x05, 0x00})

	if _, err := io.ReadFull(conn, buf[:4]); err != nil {
		return
	}
	if buf[1] != 0x01 {
		return
	}

	var host string
	var port uint16
	switch buf[3] {
	case 0x01:
		if _, err := io.ReadFull(conn, buf[:4+2]); err != nil {
			return
		}
		host = net.IP(buf[:4]).String()
		port = binary.BigEndian.Uint16(buf[4:6])
	case 0x03:
		if _, err := io.ReadFull(conn, buf[:1]); err != nil {
			return
		}
		l := int(buf[0])
		if _, err := io.ReadFull(conn, buf[:l+2]); err != nil {
			return
		}
		host = string(buf[:l])
		port = binary.BigEndian.Uint16(buf[l : l+2])
	default:
		return
	}

	hp := parseHostPortSpec(host, port)
	if !p.allowed(hp) {
		_, _ = conn.Write([]byte{0x05, 0x02, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}

	target := net.JoinHostPort(host, strconv.Itoa(int(port)))
	remote, err := net.Dial("tcp", target)
	if err != nil {
		_, _ = conn.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer remote.Close()
	_, _ = conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	go func() {
		_, _ = io.Copy(remote, conn)
	}()
	_, _ = io.Copy(conn, remote)
}

func applyProxyEnv(cleanEnv []string, proxy *EgressProxy) []string {
	if proxy == nil {
		return cleanEnv
	}
	httpURL := proxy.HTTProxyURL()
	socksURL := proxy.SOCKSProxyURL()
	filtered := make([]string, 0, len(cleanEnv)+4)
	for _, e := range cleanEnv {
		key := strings.ToUpper(strings.SplitN(e, "=", 2)[0])
		switch key {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY":
			continue
		}
		filtered = append(filtered, e)
	}
	filtered = append(filtered,
		"HTTP_PROXY="+httpURL,
		"HTTPS_PROXY="+httpURL,
		"ALL_PROXY="+socksURL,
		"NO_PROXY=127.0.0.1,localhost,::1",
	)
	return filtered
}
