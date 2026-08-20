package main

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The HTTP half of the per-session credential is covered by
// TestEgressProxyRefusesClientsWithoutThisSessionsCredential. The SOCKS half was
// not, and an acceptance pass named that: it confirmed SOCKS authentication works
// by driving it by hand, which is exactly the kind of check that does not survive
// the next refactor.
//
// Leaving one protocol unauthenticated would not be a partial fix, it would be no
// fix -- ALL_PROXY points every SOCKS-speaking client at this listener, so a
// sibling sandbox that found the port could simply ask in the other language.
// These drive the real listener end to end rather than calling socksAuthenticate
// directly, because the bug being guarded against is a path reaching the tunnel
// without passing through it.

const (
	socksVersion  = 0x05
	methodNoAuth  = 0x00
	methodUserPwd = 0x02
	methodNone    = 0xFF
	cmdConnect    = 0x01
	atypIPv4      = 0x01
)

func socksProxyForTest(t *testing.T) (proxy *EgressProxy, addr, user, token, allowed string) {
	t.Helper()
	remote := nonLoopbackListener(t)
	t.Cleanup(func() { _ = remote.Close() })
	go func() {
		for {
			c, err := remote.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	allowed = remote.Addr().String()

	policy := DefaultPolicy()
	policy.Isolation.Network.PromptUnknown = false
	policy.Isolation.Network.AllowHosts = []string{allowed}

	p, err := startEgressProxy(context.Background(), policy, Providers["node"], tempDir(t))
	if err != nil {
		t.Fatalf("startEgressProxy: %v", err)
	}
	t.Cleanup(p.Close)

	// socks5://user:token@host:port
	rest := strings.TrimPrefix(p.SOCKSProxyURL(), "socks5://")
	at := strings.LastIndex(rest, "@")
	if at == -1 {
		t.Fatal("the SOCKS proxy URL carries no credential; every local process could use this listener")
	}
	userinfo, addr := rest[:at], rest[at+1:]
	user, token, _ = strings.Cut(userinfo, ":")
	return p, addr, user, token, allowed
}

// negotiate performs SOCKS5 method selection and, when the server asks for
// username/password, the RFC 1929 sub-negotiation. It returns the method the
// server chose and whether the credentials were accepted.
func negotiate(t *testing.T, conn net.Conn, offer []byte, user, pass string) (method byte, authOK bool) {
	t.Helper()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	req := append([]byte{socksVersion, byte(len(offer))}, offer...)
	if _, err := conn.Write(req); err != nil {
		t.Fatalf("write greeting: %v", err)
	}
	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil {
		t.Fatalf("read method selection: %v", err)
	}
	method = resp[1]
	if method != methodUserPwd {
		return method, false
	}
	sub := []byte{0x01, byte(len(user))}
	sub = append(sub, user...)
	sub = append(sub, byte(len(pass)))
	sub = append(sub, pass...)
	if _, err := conn.Write(sub); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	ar := make([]byte, 2)
	if _, err := io.ReadFull(conn, ar); err != nil {
		return method, false
	}
	return method, ar[1] == 0x00
}

func socksConnect(t *testing.T, conn net.Conn, target string) byte {
	t.Helper()
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		t.Fatalf("split %q: %v", target, err)
	}
	ip := net.ParseIP(host).To4()
	if ip == nil {
		t.Fatalf("test target %q is not IPv4", target)
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		t.Fatalf("port %q: %v", portStr, err)
	}
	req := []byte{socksVersion, cmdConnect, 0x00, atypIPv4}
	req = append(req, ip...)
	req = binary.BigEndian.AppendUint16(req, uint16(port))
	if _, err := conn.Write(req); err != nil {
		t.Fatalf("write connect: %v", err)
	}
	resp := make([]byte, 4)
	if _, err := io.ReadFull(conn, resp); err != nil {
		t.Fatalf("read connect reply: %v", err)
	}
	return resp[1] // 0x00 = granted
}

func TestSOCKSRefusesAClientWithoutThisSessionsCredential(t *testing.T) {
	_, addr, user, token, allowed := socksProxyForTest(t)

	t.Run("a no-auth-only client is turned away", func(t *testing.T) {
		conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		// This is what a sibling sandbox that found the port by scanning sends.
		method, _ := negotiate(t, conn, []byte{methodNoAuth}, "", "")
		if method != methodNone {
			t.Errorf("server selected method 0x%02X for a client offering no-auth only; want 0xFF (no acceptable methods)", method)
		}
	})

	t.Run("a wrong token is rejected", func(t *testing.T) {
		conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		if _, ok := negotiate(t, conn, []byte{methodNoAuth, methodUserPwd}, user, "0000000000000000"); ok {
			t.Error("a wrong token authenticated")
		}
	})

	t.Run("a wrong user is rejected", func(t *testing.T) {
		conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		if _, ok := negotiate(t, conn, []byte{methodNoAuth, methodUserPwd}, "root", token); ok {
			t.Error("a wrong username authenticated")
		}
	})

	// ...and the session's own client still works, so this is authentication and
	// not a listener that now refuses everyone. A deny-everything proxy passes a
	// deny-only test perfectly, which is the trap this suite keeps falling into.
	t.Run("this session's credential reaches an allowlisted host", func(t *testing.T) {
		conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		method, ok := negotiate(t, conn, []byte{methodNoAuth, methodUserPwd}, user, token)
		if method != methodUserPwd || !ok {
			t.Fatalf("this session's own credential was refused (method 0x%02X, ok=%v)", method, ok)
		}
		if rep := socksConnect(t, conn, allowed); rep != 0x00 {
			t.Errorf("allowlisted %s got SOCKS reply 0x%02X, want 0x00 granted", allowed, rep)
		}
	})

	// The allowlist still applies once authenticated: a credential is permission to
	// ask, not permission to reach anything.
	t.Run("the allowlist still applies after authenticating", func(t *testing.T) {
		host, _, _ := net.SplitHostPort(allowed)
		conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		if _, ok := negotiate(t, conn, []byte{methodNoAuth, methodUserPwd}, user, token); !ok {
			t.Fatal("this session's own credential was refused")
		}
		if rep := socksConnect(t, conn, net.JoinHostPort(host, "9")); rep == 0x00 {
			t.Error("a non-allowlisted host was granted; the allowlist is not consulted on the SOCKS path")
		}
	})
}
