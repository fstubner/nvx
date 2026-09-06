package main

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// A CONNECT request that never ends is dropped, not buffered without limit.
//
// The HTTP side of the proxy read the request line and each header with
// ReadString('\n') on an unbounded buffered reader. The client is the
// sandboxed process, and the proxy is nvx itself, outside the sandbox: a
// contained process that sends bytes without a newline, for as long as it
// likes, grows the parent's memory for as long as it likes. Containment is
// supposed to bound what the inside can do to the outside, and "exhaust the
// supervisor's memory" is on the wrong side of that line.
//
// The header phase is now capped; a request that exceeds the cap is closed.
// Anything after the headers is tunnelled without a cap, as it must be.
func TestAnEndlessConnectRequestIsDroppedNotBuffered(t *testing.T) {
	p := newTestProxy(t, "proxy", []string{"github.com:443"})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.serveHTTP(ctx, ln)

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Well past any sane header size, and never a newline.
	chunk := []byte(strings.Repeat("A", 64<<10))
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	for i := 0; i < 8; i++ { // 512 KiB
		if _, err := conn.Write(chunk); err != nil {
			break // the proxy closed on us, which is the point
		}
	}

	// The proxy must hang up. If it is still reading, this read waits for the
	// deadline instead.
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 16)
	_, err = conn.Read(buf)
	if err == nil {
		t.Fatal("the proxy answered an endless request; it should have closed the connection")
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("the proxy is still reading an endless CONNECT request after 512 KiB; a contained process " +
			"can grow nvx's memory without limit")
	}
}

// The cap is generous enough for real requests: a CONNECT with ordinary
// headers to an allowlisted host gets past the header phase. The host does not
// resolve here, so the answer is a 502, and that is fine: it proves the request
// was parsed, authenticated and judged, which is everything the cap sits in
// front of.
func TestAnOrdinaryConnectRequestStillGetsThroughTheHeaderPhase(t *testing.T) {
	p := newTestProxy(t, "proxy", []string{"github.com:443"})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.serveHTTP(ctx, ln)

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	req := "CONNECT github.com:443 HTTP/1.1\r\nHost: github.com:443\r\nUser-Agent: test\r\n" +
		"X-Padding: " + strings.Repeat("p", 4000) + "\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	buf := make([]byte, 256)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("no response to an ordinary CONNECT: %v", err)
	}
	if !strings.HasPrefix(string(buf[:n]), "HTTP/1.1 ") {
		t.Fatalf("unexpected response to an ordinary CONNECT: %q", buf[:n])
	}
}
