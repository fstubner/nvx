package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// nonLoopbackListener binds a listener on an address of this host that is not
// 127.0.0.0/8, so a test target is subject to the real allowlist decision.
// EgressProxy.allowed short-circuits every loopback destination to "permitted"
// (F38), so a loopback target could not distinguish allowlisted from blocked.
//
// It binds here rather than returning an address for the caller to bind, because
// having an address is not the same as being able to listen on it: the first
// non-loopback IPv4 on this Windows host is an unbindable 169.254.0.0/16
// link-local, so the previous "return the first one" version skipped the entire
// test rather than running it. Candidates are tried until one accepts a bind.
func nonLoopbackListener(t *testing.T) net.Listener {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Skipf("cannot enumerate interfaces: %v", err)
	}
	// Ordered, not first-come, and each candidate is proved reachable before it
	// is used. Taking the first bindable address picked this machine's Tailscale
	// address (100.106.71.95), which binds happily and then intermittently
	// refuses connections -- the egress relay test failed with the proxy
	// returning 502 and a direct dial "actively refused", which reads as the
	// sandbox blocking everything rather than as the target being unreachable.
	// That is the flagship egress test reporting a containment defect that did
	// not exist.
	var candidates []net.IP
	var lastResort []net.IP
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() || ipnet.IP.IsLinkLocalUnicast() {
			continue
		}
		v4 := ipnet.IP.To4()
		if v4 == nil {
			continue
		}
		// 100.64.0.0/10 is carrier-grade NAT, which is what Tailscale and
		// similar overlay networks hand out. Usable if nothing else exists, but
		// never the first choice for a test that needs a dependable local peer.
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
			lastResort = append(lastResort, v4)
			continue
		}
		candidates = append(candidates, v4)
	}

	// Why the last error is kept rather than dropped: every candidate failing used
	// to skip with "no reachable non-loopback IPv4 address available", which reads
	// as a host without a usable network. On a host that was out of memory it was
	// instead net.Listen failing on each address in turn, and this test plus the
	// credential one below silently did not run while the gate reported success.
	var lastErr error
	for _, ip := range append(candidates, lastResort...) {
		ln, lerr := net.Listen("tcp", net.JoinHostPort(ip.String(), "0"))
		if lerr != nil {
			lastErr = lerr
			continue
		}
		// Bindable is not the same as reachable. Proving it here turns "this
		// host cannot host the probe" into a skip instead of a failure blamed on
		// containment.
		c, derr := net.DialTimeout("tcp", ln.Addr().String(), 2*time.Second)
		if derr != nil {
			lastErr = derr
			_ = ln.Close()
			continue
		}
		_ = c.Close()
		return ln
	}
	failIfHostIsOutOfMemory(t, "binding a non-loopback listener", lastErr)
	if lastErr != nil {
		t.Skipf("no reachable non-loopback IPv4 address available; last attempt failed with: %v", lastErr)
	}
	t.Skipf("no reachable non-loopback IPv4 address available: none of this host's %d addresses were candidates", len(addrs))
	return nil
}

// connectVia issues an HTTP CONNECT for target through addr and returns the
// status line. cred is a "user:pass@" userinfo prefix as EgressProxy.
// ProxyCredential returns it; "" sends no Proxy-Authorization at all, which is
// what a sibling sandbox that found the port by scanning would do.
func connectVia(t *testing.T, addr, target, cred string) string {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	authHeader := ""
	if cred != "" {
		userpass := strings.TrimSuffix(cred, "@")
		authHeader = "Proxy-Authorization: Basic " +
			base64.StdEncoding.EncodeToString([]byte(userpass)) + "\r\n"
	}
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n%s\r\n", target, target, authHeader); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read status line: %v", err)
	}
	return strings.TrimSpace(line)
}

// TestEgressProxyOverUnixSocketEnforcesAllowlistBothWays exercises the whole F31
// chain -- contained-side loopback TCP -> relay -> UNIX socket -> egress proxy --
// and asserts the allowlist decision in *both* directions.
//
// Asserting the allow path matters independently: the existing egress smoke test
// only ever checked that blocked traffic fails (F27), which a sandbox that denies
// everything passes perfectly. That is precisely the state proxy mode was in.
func TestEgressProxyOverUnixSocketEnforcesAllowlistBothWays(t *testing.T) {
	// A stand-in "remote host" on a non-loopback address.
	remote := nonLoopbackListener(t)
	defer remote.Close()
	host, _, _ := net.SplitHostPort(remote.Addr().String())
	go func() {
		for {
			c, err := remote.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	_, allowedPort, _ := net.SplitHostPort(remote.Addr().String())
	allowedTarget := net.JoinHostPort(host, allowedPort)

	policy := DefaultPolicy()
	policy.Isolation.Network.PromptUnknown = false // deny unknown without prompting
	policy.Isolation.Network.AllowHosts = []string{allowedTarget}

	proxy, err := startEgressProxy(context.Background(), policy, Providers["node"], tempDir(t))
	if err != nil {
		t.Fatalf("startEgressProxy: %v", err)
	}
	defer proxy.Close()

	sock := unixSocketTempPath(t)
	if err := proxy.ListenUnix(sock); err != nil {
		t.Fatalf("ListenUnix: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	relayAddr, stop, err := startProxyRelay(ctx, sock)
	if err != nil {
		t.Fatalf("startProxyRelay: %v", err)
	}
	defer stop()

	cred := proxy.ProxyCredential()
	if cred == "" {
		t.Fatal("proxy issued no credential; every sibling sandbox could use this listener")
	}

	// Allowlisted destination must tunnel.
	if got := connectVia(t, relayAddr, allowedTarget, cred); !strings.Contains(got, "200") {
		t.Errorf("allowlisted %s through relay: got %q, want 200 Connection Established", allowedTarget, got)
	}

	// A different port on the same host is NOT allowlisted and must be refused,
	// proving the allowlist is consulted rather than everything being permitted.
	blockedTarget := net.JoinHostPort(host, "9")
	if got := connectVia(t, relayAddr, blockedTarget, cred); !strings.Contains(got, "403") {
		t.Errorf("non-allowlisted %s through relay: got %q, want 403 Forbidden", blockedTarget, got)
	}
}

// TestEgressProxyRefusesClientsWithoutThisSessionsCredential is the F79 proof.
//
// Every nvx sandbox on a machine shares one AppContainer package identity, and
// Windows scopes its loopback restriction to the package -- so two projects
// running at once sit in the same loopback namespace. An acceptance pass on
// 2026-08-19 port-scanned loopback from project B, found project A's relay, and
// tunnelled to a host only A's policy allowed: B's own proxy refused the host in
// the same run. The host-side TCP listeners have the same exposure to any local
// process.
//
// The allowlist is only meaningful if reaching the enforcement point requires
// this session's credential, so that is what this asserts -- including that an
// anonymous client is refused BEFORE the allowlist is consulted, or the 403/200
// difference would still leak what this session may reach.
func TestEgressProxyRefusesClientsWithoutThisSessionsCredential(t *testing.T) {
	remote := nonLoopbackListener(t)
	defer remote.Close()
	go func() {
		for {
			c, err := remote.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	allowedTarget := remote.Addr().String()
	host, _, _ := net.SplitHostPort(allowedTarget)

	policy := DefaultPolicy()
	policy.Isolation.Network.PromptUnknown = false
	policy.Isolation.Network.AllowHosts = []string{allowedTarget}

	proxy, err := startEgressProxy(context.Background(), policy, Providers["node"], tempDir(t))
	if err != nil {
		t.Fatalf("startEgressProxy: %v", err)
	}
	defer proxy.Close()

	httpAddr := strings.TrimPrefix(proxy.HTTProxyURL(), "http://")
	if at := strings.LastIndex(httpAddr, "@"); at != -1 {
		httpAddr = httpAddr[at+1:]
	}

	cases := []struct {
		name   string
		cred   string
		target string
		want   string
	}{
		// The sibling's view: it knows the port, not the token.
		{"no credential, allowlisted host", "", allowedTarget, "407"},
		// Refused before the allowlist, so 403-vs-407 cannot be used to probe
		// which hosts this session is permitted to reach.
		{"no credential, blocked host", "", net.JoinHostPort(host, "9"), "407"},
		{"wrong token", "nvx:0000000000000000000000000000000@", allowedTarget, "407"},
		{"wrong user", "root:" + strings.TrimSuffix(strings.TrimPrefix(proxy.ProxyCredential(), "nvx:"), "@") + "@", allowedTarget, "407"},
		// And the session's own client still works, so this is authentication
		// rather than a proxy that now refuses everyone.
		{"this session's credential", proxy.ProxyCredential(), allowedTarget, "200"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := connectVia(t, httpAddr, tc.target, tc.cred); !strings.Contains(got, tc.want) {
				t.Errorf("CONNECT %s: got %q, want %s", tc.target, got, tc.want)
			}
		})
	}
}
