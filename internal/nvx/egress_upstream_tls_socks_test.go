package nvx

import (
	"bufio"
	"bytes"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// countingListener counts the connections a fake upstream proxy accepted, so a
// test can tell "never dialled" from "dialled and refused".
type countingListener struct {
	net.Listener
	n atomic.Int32
}

func (l *countingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		l.n.Add(1)
	}
	return c, err
}

// tlsUpstream is an https:// proxy stand-in: an httptest TLS server that
// answers CONNECT and then echoes the tunnel.
type tlsUpstream struct {
	srv      *httptest.Server
	ln       *countingListener
	mu       sync.Mutex
	requests []string // method, target and Proxy-Authorization of each CONNECT
}

func (f *tlsUpstream) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

func startTLSUpstream(t *testing.T) *tlsUpstream {
	t.Helper()
	f := &tlsUpstream{}
	f.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.Method+" "+r.Host+" "+r.Header.Get("Proxy-Authorization"))
		f.mu.Unlock()
		if r.Method != http.MethodConnect || r.TLS == nil {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		c, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n")
		_, _ = io.Copy(c, rw)
	}))
	f.ln = &countingListener{Listener: f.srv.Listener}
	f.srv.Listener = f.ln
	f.srv.StartTLS()
	t.Cleanup(f.srv.Close)
	return f
}

// pool trusts the test server's certificate, as a corporate CA would be
// trusted through the system roots.
func (f *tlsUpstream) pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(f.srv.Certificate())
	return p
}

// An allowed host is reached through an https:// proxy: TLS to the proxy, its
// certificate verified, then CONNECT with the URL's credentials. A host the
// allowlist refuses never opens a connection to the proxy. Before this, an
// https:// HTTPS_PROXY was ignored and the host was dialled directly, so on a
// machine whose only route out is the proxy the tunnel failed with 502.
func TestAllowedConnectGoesThroughAnHTTPSUpstreamProxy(t *testing.T) {
	up := startTLSUpstream(t)
	t.Setenv("HTTPS_PROXY", "https://alice:p%40ss@"+up.ln.Addr().String())
	t.Setenv("NO_PROXY", "")
	resolveAs(t, map[string]string{"elsewhere.example.test": "203.0.113.7"})

	p := proxyAllowing(t, "pkgs.example.test:443")
	if p.upstream == nil {
		t.Fatal("the https:// HTTPS_PROXY was ignored")
	}
	p.upstream.rootCAs = up.pool()

	status, echo := connectThrough(t, p, "pkgs.example.test:443")
	if !strings.Contains(status, " 200 ") || echo != "ping" {
		t.Fatalf("the allowed tunnel did not open through the https:// proxy: status %q, echo %q", status, echo)
	}
	want := "CONNECT pkgs.example.test:443 Basic " + "YWxpY2U6cEBzcw==" // alice:p@ss
	if got := up.seen(); len(got) != 1 || got[0] != want {
		t.Fatalf("the https:// proxy saw %q, want [%q]", got, want)
	}

	status, _ = connectThrough(t, p, "elsewhere.example.test:443")
	if !strings.Contains(status, " 403 ") {
		t.Fatalf("a host outside the allowlist got %q, want 403", status)
	}
	if n := up.ln.n.Load(); n != 1 {
		t.Fatalf("the https:// proxy accepted %d connections, want 1: a refused host was dialled upstream", n)
	}
}

// A proxy whose certificate does not verify gets no CONNECT, and so never sees
// the credential. Two ways to fail: a CA the machine does not trust, and a
// trusted certificate that does not name the host in the proxy URL.
func TestHTTPSUpstreamProxyWithABadCertificateIsRefused(t *testing.T) {
	up := startTLSUpstream(t)
	_, port, _ := net.SplitHostPort(up.ln.Addr().String())
	cases := []struct {
		name, url string
		trust     bool
	}{
		// System roots only. The test CA is not among them.
		{"untrusted CA", "https://alice:secret@127.0.0.1:" + port, false},
		// The httptest certificate names example.com, 127.0.0.1 and ::1.
		{"wrong name", "https://alice:secret@localhost:" + port, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := up.ln.n.Load()
			t.Setenv("HTTPS_PROXY", tc.url)
			t.Setenv("NO_PROXY", "")
			resolveAs(t, nil)

			p := proxyAllowing(t, "pkgs.example.test:443")
			if p.upstream == nil {
				t.Fatal("the https:// HTTPS_PROXY was ignored")
			}
			if tc.trust {
				p.upstream.rootCAs = up.pool()
			}
			status, _ := connectThrough(t, p, "pkgs.example.test:443")
			if !strings.Contains(status, " 502 ") {
				t.Fatalf("a proxy with a bad certificate gave %q, want 502", status)
			}
			if up.ln.n.Load() == before {
				t.Fatal("nvx never dialled the https:// proxy, so this proves nothing about its certificate check")
			}
			if got := up.seen(); len(got) != 0 {
				t.Fatalf("a proxy whose certificate failed verification received %q", got)
			}
		})
	}
}

// socksUpstream is a SOCKS5 proxy stand-in. It records the greeting, the
// RFC 1929 login and the CONNECT request byte for byte, then echoes the tunnel.
type socksUpstream struct {
	ln *countingListener
	// wantUser and wantPass, when set, make it require RFC 1929 login.
	wantUser, wantPass string
	mu                 sync.Mutex
	greetings, logins  [][]byte
	requests           [][]byte
}

func (f *socksUpstream) record(dst *[][]byte, b []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	*dst = append(*dst, append([]byte(nil), b...))
}

func (f *socksUpstream) seen() (greetings, logins, requests [][]byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.greetings, f.logins, f.requests
}

func startSOCKSUpstream(t *testing.T, user, pass string) *socksUpstream {
	t.Helper()
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	f := &socksUpstream{ln: &countingListener{Listener: raw}, wantUser: user, wantPass: pass}
	go func() {
		for {
			c, err := f.ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *socksUpstream) serve(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(br, hdr); err != nil {
		return
	}
	methods := make([]byte, hdr[1])
	if _, err := io.ReadFull(br, methods); err != nil {
		return
	}
	f.record(&f.greetings, append(hdr, methods...))
	if f.wantUser == "" {
		_, _ = c.Write([]byte{0x05, 0x00})
	} else {
		if !bytes.Contains(methods, []byte{0x02}) {
			_, _ = c.Write([]byte{0x05, 0xFF})
			return
		}
		_, _ = c.Write([]byte{0x05, 0x02})
		ver, _ := br.ReadByte()
		ulen, _ := br.ReadByte()
		user := make([]byte, ulen)
		_, _ = io.ReadFull(br, user)
		plen, _ := br.ReadByte()
		pass := make([]byte, plen)
		if _, err := io.ReadFull(br, pass); err != nil {
			return
		}
		login := append(append(append([]byte{ver, ulen}, user...), plen), pass...)
		f.record(&f.logins, login)
		if string(user) != f.wantUser || string(pass) != f.wantPass {
			_, _ = c.Write([]byte{0x01, 0x01})
			return
		}
		_, _ = c.Write([]byte{0x01, 0x00})
	}
	req := make([]byte, 4)
	if _, err := io.ReadFull(br, req); err != nil {
		return
	}
	var rest []byte
	switch req[3] {
	case 0x01:
		rest = make([]byte, 4+2)
	case 0x04:
		rest = make([]byte, 16+2)
	case 0x03:
		l, _ := br.ReadByte()
		req = append(req, l)
		rest = make([]byte, int(l)+2)
	}
	if _, err := io.ReadFull(br, rest); err != nil {
		return
	}
	f.record(&f.requests, append(req, rest...))
	// A domain-type bound address, so the client's reply parsing is exercised
	// past the fixed-size case.
	_, _ = c.Write([]byte{0x05, 0x00, 0x00, 0x03, 4, 'b', 'n', 'd', '1', 0x04, 0x38})
	_, _ = io.Copy(c, br)
}

// socks5h:// sends the destination's NAME, so a host nvx's own resolver cannot
// look up is still reachable, as with an http:// proxy. A refused host never
// reaches the proxy. Before this, a socks5h:// HTTPS_PROXY was ignored and the
// host was dialled directly.
func TestAllowedConnectGoesThroughASOCKS5hUpstreamProxy(t *testing.T) {
	up := startSOCKSUpstream(t, "", "")
	t.Setenv("HTTPS_PROXY", "socks5h://"+up.ln.Addr().String())
	t.Setenv("NO_PROXY", "")
	resolveAs(t, map[string]string{"elsewhere.example.test": "203.0.113.7"})

	p := proxyAllowing(t, "pkgs.example.test:443")
	status, echo := connectThrough(t, p, "pkgs.example.test:443")
	if !strings.Contains(status, " 200 ") || echo != "ping" {
		t.Fatalf("the allowed tunnel did not open through the socks5h:// proxy: status %q, echo %q", status, echo)
	}
	greetings, logins, requests := up.seen()
	if len(greetings) != 1 || !bytes.Equal(greetings[0], []byte{0x05, 0x01, 0x00}) {
		t.Fatalf("greeting %x, want one offering only no-auth (050100)", greetings)
	}
	if len(logins) != 0 {
		t.Fatalf("a login was sent with no user in the proxy URL: %x", logins)
	}
	want := append(append([]byte{0x05, 0x01, 0x00, 0x03, byte(len("pkgs.example.test"))}, "pkgs.example.test"...), 0x01, 0xBB)
	if len(requests) != 1 || !bytes.Equal(requests[0], want) {
		t.Fatalf("CONNECT request %x, want %x", requests, want)
	}

	status, _ = connectThrough(t, p, "elsewhere.example.test:443")
	if !strings.Contains(status, " 403 ") {
		t.Fatalf("a host outside the allowlist got %q, want 403", status)
	}
	if n := up.ln.n.Load(); n != 1 {
		t.Fatalf("the SOCKS5 proxy accepted %d connections, want 1: a refused host was dialled upstream", n)
	}
}

// socks5:// sends the address nvx resolved and vetted, and logs in with the
// URL's user and password over RFC 1929.
func TestAllowedConnectGoesThroughASOCKS5UpstreamProxyWithLogin(t *testing.T) {
	up := startSOCKSUpstream(t, "alice", "p@ss")
	t.Setenv("HTTPS_PROXY", "socks5://alice:p%40ss@"+up.ln.Addr().String())
	t.Setenv("NO_PROXY", "")
	resolveAs(t, map[string]string{"pkgs.example.test": "192.0.2.10", "elsewhere.example.test": "203.0.113.7"})

	p := proxyAllowing(t, "pkgs.example.test:443")
	status, echo := connectThrough(t, p, "pkgs.example.test:443")
	if !strings.Contains(status, " 200 ") || echo != "ping" {
		t.Fatalf("the allowed tunnel did not open through the socks5:// proxy: status %q, echo %q", status, echo)
	}
	greetings, logins, requests := up.seen()
	if len(greetings) != 1 || !bytes.Equal(greetings[0], []byte{0x05, 0x02, 0x00, 0x02}) {
		t.Fatalf("greeting %x, want one offering no-auth and user/pass (05020002)", greetings)
	}
	wantLogin := append(append(append([]byte{0x01, 5}, "alice"...), 4), "p@ss"...)
	if len(logins) != 1 || !bytes.Equal(logins[0], wantLogin) {
		t.Fatalf("login %x, want %x", logins, wantLogin)
	}
	wantReq := []byte{0x05, 0x01, 0x00, 0x01, 192, 0, 2, 10, 0x01, 0xBB}
	if len(requests) != 1 || !bytes.Equal(requests[0], wantReq) {
		t.Fatalf("CONNECT request %x, want the vetted address %x", requests, wantReq)
	}

	status, _ = connectThrough(t, p, "elsewhere.example.test:443")
	if !strings.Contains(status, " 403 ") {
		t.Fatalf("a host outside the allowlist got %q, want 403", status)
	}
	if n := up.ln.n.Load(); n != 1 {
		t.Fatalf("the SOCKS5 proxy accepted %d connections, want 1: a refused host was dialled upstream", n)
	}
}

// A refused login is not retried for the destination's next address. A proxy
// that locks accounts after failed logins would count each one.
func TestSOCKS5UpstreamLoginIsTriedOnce(t *testing.T) {
	up := startSOCKSUpstream(t, "alice", "right")
	t.Setenv("HTTPS_PROXY", "socks5://alice:wrong@"+up.ln.Addr().String())
	t.Setenv("NO_PROXY", "")
	orig := resolveEgressTarget
	resolveEgressTarget = func(string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("192.0.2.10"), net.ParseIP("192.0.2.11")}, nil
	}
	t.Cleanup(func() { resolveEgressTarget = orig })

	p := proxyAllowing(t, "pkgs.example.test:443")
	status, _ := connectThrough(t, p, "pkgs.example.test:443")
	if !strings.Contains(status, " 502 ") {
		t.Fatalf("a refused login gave %q, want 502", status)
	}
	if _, logins, requests := up.seen(); len(logins) != 1 || len(requests) != 0 {
		t.Fatalf("the proxy saw %d logins and %d requests, want 1 login and no request", len(logins), len(requests))
	}
}

// The schemes nvx forwards through, and the default port each one gets.
// Anything else is ignored, so connections are made directly.
func TestUpstreamProxySchemes(t *testing.T) {
	cases := map[string]string{
		"http://proxy.example":           "proxy.example:80",
		"https://proxy.example":          "proxy.example:443",
		"HTTPS://proxy.example:8443":     "proxy.example:8443",
		"socks5://proxy.example":         "proxy.example:1080",
		"socks5h://u:p@proxy.example:99": "proxy.example:99",
		"socks4://proxy.example":         "",
		"ftp://proxy.example":            "",
	}
	for in, want := range cases {
		t.Setenv("HTTPS_PROXY", in)
		got := ""
		if u := upstreamProxyFromEnv(); u != nil {
			got = u.addr
		}
		if got != want {
			t.Errorf("HTTPS_PROXY=%s gave upstream %q, want %q", in, got, want)
		}
	}
}
