package nvx

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A real Node, handed the environment nvx builds for a contained process, sends
// its fetch() through nvx's proxy. The allowlisted host is reached and the other
// is refused by the proxy.
//
// This is the delivery check the environment tests above cannot be. They show the
// variable is in the environment, and this shows a Node acts on it. It runs the
// process uncontained, so it needs no sandbox and runs on every platform CI has.
//
// The names end in .test and resolve to nothing. If Node ignored the proxy
// variables it would try to look them up itself and fail with ENOTFOUND, which is
// how the allowlisted half failed before nvx set NODE_USE_ENV_PROXY.

// nodeHonoursEnvProxy returns the node on PATH if it is one that reads
// NODE_USE_ENV_PROXY for fetch(). That is 24.0.0 and later, and 22.21.0 and
// later. See nodeUseEnvProxy.
func nodeHonoursEnvProxy(t *testing.T) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	out, err := exec.Command(node, "-p", "process.versions.node").Output()
	if err != nil {
		t.Skipf("could not run node: %v", err)
	}
	var major, minor int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d.%d", &major, &minor); err != nil {
		t.Skipf("could not read node's version from %q", out)
	}
	if major < 24 && !(major == 22 && minor >= 21) {
		t.Skipf("node %d.%d ignores NODE_USE_ENV_PROXY, so there is nothing to deliver to it", major, minor)
	}
	return node
}

func TestNodeFetchUsesTheProxyNvxPutsInItsEnvironment(t *testing.T) {
	node := nodeHonoursEnvProxy(t)

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("origin-ok"))
	}))
	t.Cleanup(origin.Close)
	_, port, _ := net.SplitHostPort(origin.Listener.Addr().String())

	// The proxy answers the names from a table, so no DNS is involved.
	resolveAs(t, map[string]string{"allowed.example.test": "127.0.0.1"})
	// Dial directly, so the test does not depend on the machine's own proxy.
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		t.Setenv(k, "")
	}
	p := proxyAllowing(t, "allowed.example.test:"+port)
	env := applyProxyEnv(os.Environ(), p)

	fetchBody := func(host string) string {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		script := fmt.Sprintf(`fetch('http://%s:%s/').then(r => r.text()).then(t => console.log('BODY=' + t))`+
			`.catch(e => console.log('ERR=' + ((e.cause && (e.cause.code || e.cause.message)) || e.message)))`, host, port)
		cmd := exec.CommandContext(ctx, node, "-e", script)
		cmd.Env = env
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("node -e for %s: %v", host, err)
		}
		return strings.TrimSpace(string(out))
	}

	if got := fetchBody("allowed.example.test"); got != "BODY=origin-ok" {
		t.Errorf("fetch to the allowlisted host printed %q, want BODY=origin-ok: the request did not go through the proxy", got)
	}
	if got := fetchBody("denied.example.test"); !strings.HasPrefix(got, "ERR=") || strings.Contains(got, "ENOTFOUND") {
		t.Errorf("fetch to a host off the allowlist printed %q, want a refusal from the proxy rather than a lookup by node", got)
	}

	// The proxy is what said yes and no, which is what the audit log shows.
	if got, want := allowEvents(t, p.nvxHome), []string{"allowed.example.test:" + port + " allow_hosts"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("egress_allow records = %q, want %q", got, want)
	}
	if !auditContains(t, p.nvxHome, "denied.example.test:"+port) {
		t.Error("the refusal of the other host was not recorded")
	}
}

// A port nvx opened inside the sandbox is dialled directly, and any other loopback
// port still goes to the proxy. Measured on Linux with Node 22.23.2, a --connect
// service answered a fetch with 200 before nvx set NODE_USE_ENV_PROXY=1 and
// refused it after, until the port was listed in NO_PROXY.
func TestNodeFetchToAPortNvxOpenedInsideTheSandboxBypassesTheProxy(t *testing.T) {
	node := nodeHonoursEnvProxy(t)

	newOrigin := func(body string) (port string) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		t.Cleanup(srv.Close)
		_, port, _ = net.SplitHostPort(srv.Listener.Addr().String())
		return port
	}
	inside := newOrigin("inside-ok")
	elsewhere := newOrigin("elsewhere-ok")

	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		t.Setenv(k, "")
	}
	p := proxyAllowing(t, "unrelated.example.test:443")

	insidePort, _ := strconv.Atoi(inside)
	fetchBody := func(env []string, port string) string {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		script := fmt.Sprintf(`fetch('http://127.0.0.1:%s/').then(r => r.text()).then(t => console.log('BODY=' + t))`+
			`.catch(e => console.log('ERR=' + ((e.cause && (e.cause.code || e.cause.message)) || e.message)))`, port)
		cmd := exec.CommandContext(ctx, node, "-e", script)
		cmd.Env = env
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("node -e for port %s: %v", port, err)
		}
		return strings.TrimSpace(string(out))
	}

	// As the supervisor builds it: the parent's environment, rewritten for the relay.
	parent := applyProxyEnv(os.Environ(), p)
	args := supervisorExecArgs{ConnectPorts: []connectMapping{{Host: 9222, Inside: insidePort}}}
	listed := applyRelayProxyEnv(parent, p.httpAddr, inSandboxNoProxy(args))
	unlisted := applyRelayProxyEnv(parent, p.httpAddr, "")

	if got := fetchBody(listed, inside); got != "BODY=inside-ok" {
		t.Errorf("fetch to the in-sandbox port printed %q, want BODY=inside-ok", got)
	}
	if got := fetchBody(listed, elsewhere); !strings.HasPrefix(got, "ERR=") {
		t.Errorf("fetch to another loopback port printed %q, want it refused by the proxy", got)
	}
	// The control. Without the entry the same request goes to the proxy, which is
	// what made --connect stop working for Node.
	if got := fetchBody(unlisted, inside); !strings.HasPrefix(got, "ERR=") {
		t.Errorf("with no NO_PROXY entry the fetch printed %q, want it refused by the proxy", got)
	}
}
