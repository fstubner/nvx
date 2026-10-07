package nvx

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A server and a client in one sandbox talk over 127.0.0.1. Node's fetch and http
// follow the proxy variables since nvx sets NODE_USE_ENV_PROXY=1, so unless
// NO_PROXY says otherwise the request goes to nvx's proxy, which dials the same
// number on this machine and refuses it. Measured on Windows and on Linux with Node
// 22.23.2, a server and client in one sandbox answered 200 before the variable was
// set, and after it fetch was rejected and http.get received 405.
//
// The proxy refuses a loopback address unless the policy has it admit loopback (an
// allow_hosts entry for it, or network.mode loopback). When it does not, nothing is
// lost by connecting directly, and the OS decides what a direct connection reaches.
// On Windows and Linux that is the sandbox's own loopback. The tests below run a
// real Node against the environment nvx builds for the supervisor's child.

// ownLoopback reports whether a contained process's 127.0.0.1 is its own and not
// this machine's. macOS shares this machine's, so nvx always lists loopback in
// NO_PROXY there.
func ownLoopback() bool { return runtime.GOOS == "windows" || runtime.GOOS == "linux" }

func newLoopbackOrigin(t *testing.T, body string) (port string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	_, port, _ = net.SplitHostPort(srv.Listener.Addr().String())
	return port
}

// nodeRequests runs a real node with env, asking http://host:port/ with fetch and
// with http.get, and returns "fetch=<body or ERR> get=<body or ERR>".
func nodeRequests(t *testing.T, node string, env []string, host, port string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	script := fmt.Sprintf(`
const url = 'http://%s:%s/';
const show = (label, v) => process.stdout.write(label + '=' + v + ' ');
fetch(url).then(r => r.text()).then(b => show('fetch', b), e => show('fetch', 'ERR')).then(() =>
  new Promise(done => require('http').get(url, r => { let b = ''; r.on('data', d => b += d); r.on('end', () => { show('get', r.statusCode === 200 ? b : 'ERR'); done(); }); })
    .on('error', () => { show('get', 'ERR'); done(); })));`, host, port)
	cmd := exec.CommandContext(ctx, node, "-e", script)
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node -e for %s:%s: %v", host, port, err)
	}
	return strings.TrimSpace(string(out))
}

// supervisorEnv is the environment the supervisor hands the target. It is built as
// a launch builds it. The parent decides about loopback from the policy, and the
// supervisor adds the ports nvx opened and points the variables at the relay.
func supervisorEnv(p *EgressProxy, args supervisorExecArgs) []string {
	parent := applyProxyEnv(os.Environ(), p, loopbackViaProxy(p))
	return applyRelayProxyEnv(parent, p.httpAddr, inSandboxNoProxy(args))
}

func noHostProxy(t *testing.T) {
	t.Helper()
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(k, "")
	}
}

func TestNodeReachesAServerInTheSameSandboxWhenThePolicyAdmitsNoLoopback(t *testing.T) {
	node := nodeOnPathHonoursEnvProxy(t)
	noHostProxy(t)
	port := newLoopbackOrigin(t, "inside-ok")
	p := proxyAllowing(t, "unrelated.example.test:443")

	env := supervisorEnv(p, supervisorExecArgs{})
	for _, host := range []string{"127.0.0.1", "localhost"} {
		if got := nodeRequests(t, node, env, host, port); got != "fetch=inside-ok get=inside-ok" {
			t.Errorf("a client reached a server in the same sandbox, by %s, as %q, want fetch=inside-ok get=inside-ok: its request went to the proxy", host, got)
		}
	}
}

// What the policy has the proxy admit keeps going to the proxy, so a service on
// this machine stays reachable through allow_hosts, a local registry included. The
// proxy is the only thing that writes egress_allow, so the record is how this knows
// the request went through it and was not dialled directly.
func TestAnAllowedLoopbackEntryStillGoesThroughTheProxy(t *testing.T) {
	if !ownLoopback() {
		t.Skip("macOS shares this machine's loopback, so nvx always lists it in NO_PROXY there")
	}
	node := nodeOnPathHonoursEnvProxy(t)
	noHostProxy(t)
	hostService := newLoopbackOrigin(t, "host-service-ok")
	other := newLoopbackOrigin(t, "other-ok")
	p := proxyAllowing(t, "127.0.0.1:"+hostService)

	env := supervisorEnv(p, supervisorExecArgs{})
	// fetch tunnels with CONNECT, which the proxy serves. http.get sends a plain
	// request in proxy form, which it answers with 405.
	if got := nodeRequests(t, node, env, "127.0.0.1", hostService); !strings.HasPrefix(got, "fetch=host-service-ok ") {
		t.Errorf("a request to the allowlisted loopback port gave %q, want fetch=host-service-ok", got)
	}
	if got, want := allowEvents(t, p.nvxHome), []string{"127.0.0.1:" + hostService + " allow_hosts"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("egress_allow records = %q, want %q: the request did not go through the proxy", got, want)
	}
	// Any other loopback port is still the proxy's to refuse.
	if got := nodeRequests(t, node, env, "127.0.0.1", other); got != "fetch=ERR get=ERR" {
		t.Errorf("a request to a loopback port the policy does not name gave %q, want it refused by the proxy", got)
	}
}

// With loopback sent through the proxy, the ports nvx opened inside the sandbox are
// still dialled directly. Measured on Linux with Node 22.23.2, a --connect service
// answered 200 before nvx set NODE_USE_ENV_PROXY=1 and was refused after it, until
// the port was listed in NO_PROXY.
func TestNodeReachesAPortNvxOpenedInsideTheSandboxWhenLoopbackGoesThroughTheProxy(t *testing.T) {
	if !ownLoopback() {
		t.Skip("macOS shares this machine's loopback, so nvx always lists it in NO_PROXY there")
	}
	node := nodeOnPathHonoursEnvProxy(t)
	noHostProxy(t)
	inside := newLoopbackOrigin(t, "inside-ok")
	hostService := newLoopbackOrigin(t, "host-service-ok")
	p := proxyAllowing(t, "localhost:"+hostService)

	insidePort, _ := strconv.Atoi(inside)
	args := supervisorExecArgs{ConnectPorts: []connectMapping{{Host: 9222, Inside: insidePort}}}
	if got := nodeRequests(t, node, supervisorEnv(p, args), "127.0.0.1", inside); got != "fetch=inside-ok get=inside-ok" {
		t.Errorf("a request to the in-sandbox port gave %q, want fetch=inside-ok get=inside-ok", got)
	}
	// The control. Without the listing the same request goes to the proxy and is refused.
	if got := nodeRequests(t, node, supervisorEnv(p, supervisorExecArgs{}), "127.0.0.1", inside); got != "fetch=ERR get=ERR" {
		t.Errorf("with no NO_PROXY entry the request gave %q, want it refused by the proxy", got)
	}
}

// noProxyEntries returns every NO_PROXY entry in env as NAME=value, matching the
// name the way Windows does.
func noProxyEntries(env []string) []string {
	var out []string
	for _, e := range env {
		if name, value, _ := strings.Cut(e, "="); strings.EqualFold(name, "NO_PROXY") {
			out = append(out, name+"="+value)
		}
	}
	return out
}

func TestNoProxyListsLoopbackUnlessTheProxyAdmitsIt(t *testing.T) {
	p := testProxyForEnv()
	// Whatever the host had is replaced, in whichever case it was written.
	host := []string{"PATH=/bin", "NO_PROXY=intranet.example.test", "no_proxy=other.example.test"}

	got := noProxyEntries(applyProxyEnv(host, p, false))
	if strings.Join(got, "|") != "NO_PROXY=localhost,127.0.0.1,::1" {
		t.Errorf("NO_PROXY entries = %q, want the loopback names alone", got)
	}
	if got := noProxyEntries(applyProxyEnv(host, p, true)); len(got) != 0 {
		t.Errorf("NO_PROXY entries with loopback going to the proxy = %q, want none", got)
	}
	if got := noProxyEntries(applyProxyEnv(host, nil, false)); strings.Join(got, "|") != "NO_PROXY=intranet.example.test|no_proxy=other.example.test" {
		t.Errorf("with no proxy the environment changed: %q", got)
	}
}

func TestTheProxyAdmitsLoopbackOnlyWhenThePolicyDoes(t *testing.T) {
	cases := []struct {
		name         string
		mode         string
		allowHosts   []string
		defaultAllow []string
		want         bool
	}{
		{"the shipped defaults", "", nil, nil, false},
		{"a registry in allow_hosts", "proxy", []string{"registry.example.test:443"}, nil, false},
		{"localhost with a port", "proxy", []string{"localhost:4873"}, nil, true},
		{"127.0.0.1 with a port", "proxy", []string{"127.0.0.1:4873"}, nil, true},
		{"127.0.0.1 with no port", "proxy", []string{"127.0.0.1"}, nil, true},
		{"localhost, any port", "proxy", []string{"localhost:*"}, nil, true},
		{"::1 with a port", "proxy", []string{"::1:4873"}, nil, true},
		{"capitals", "proxy", []string{"LocalHost:4873"}, nil, true},
		{"loopback among other hosts", "proxy", []string{"registry.example.test:443", "localhost:4873"}, nil, true},
		{"a default_allow entry", "proxy", nil, []string{"localhost:4873"}, true},
		// The proxy matches a loopback request against the three names above, so an
		// entry spelled any other way matches nothing and routes nothing.
		{"[::1] never matches", "proxy", []string{"[::1]:4873"}, nil, false},
		{"another 127 address never matches", "proxy", []string{"127.0.0.2:4873"}, nil, false},
		{"a name that starts with localhost", "proxy", []string{"localhost.example.test:443"}, nil, false},
		{"network.mode loopback", "loopback", nil, nil, true},
		{"network.mode loopback in capitals", " LOOPBACK ", nil, nil, true},
		{"offline admits nothing, an entry or not", "offline", []string{"localhost:4873"}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy := DefaultPolicy()
			policy.Isolation.Network.PromptUnknown = false
			policy.Isolation.Network.Mode = tc.mode
			policy.Isolation.Network.AllowHosts = tc.allowHosts
			if tc.defaultAllow != nil {
				policy.Isolation.Network.DefaultAllow = tc.defaultAllow
			}
			p := proxyWithPolicy(t, policy)
			if got := p.admitsLoopback(); got != tc.want {
				t.Fatalf("admitsLoopback = %v, want %v for allow_hosts %q, default_allow %q, mode %q", got, tc.want, tc.allowHosts, tc.defaultAllow, tc.mode)
			}
		})
	}
	var none *EgressProxy
	if none.admitsLoopback() {
		t.Error("a missing proxy admits loopback")
	}
}

// macOS shares this machine's loopback, so the policy cannot send it through the
// proxy there. A request to it connects directly, and the Seatbelt profile decides.
func TestLoopbackGoesThroughTheProxyOnlyWhereTheSandboxHasItsOwn(t *testing.T) {
	policy := DefaultPolicy()
	policy.Isolation.Network.AllowHosts = []string{"localhost:4873"}
	p := proxyWithPolicy(t, policy)
	if got := loopbackViaProxy(p); got != ownLoopback() {
		t.Errorf("loopbackViaProxy = %v on %s, want %v", got, runtime.GOOS, ownLoopback())
	}
	if loopbackViaProxy(nil) {
		t.Error("loopbackViaProxy is true with no proxy")
	}
}

// The supervisor adds to the parent's NO_PROXY, and does not replace it. The
// parent knows the policy, so its list is the decision about loopback.
func TestTheRelayKeepsTheParentsNoProxyAndAddsThePortsNvxOpened(t *testing.T) {
	const relay = "127.0.0.1:42000"
	proxyVars := []string{"HTTPS_PROXY=http://nvx:tok@127.0.0.1:41000"}
	withLoopback := append([]string{"NO_PROXY=" + loopbackNoProxy}, proxyVars...)
	ports := "127.0.0.1:19222,localhost:19222"

	cases := []struct {
		name      string
		inherited []string
		addr      string
		inSandbox string
		want      string
	}{
		{"loopback listed, ports opened", withLoopback, relay, ports, loopbackNoProxy + "," + ports},
		{"loopback listed, no ports", withLoopback, relay, "", loopbackNoProxy},
		{"loopback to the proxy, ports opened", proxyVars, relay, ports, ports},
		{"loopback to the proxy, no ports", proxyVars, relay, "", ""},
		{"no relay strips everything", withLoopback, "", ports, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := noProxyEntries(applyRelayProxyEnv(tc.inherited, tc.addr, tc.inSandbox))
			want := ""
			if tc.want != "" {
				want = "NO_PROXY=" + tc.want + "|no_proxy=" + tc.want
			}
			if strings.Join(got, "|") != want {
				t.Fatalf("NO_PROXY entries = %q, want %q", got, want)
			}
		})
	}
}
