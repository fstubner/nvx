//go:build linux

package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// nonLoopbackIPv4 returns an address of this host that is not 127.0.0.0/8, so a
// test target is subject to the real allowlist decision. EgressProxy.allowed
// short-circuits every loopback destination to "permitted" (F38), so a loopback
// target could not distinguish allowlisted from blocked.
func nonLoopbackIPv4(t *testing.T) string {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Skipf("cannot enumerate interfaces: %v", err)
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		if v4 := ipnet.IP.To4(); v4 != nil {
			return v4.String()
		}
	}
	t.Skip("no non-loopback IPv4 address available")
	return ""
}

// connectVia issues an HTTP CONNECT for target through addr and returns the
// status line.
func connectVia(t *testing.T, addr, target string) string {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target); err != nil {
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
	host := nonLoopbackIPv4(t)

	// A stand-in "remote host" on a non-loopback address.
	remote, err := net.Listen("tcp", host+":0")
	if err != nil {
		t.Skipf("cannot bind %s: %v", host, err)
	}
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
	_, allowedPort, _ := net.SplitHostPort(remote.Addr().String())
	allowedTarget := net.JoinHostPort(host, allowedPort)

	policy := DefaultPolicy()
	policy.Isolation.Network.PromptUnknown = false // deny unknown without prompting
	policy.Isolation.Network.AllowHosts = []string{allowedTarget}

	proxy, err := startEgressProxy(context.Background(), policy, Providers["node"], t.TempDir())
	if err != nil {
		t.Fatalf("startEgressProxy: %v", err)
	}
	defer proxy.Close()

	sock := filepath.Join(t.TempDir(), "egress.sock")
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

	// Allowlisted destination must tunnel.
	if got := connectVia(t, relayAddr, allowedTarget); !strings.Contains(got, "200") {
		t.Errorf("allowlisted %s through relay: got %q, want 200 Connection Established", allowedTarget, got)
	}

	// A different port on the same host is NOT allowlisted and must be refused,
	// proving the allowlist is consulted rather than everything being permitted.
	blockedTarget := net.JoinHostPort(host, "9")
	if got := connectVia(t, relayAddr, blockedTarget); !strings.Contains(got, "403") {
		t.Errorf("non-allowlisted %s through relay: got %q, want 403 Forbidden", blockedTarget, got)
	}
}
