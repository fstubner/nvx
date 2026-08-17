//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"
	"testing"
)

// socketProbe is one socket(2) call the filter must either permit or refuse.
type socketProbe struct {
	name   string
	domain int
	typ    int
}

// proxyModeProbes encodes what proxy mode is documented to enforce
// (buildProxyNetworkFilter: "denies IPv4/IPv6 UDP socket creation; TCP is
// allowed for loopback proxy use"). AF_UNIX is not a network socket and must
// never be refused -- denying it breaks ordinary local IPC.
var proxyModeProbes = []struct {
	socketProbe
	wantAllowed bool
}{
	{socketProbe{"inet_tcp", syscall.AF_INET, syscall.SOCK_STREAM}, true},
	{socketProbe{"inet_udp", syscall.AF_INET, syscall.SOCK_DGRAM}, false},
	// socket(2)'s type argument carries SOCK_CLOEXEC/SOCK_NONBLOCK flags OR'd in,
	// and Go's own net package sets them. A filter comparing the raw type against
	// SOCK_DGRAM misses every flagged UDP socket, so the mask is load-bearing.
	{socketProbe{"inet_udp_cloexec", syscall.AF_INET, syscall.SOCK_DGRAM | syscall.SOCK_CLOEXEC}, false},
	{socketProbe{"inet6_tcp", syscall.AF_INET6, syscall.SOCK_STREAM}, true},
	{socketProbe{"inet6_udp", syscall.AF_INET6, syscall.SOCK_DGRAM}, false},
	{socketProbe{"unix_stream", syscall.AF_UNIX, syscall.SOCK_STREAM}, true},
}

// TestProxyNetworkFilterEnforcesUDPDenyAndTCPAllow pins F23. The proxy-mode cBPF
// filter was inverted: instruction [3]'s false branch fell through to [5] with
// args[0] (the domain) still in the accumulator instead of args[1] (the type),
// so it compared a domain against SOCK_DGRAM. Result: IPv4 TCP denied (breaking
// the loopback proxy it exists to serve), IPv4 UDP allowed (the one thing it
// claims to block), and AF_UNIX denied.
//
// Verified against the real kernel by installing the filter in a subprocess --
// seccomp is irreversible for the calling process.
func TestProxyNetworkFilterEnforcesUDPDenyAndTCPAllow(t *testing.T) {
	assertFilterBehavior(t, "proxy")
}

// TestOfflineNetworkFilterDeniesAllInetSockets is the control: offline mode was
// reported correct, so it must stay correct. AF_UNIX remains permitted.
func TestOfflineNetworkFilterDeniesAllInetSockets(t *testing.T) {
	assertFilterBehavior(t, "offline")
}

func assertFilterBehavior(t *testing.T, mode string) {
	t.Helper()

	if os.Getenv("NVX_TEST_SECCOMP_CHILD") == mode {
		if err := applyLinuxNetworkSeccomp(mode); err != nil {
			fmt.Printf("INSTALL_FAILED=%v\n", err)
			os.Exit(0)
		}
		for _, p := range proxyModeProbes {
			fd, err := syscall.Socket(p.domain, p.typ, 0)
			if err == nil {
				_ = syscall.Close(fd)
				fmt.Printf("%s=allowed\n", p.name)
			} else {
				fmt.Printf("%s=denied\n", p.name)
			}
		}
		os.Exit(0)
	}

	cmd := exec.Command(os.Args[0], "-test.run="+t.Name())
	cmd.Env = append(os.Environ(), "NVX_TEST_SECCOMP_CHILD="+mode)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("probe subprocess failed: %v\noutput:\n%s", err, out)
	}
	got := parseProbeResults(string(out))
	if msg, bad := got["INSTALL_FAILED"]; bad {
		t.Skipf("seccomp unavailable in this environment: %s", msg)
	}
	if len(got) == 0 {
		t.Fatalf("probe produced no results; output:\n%s", out)
	}

	for _, p := range proxyModeProbes {
		want := "denied"
		// Offline mode denies every AF_INET/AF_INET6 socket regardless of type;
		// AF_UNIX stays allowed in both modes.
		if (mode == "proxy" && p.wantAllowed) || (mode == "offline" && p.domain == syscall.AF_UNIX) {
			want = "allowed"
		}
		if got[p.name] != want {
			t.Errorf("%s mode: socket(%s) = %s, want %s", mode, p.name, got[p.name], want)
		}
	}
	if t.Failed() {
		keys := make([]string, 0, len(got))
		for k := range got {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		for _, k := range keys {
			fmt.Fprintf(&b, "  %s=%s\n", k, got[k])
		}
		t.Logf("full probe result for %s mode:\n%s", mode, b.String())
	}
}

func parseProbeResults(out string) map[string]string {
	res := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if k, v, ok := strings.Cut(line, "="); ok && k != "" {
			res[k] = v
		}
	}
	return res
}
