//go:build linux

package main

import (
	"context"
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
	if os.Geteuid() != 0 {
		t.Skip("creating a network namespace needs root (run privileged)")
	}
	if _, err := exec.LookPath("ip"); err != nil {
		t.Skip("iproute2 not installed; bringUpLoopback needs `ip`")
	}

	host := nonLoopbackIPv4(t)

	// Stand-in "remote host" on a non-loopback address, so the allowlist decision
	// is genuinely exercised (loopback is always permitted -- F38).
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
	allowed := remote.Addr().String()
	blocked := net.JoinHostPort(host, "9")

	policy := DefaultPolicy()
	policy.Isolation.Network.PromptUnknown = false
	policy.Isolation.Network.AllowHosts = []string{allowed}

	// The proxy lives HERE, in the parent, outside the namespace the child gets.
	proxy, err := startEgressProxy(context.Background(), policy, Providers["node"], t.TempDir())
	if err != nil {
		t.Fatalf("startEgressProxy: %v", err)
	}
	defer proxy.Close()

	sock := filepath.Join(t.TempDir(), "egress.sock")
	if err := proxy.ListenUnix(sock); err != nil {
		t.Fatalf("ListenUnix: %v", err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestNetnsContainedProcessReachesOnlyAllowlistedHosts")
	cmd.Env = append(os.Environ(),
		"NVX_TEST_NETNS_CHILD=1",
		"NVX_TEST_SOCK="+sock,
		"NVX_TEST_ALLOWED="+allowed,
		"NVX_TEST_BLOCKED="+blocked,
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

	fmt.Printf("via_relay_allowed=%s\n", statusCodeVia(relayAddr, allowed))
	fmt.Printf("via_relay_blocked=%s\n", statusCodeVia(relayAddr, blocked))
}

// statusCodeVia issues a CONNECT through addr and returns just the status code.
func statusCodeVia(addr, target string) string {
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return "dial-failed"
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target); err != nil {
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
