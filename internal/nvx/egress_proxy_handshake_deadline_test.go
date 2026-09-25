package nvx

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// The proxy's client is the contained process, and the proxy runs in nvx
// itself. A client that connects and never finishes its request held a
// goroutine and a descriptor in the parent for as long as the run lasted.
// These hold both listeners to a bounded handshake, and hold the tunnel that
// follows to no bound at all. Measured 2026-09-25 on Windows 11 with the bound
// at 200ms: before it was applied, all four stalled clients below were still
// connected 3.2s later; after, each was closed at 200ms.

// shortProxyHandshake shortens the handshake bound for one test.
func shortProxyHandshake(t *testing.T) time.Duration {
	t.Helper()
	old := proxyHandshakeTimeout
	proxyHandshakeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { proxyHandshakeTimeout = old })
	return proxyHandshakeTimeout
}

// echoTarget is an allowlisted stand-in host that echoes what it is sent.
func echoTarget(t *testing.T) string {
	t.Helper()
	ln := proxyTargetListener(t)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	return ln.Addr().String()
}

func proxyAllowing(t *testing.T, target string) *EgressProxy {
	t.Helper()
	policy := DefaultPolicy()
	policy.Isolation.Network.PromptUnknown = false
	policy.Isolation.Network.AllowHosts = []string{target}
	p, err := startEgressProxy(context.Background(), policy, Providers["node"], tempDir(t))
	if err != nil {
		t.Fatalf("startEgressProxy: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// closedByServer waits up to within for the server to close conn, and fails if
// it is still open then.
func closedByServer(t *testing.T, conn net.Conn, within time.Duration) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(within))
	start := time.Now()
	_, err := io.Copy(io.Discard, conn)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("the proxy still held the connection open after %v with no complete request", within)
	}
	t.Logf("closed by the proxy after %v", time.Since(start).Round(10*time.Millisecond))
}

func TestEgressProxyClosesAClientThatNeverFinishesItsRequest(t *testing.T) {
	bound := shortProxyHandshake(t)
	p := proxyAllowing(t, echoTarget(t))
	// Generous next to the bound, so a slow machine does not fail it, and short
	// next to "for as long as the run lasts", which is what it replaces.
	within := bound + 3*time.Second

	cases := []struct {
		name, addr string
		send       []byte
	}{
		{"HTTP, nothing sent", p.httpAddr, nil},
		{"HTTP, request line without the end of the headers", p.httpAddr, []byte("CONNECT 127.0.0.1:1 HTTP/1.1\r\nHost: 127.0.0.1:1\r\n")},
		{"SOCKS, nothing sent", p.socksAddr, nil},
		{"SOCKS, greeting without the rest", p.socksAddr, []byte{socksVersion, 2}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			conn, err := net.DialTimeout("tcp", c.addr, 3*time.Second)
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer conn.Close()
			if len(c.send) > 0 {
				if _, err := conn.Write(c.send); err != nil {
					t.Fatalf("write: %v", err)
				}
			}
			closedByServer(t, conn, within)
		})
	}
}

// The bound is on how long the client may take to say where it wants to go,
// never on the tunnel after it. A tunnel left idle for several times the bound
// still carries traffic both ways.
func TestEgressProxyTunnelOutlivesTheHandshakeBound(t *testing.T) {
	bound := shortProxyHandshake(t)
	target := echoTarget(t)
	p := proxyAllowing(t, target)

	roundTrip := func(t *testing.T, conn net.Conn, r io.Reader) {
		t.Helper()
		time.Sleep(3 * bound)
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := conn.Write([]byte("ping\n")); err != nil {
			t.Fatalf("write through the tunnel after idling: %v", err)
		}
		got, err := bufio.NewReader(r).ReadString('\n')
		if err != nil || got != "ping\n" {
			t.Fatalf("tunnel idle for %v stopped carrying traffic: got %q, err %v", 3*bound, got, err)
		}
	}

	t.Run("HTTP CONNECT", func(t *testing.T) {
		conn, err := net.DialTimeout("tcp", p.httpAddr, 3*time.Second)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		cred := base64.StdEncoding.EncodeToString([]byte(proxyAuthUser + ":" + p.token))
		fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: Basic %s\r\n\r\n", target, target, cred)
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		br := bufio.NewReader(conn)
		status, err := br.ReadString('\n')
		if err != nil || !strings.Contains(status, " 200 ") {
			t.Fatalf("CONNECT to an allowlisted host: status %q, err %v", status, err)
		}
		if _, err := br.ReadString('\n'); err != nil { // blank line ending the response
			t.Fatal(err)
		}
		_ = conn.SetReadDeadline(time.Time{})
		roundTrip(t, conn, br)
	})

	t.Run("SOCKS", func(t *testing.T) {
		conn, err := net.DialTimeout("tcp", p.socksAddr, 3*time.Second)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		if _, ok := negotiate(t, conn, []byte{methodUserPwd}, proxyAuthUser, p.token); !ok {
			t.Fatal("this session's own credential was refused")
		}
		if rep := socksConnect(t, conn, target); rep != 0x00 {
			t.Fatalf("allowlisted %s got SOCKS reply 0x%02X", target, rep)
		}
		// Past the reply's bound address and port.
		if _, err := io.ReadFull(conn, make([]byte, 6)); err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Time{})
		roundTrip(t, conn, conn)
	})
}
