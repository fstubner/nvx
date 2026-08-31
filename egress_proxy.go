package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
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
	// token authenticates this session's clients to this session's proxy.
	//
	// Every nvx sandbox on a machine shares one AppContainer package identity, and
	// Windows scopes its loopback restriction to the package -- so two projects
	// running at once are in the same loopback namespace and either one can connect
	// to the other's relay port. Without a credential that meant project B could
	// borrow project A's allowlist by port-scanning loopback, which an independent
	// acceptance pass demonstrated on 2026-08-19: B's own proxy refused a host, A's
	// established the tunnel. The same applies to the host-side TCP listeners, which
	// any local process can reach.
	//
	// The token travels as ordinary proxy credentials in HTTP_PROXY, so npm, node
	// and curl send it without knowing anything about nvx. A sibling that scans its
	// way to the port does not have it and gets 407.
	token string
	ctx   context.Context
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

	token, err := newProxyToken()
	if err != nil {
		// Fail closed: an unauthenticated proxy is reachable by every sibling
		// sandbox and every local process, which is the hole this exists to close.
		return nil, fmt.Errorf("egress proxy credential: %w", err)
	}

	p := &EgressProxy{
		token:    token,
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

// newProxyToken returns a fresh per-session credential. 128 bits: this is guessed
// online against a listener a sibling can reach, not stored.
func newProxyToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// proxyCredential is the userinfo prefix for a proxy URL ("nvx:<token>@").
func (p *EgressProxy) proxyCredential() string {
	if p == nil || p.token == "" {
		return ""
	}
	return proxyAuthUser + ":" + p.token + "@"
}

// ProxyCredential exposes the userinfo for callers that build their own proxy URL
// -- the in-container relay, whose address differs from this proxy's.
func (p *EgressProxy) ProxyCredential() string {
	return p.proxyCredential()
}

const proxyAuthUser = "nvx"

func (p *EgressProxy) HTTProxyURL() string {
	if p == nil {
		return ""
	}
	return "http://" + p.proxyCredential() + p.httpAddr
}

func (p *EgressProxy) SOCKSProxyURL() string {
	if p == nil {
		return ""
	}
	return "socks5://" + p.proxyCredential() + p.socksAddr
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

	// A loopback destination is never grantable by prompt -- only by policy file.
	//
	// Reaching localhost when the policy says so is a supported case and stays
	// one: a dev server, a local registry, `allow_hosts: ["localhost:5432"]` in
	// README. What is withdrawn is the path where the CONTAINED PROCESS causes the
	// question to be asked. The prompt is triggered by whatever the sandbox is
	// running, which is the untrusted code, at a moment the developer is not
	// expecting a security question -- so a postinstall could ask, on its own
	// behalf, for access to the developer's local database.
	//
	// Loopback is where that matters most: the services listening there are the
	// ones that assume anything local is trusted and take no credentials --
	// Postgres, Redis, dev servers, other agents' MCP servers. An allowlist entry
	// someone typed into a policy file is a decision that can be read and diffed;
	// an answer to a prompt raised by untrusted code is not.
	if isLoopback(hp.host) {
		LogWarn("Blocked egress to a local service: %s", key)
		LogInfo("nvx does not offer local services through a prompt, because the contained process is what triggers it. "+
			"If this is meant, add %q to isolation.network.allow_hosts in the project policy, or use --connect for one run.", key)
		auditLog(p.nvxHome, "egress_deny_loopback_prompt", map[string]string{"host": key})
		return false
	}

	p.promptMu.Lock()
	defer p.promptMu.Unlock()
	if p.prompted[key] {
		return p.session[key]
	}
	p.prompted[key] = true

	msg := fmt.Sprintf("Allow outbound connection to %s for the rest of this run?", key)
	if !PromptTrustBoundary(msg) {
		LogWarn("Blocked egress: %s", key)
		auditLog(p.nvxHome, "egress_deny", map[string]string{"host": key})
		return false
	}
	// This run only. It used to also write the host into the project policy, so a
	// single yes was a permanent grant -- the prompt said "allow outbound
	// connection to X?", mentioned no persistence, and the field it set is called
	// `session`. An acceptance pass approved one host, then reached it in a later
	// run with no prompt and no trust environment at all.
	//
	// Persisting is still available and is now only the deliberate form: write the
	// host into isolation.network.allow_hosts, where it can be reviewed in a diff
	// like every other policy decision.
	p.session[key] = true
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

		// Read the remaining CONNECT request headers up to the blank line, and
		// keep the credential out of them. They must be consumed either way:
		// otherwise the buffered bytes (Host:, etc.) would be forwarded to the
		// remote ahead of the client's TLS ClientHello, corrupting the handshake
		// (ERR_SSL_PACKET_LENGTH_TOO_LONG).
		var auth string
		for {
			line, lerr := br.ReadString('\n')
			if lerr != nil {
				return
			}
			if line == "\r\n" || line == "\n" {
				break
			}
			if name, value, ok := strings.Cut(line, ":"); ok &&
				strings.EqualFold(strings.TrimSpace(name), "Proxy-Authorization") {
				auth = strings.TrimSpace(value)
			}
		}

		// Authenticate before consulting the allowlist, so a sibling sandbox
		// scanning loopback cannot use the 403/200 difference to learn what this
		// session is permitted to reach.
		if !p.authorized(auth) {
			_, _ = fmt.Fprintf(client, "HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Basic realm=\"nvx\"\r\n\r\n")
			return
		}
		if !p.allowed(hp) {
			_, _ = fmt.Fprintf(client, "HTTP/1.1 403 Forbidden\r\n\r\n")
			return
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

// socksAuthenticate completes SOCKS5 method negotiation, requiring username /
// password (RFC 1929) whenever this session has a token. Returns false if the
// client cannot or will not authenticate, having already told it so.
func (p *EgressProxy) socksAuthenticate(conn net.Conn, offered []byte) bool {
	const (
		methodNoAuth   = 0x00
		methodUserPass = 0x02
		methodNone     = 0xFF
	)
	if p.token == "" {
		_, _ = conn.Write([]byte{0x05, methodNoAuth})
		return true
	}
	if !bytesContain(offered, methodUserPass) {
		_, _ = conn.Write([]byte{0x05, methodNone})
		return false
	}
	if _, err := conn.Write([]byte{0x05, methodUserPass}); err != nil {
		return false
	}

	// Sub-negotiation: version, ulen, uname, plen, passwd.
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(conn, hdr); err != nil || hdr[0] != 0x01 {
		return false
	}
	user := make([]byte, int(hdr[1]))
	if _, err := io.ReadFull(conn, user); err != nil {
		return false
	}
	plen := make([]byte, 1)
	if _, err := io.ReadFull(conn, plen); err != nil {
		return false
	}
	pass := make([]byte, int(plen[0]))
	if _, err := io.ReadFull(conn, pass); err != nil {
		return false
	}

	ok := string(user) == proxyAuthUser &&
		subtle.ConstantTimeCompare(pass, []byte(p.token)) == 1
	if !ok {
		_, _ = conn.Write([]byte{0x01, 0x01}) // failure
		return false
	}
	_, _ = conn.Write([]byte{0x01, 0x00}) // success
	return true
}

func bytesContain(b []byte, want byte) bool {
	for _, x := range b {
		if x == want {
			return true
		}
	}
	return false
}

// authorized checks a Proxy-Authorization header value against this session's
// token. Compared in constant time: the check runs against a listener any local
// process can talk to as often as it likes.
func (p *EgressProxy) authorized(header string) bool {
	if p.token == "" {
		return true
	}
	scheme, encoded, ok := strings.Cut(strings.TrimSpace(header), " ")
	if !ok || !strings.EqualFold(scheme, "Basic") {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return false
	}
	user, pass, ok := strings.Cut(string(raw), ":")
	if !ok || user != proxyAuthUser {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(pass), []byte(p.token)) == 1
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
	// Same credential as the HTTP path, over RFC 1929. Leaving SOCKS open while
	// HTTP is authenticated would just move the sibling-borrows-the-allowlist hole
	// one port along.
	if !p.socksAuthenticate(conn, buf[:nMethods]) {
		return
	}

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
