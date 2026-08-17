//go:build linux

package main

import (
	"context"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"
)

// TestProxyRelayForwardsBothDirections covers the mechanism F31's fix rests on:
// a loopback-only network namespace has no route out, so the egress proxy must
// stay outside it. A UNIX socket crosses the namespace boundary but npm/node
// cannot use one via HTTP_PROXY, so the contained side needs a TCP endpoint that
// forwards to it.
func TestProxyRelayForwardsBothDirections(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "egress.sock")

	// Stand in for the parent-side egress proxy: echo with a marker prefix so we
	// can prove data moved in both directions, not just that a dial succeeded.
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("unix listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 256)
				n, err := c.Read(buf)
				if err != nil {
					return
				}
				_, _ = c.Write(append([]byte("via-unix:"), buf[:n]...))
			}(c)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr, stop, err := startProxyRelay(ctx, sock)
	if err != nil {
		t.Fatalf("startProxyRelay: %v", err)
	}
	defer stop()

	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial relay %s: %v", addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))

	if _, err := conn.Write([]byte("CONNECT example.com:443")); err != nil {
		t.Fatalf("write to relay: %v", err)
	}
	buf := make([]byte, 256)
	n, err := conn.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("read from relay: %v", err)
	}
	if got, want := string(buf[:n]), "via-unix:CONNECT example.com:443"; got != want {
		t.Fatalf("relay round-trip = %q, want %q", got, want)
	}
}

// TestProxyRelayListensOnLoopbackOnly keeps the relay from becoming an
// accidentally externally-reachable proxy: it must bind 127.0.0.1, never 0.0.0.0.
func TestProxyRelayListensOnLoopbackOnly(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "egress.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("unix listen: %v", err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr, stop, err := startProxyRelay(ctx, sock)
	if err != nil {
		t.Fatalf("startProxyRelay: %v", err)
	}
	defer stop()

	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", addr, err)
	}
	if host != "127.0.0.1" {
		t.Fatalf("relay bound to %q, want 127.0.0.1", host)
	}
}
