package nvx

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// connectOnce sends one CONNECT request to a fresh HTTP listener for p and
// returns the status line of the answer.
func connectOnce(t *testing.T, p *EgressProxy, target, auth string) string {
	t.Helper()
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
	req := "CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\n"
	if auth != "" {
		req += "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(proxyAuthUser+":"+auth)) + "\r\n"
	}
	if _, err := conn.Write([]byte(req + "\r\n")); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	buf := make([]byte, 256)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("no answer to CONNECT %q: %v", target, err)
	}
	line, _, _ := strings.Cut(string(buf[:n]), "\r\n")
	return line
}

// failingListener fails every Accept, as a listener out of file descriptors does.
type failingListener struct {
	net.Listener
	calls atomic.Int64
}

func (l *failingListener) Accept() (net.Conn, error) {
	l.calls.Add(1)
	return nil, errors.New("too many open files")
}
func (l *failingListener) Close() error { return nil }

// An accept loop waits between failures. It retried at once, so a lasting error
// spun a core for as long as the error lasted.
func TestAcceptLoopsBackOffOnAPersistentError(t *testing.T) {
	p := newTestProxy(t, "proxy", nil)
	for name, serve := range map[string]func(context.Context, net.Listener){
		"http": p.serveHTTP, "socks": p.serveSOCKS,
	} {
		ln := &failingListener{}
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		serve(ctx, ln)
		cancel()
		if n := ln.calls.Load(); n > 20 {
			t.Errorf("%s: %d Accept calls in 200ms against a listener that always fails", name, n)
		}
	}
}

// A CONNECT port that is not a port is refused. The parse error was dropped,
// so "443x" went on as port 0 and "99999" as 65535, and the raw target, port
// part included, was later printed to the terminal.
func TestAConnectWithAMalformedPortIsRefused(t *testing.T) {
	p := newTestProxy(t, "proxy", []string{"github.com:443"})
	p.token = "secret"
	for _, target := range []string{"github.com:443x", "github.com:99999", "github.com:0", "github.com:443\x1bc"} {
		if got := connectOnce(t, p, target, "secret"); !strings.Contains(got, " 400 ") {
			t.Errorf("CONNECT %q answered %q, want 400 Bad Request", target, got)
		}
	}
}

// The HTTP path authenticates before it judges the destination, as the SOCKS
// path does. It refused an invalid host first, so anything that could reach the
// listener could put a warning on the user's terminal and a line in the audit
// log without the session's credential.
func TestAConnectIsAuthenticatedBeforeItsHostIsJudged(t *testing.T) {
	p := newTestProxy(t, "proxy", []string{"github.com:443"})
	p.token = "secret"
	if got := connectOnce(t, p, "evil\x1bc.example:443", ""); !strings.Contains(got, " 407 ") {
		t.Fatalf("unauthenticated CONNECT to an invalid host answered %q, want 407", got)
	}
	if auditContains(t, p.nvxHome, "egress_deny_invalid_host") {
		t.Fatal("an unauthenticated request wrote an audit record")
	}
}
