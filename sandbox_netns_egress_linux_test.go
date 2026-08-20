//go:build linux

package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestNetnsContainedProcessReachesOnlyAllowlistedHosts is the end-to-end proof for
// F31: proxy mode places the contained process in a loopback-only network
// namespace, which has no route to any allowlisted host, so the egress proxy has
// to stay outside that namespace and be reached over a UNIX socket.
//
// It asserts all three properties together, which is what makes it meaningful:
//   - direct egress from inside the namespace FAILS (the namespace is real)
//   - an allowlisted host succeeds THROUGH the relay (the fix works)
//   - a non-allowlisted host is refused through the relay (the allowlist is live)
//
// Before this change proxy mode could satisfy only the first, because the proxy
// ran inside the namespace and had nothing to forward through.
func TestNetnsContainedProcessReachesOnlyAllowlistedHosts(t *testing.T) {
	if os.Getenv("NVX_TEST_NETNS_CHILD") == "1" {
		runNetnsEgressChild()
		return
	}
	requireNamespaceSupport(t, syscall.CLONE_NEWNET)
	if _, err := exec.LookPath("ip"); err != nil {
		t.Skip("iproute2 not installed; bringUpLoopback needs `ip`")
	}

	// Stand-in "remote host" on a non-loopback address, so the allowlist decision
	// is genuinely exercised (loopback is always permitted -- F38).
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
	allowed := remote.Addr().String()
	blocked := net.JoinHostPort(host, "9")

	policy := DefaultPolicy()
	policy.Isolation.Network.PromptUnknown = false
	policy.Isolation.Network.AllowHosts = []string{allowed}

	// The proxy lives HERE, in the parent, outside the namespace the child gets.
	proxy, err := startEgressProxy(context.Background(), policy, Providers["node"], tempDir(t))
	if err != nil {
		t.Fatalf("startEgressProxy: %v", err)
	}
	defer proxy.Close()

	sock := filepath.Join(tempDir(t), "egress.sock")
	if err := proxy.ListenUnix(sock); err != nil {
		t.Fatalf("ListenUnix: %v", err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestNetnsContainedProcessReachesOnlyAllowlistedHosts")
	cmd.Env = append(os.Environ(),
		"NVX_TEST_NETNS_CHILD=1",
		"NVX_TEST_SOCK="+sock,
		"NVX_TEST_ALLOWED="+allowed,
		"NVX_TEST_BLOCKED="+blocked,
		// The parent proxy authenticates its clients, so the child needs this
		// session's credential. In production it arrives the same way -- inside
		// HTTP_PROXY, written by applyRelayProxyEnv.
		"NVX_TEST_CRED="+proxy.ProxyCredential(),
	)
	// Exactly how platformLaunchNative creates the namespace: a clone flag, so the
	// whole child process is inside it from birth rather than one thread.
	cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: syscall.CLONE_NEWNET}

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("contained child failed: %v\noutput:\n%s", err, out)
	}
	got := parseProbeResults(string(out))

	for _, want := range []struct{ key, val, why string }{
		{"direct_egress", "blocked", "the network namespace must block egress that bypasses the proxy"},
		{"via_relay_allowed", "200", "an allowlisted host must tunnel through the relay to the parent proxy"},
		{"via_relay_blocked", "403", "a non-allowlisted host must still be refused"},
		// Sandboxes share a machine here too, so a client that reached the relay
		// without this session's credential must not inherit its allowlist.
		{"via_relay_anonymous", "407", "a client without this session's credential must be refused before the allowlist"},
	} {
		if got[want.key] != want.val {
			t.Errorf("%s = %q, want %q -- %s\nfull output:\n%s", want.key, got[want.key], want.val, want.why, out)
		}
	}
}

// runNetnsEgressChild executes inside the fresh network namespace.
func runNetnsEgressChild() {
	sock := os.Getenv("NVX_TEST_SOCK")
	allowed := os.Getenv("NVX_TEST_ALLOWED")
	blocked := os.Getenv("NVX_TEST_BLOCKED")

	if err := bringUpLoopback(); err != nil {
		fmt.Printf("setup_failed=%v\n", err)
		return
	}

	// 1. Direct egress must fail: the namespace has no route out.
	if c, err := net.DialTimeout("tcp", allowed, 2*time.Second); err == nil {
		_ = c.Close()
		fmt.Println("direct_egress=REACHED")
	} else {
		fmt.Println("direct_egress=blocked")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	relayAddr, stop, err := startProxyRelay(ctx, sock)
	if err != nil {
		fmt.Printf("relay_failed=%v\n", err)
		return
	}
	defer stop()

	cred := os.Getenv("NVX_TEST_CRED")
	fmt.Printf("via_relay_allowed=%s\n", statusCodeVia(relayAddr, allowed, cred))
	fmt.Printf("via_relay_blocked=%s\n", statusCodeVia(relayAddr, blocked, cred))
	fmt.Printf("via_relay_anonymous=%s\n", statusCodeVia(relayAddr, allowed, ""))
}

// statusCodeVia issues a CONNECT through addr and returns just the status code.
// cred is a "user:pass@" userinfo prefix; "" sends no credential at all.
func statusCodeVia(addr, target, cred string) string {
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return "dial-failed"
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	authHeader := ""
	if cred != "" {
		authHeader = "Proxy-Authorization: Basic " +
			base64.StdEncoding.EncodeToString([]byte(strings.TrimSuffix(cred, "@"))) + "\r\n"
	}
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n%s\r\n", target, target, authHeader); err != nil {
		return "write-failed"
	}
	buf := make([]byte, 128)
	n, _ := conn.Read(buf)
	for _, f := range strings.Fields(string(buf[:n])) {
		if len(f) == 3 && f[0] >= '1' && f[0] <= '5' {
			return f
		}
	}
	return "no-status"
}
