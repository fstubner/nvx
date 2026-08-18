package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
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
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() || ipnet.IP.IsLinkLocalUnicast() {
			continue
		}
		v4 := ipnet.IP.To4()
		if v4 == nil {
			continue
		}
		ln, lerr := net.Listen("tcp", net.JoinHostPort(v4.String(), "0"))
		if lerr != nil {
			continue
		}
		return ln
	}
	t.Skip("no bindable non-loopback IPv4 address available")
	return nil
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

	proxy, err := startEgressProxy(context.Background(), policy, Providers["node"], t.TempDir())
	if err != nil {
		t.Fatalf("startEgressProxy: %v", err)
	}
	defer proxy.Close()

	// Not t.TempDir(): it embeds the test name, and sockaddr_un.sun_path caps the
	// whole path at 108 bytes on Windows exactly as on Unix. This test's name alone
	// pushes it over, and the bind fails as "invalid argument" -- which reads like a
	// permissions problem and is not one.
	sockDir, err := os.MkdirTemp("", "nvxs")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sockDir)
	sock := filepath.Join(sockDir, "egress.sock")
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
