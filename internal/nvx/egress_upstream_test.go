package nvx

import (
	"bufio"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeUpstream is a corporate proxy stand-in. It records each CONNECT and then
// echoes the tunnel's bytes back.
type fakeUpstream struct {
	addr     string
	mu       sync.Mutex
	targets  []string
	authSeen []string
}

func (f *fakeUpstream) seen() ([]string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.targets...), append([]string(nil), f.authSeen...)
}

func startFakeUpstream(t *testing.T) *fakeUpstream {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	f := &fakeUpstream{addr: ln.Addr().String()}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				br := bufio.NewReader(c)
				req, err := http.ReadRequest(br)
				if err != nil {
					return
				}
				f.mu.Lock()
				f.targets = append(f.targets, req.Method+" "+req.Host)
				f.authSeen = append(f.authSeen, req.Header.Get("Proxy-Authorization"))
				f.mu.Unlock()
				_, _ = io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n")
				_, _ = io.Copy(c, br)
			}()
		}
	}()
	return f
}

// connectThrough asks nvx's proxy for a tunnel to target and returns the
// status line, and the echo of "ping" when the tunnel opened.
func connectThrough(t *testing.T, p *EgressProxy, target string) (status, echo string) {
	t.Helper()
	c, err := net.Dial("tcp", p.httpAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	cred := base64.StdEncoding.EncodeToString([]byte(proxyAuthUser + ":" + p.token))
	_, _ = io.WriteString(c, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\nProxy-Authorization: Basic "+cred+"\r\n\r\n")
	br := bufio.NewReader(c)
	status, err = br.ReadString('\n')
	if err != nil {
		return "", ""
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil || line == "\r\n" {
			break
		}
	}
	if !strings.Contains(status, " 200 ") {
		return status, ""
	}
	_, _ = io.WriteString(c, "ping")
	buf := make([]byte, 4)
	if _, err := io.ReadFull(br, buf); err != nil {
		return status, ""
	}
	return status, string(buf)
}

// resolveAs makes the egress proxy's DNS answer from a table, so a test needs
// no real names. A name not in the table does not resolve, as on a machine
// whose only way out is its proxy.
func resolveAs(t *testing.T, table map[string]string) {
	t.Helper()
	orig := resolveEgressTarget
	resolveEgressTarget = func(host string) ([]net.IP, error) {
		if ip, ok := table[host]; ok {
			return []net.IP{net.ParseIP(ip)}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { resolveEgressTarget = orig })
}

// An allowed host is reached through the user's HTTPS_PROXY, with the
// credentials from its URL. Measured 2026-10-01 against main, the egress proxy
// dialled the host itself, so on a machine whose only route out is a corporate
// proxy the tunnel failed with 502.
func TestAllowedConnectGoesThroughTheUpstreamProxy(t *testing.T) {
	up := startFakeUpstream(t)
	t.Setenv("HTTPS_PROXY", "http://alice:p%40ss@"+up.addr)
	t.Setenv("NO_PROXY", "")
	resolveAs(t, nil)

	p := proxyAllowing(t, "pkgs.example.test:443")
	status, echo := connectThrough(t, p, "pkgs.example.test:443")
	if !strings.Contains(status, " 200 ") || echo != "ping" {
		t.Fatalf("the allowed tunnel did not open through the upstream proxy: status %q, echo %q", status, echo)
	}
	targets, auth := up.seen()
	if len(targets) != 1 || targets[0] != "CONNECT pkgs.example.test:443" {
		t.Fatalf("upstream saw %v, want one CONNECT to pkgs.example.test:443", targets)
	}
	if want := "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:p@ss")); auth[0] != want {
		t.Fatalf("upstream got Proxy-Authorization %q, want %q", auth[0], want)
	}
}

// A destination NO_PROXY names is dialled directly, and the upstream proxy
// never hears of it.
func TestNoProxyDestinationIsDialledDirectly(t *testing.T) {
	up := startFakeUpstream(t)
	direct := echoTarget(t)
	_, port, _ := net.SplitHostPort(direct)
	t.Setenv("HTTPS_PROXY", "http://"+up.addr)
	t.Setenv("NO_PROXY", "intranet.example.test")
	resolveAs(t, map[string]string{"build.intranet.example.test": "127.0.0.1"})

	p := proxyAllowing(t, "build.intranet.example.test:"+port)
	status, echo := connectThrough(t, p, "build.intranet.example.test:"+port)
	if !strings.Contains(status, " 200 ") || echo != "ping" {
		t.Fatalf("the NO_PROXY destination was not reached directly: status %q, echo %q", status, echo)
	}
	if targets, _ := up.seen(); len(targets) != 0 {
		t.Fatalf("a NO_PROXY destination was sent to the upstream proxy: %v", targets)
	}
}

// The allowlist decides before anything is forwarded. A host it does not name
// gets 403 from nvx and never reaches the upstream proxy.
func TestRefusedHostIsNeverForwardedUpstream(t *testing.T) {
	up := startFakeUpstream(t)
	t.Setenv("HTTPS_PROXY", "http://"+up.addr)
	t.Setenv("NO_PROXY", "")
	resolveAs(t, map[string]string{"elsewhere.example.test": "203.0.113.7"})

	p := proxyAllowing(t, "pkgs.example.test:443")
	status, _ := connectThrough(t, p, "elsewhere.example.test:443")
	if !strings.Contains(status, " 403 ") {
		t.Fatalf("a host outside the allowlist got %q, want 403", status)
	}
	if targets, _ := up.seen(); len(targets) != 0 {
		t.Fatalf("a refused host was sent to the upstream proxy: %v", targets)
	}
}

func TestNoProxyEntries(t *testing.T) {
	u := &upstreamProxy{noProxy: []string{".corp.example", "intranet.example", "10.0.0.0/8", "192.0.2.1", "api.example:8443"}}
	cases := map[string]bool{
		"a.corp.example:443":     true,
		"corp.example:443":       false,
		"intranet.example:443":   true,
		"x.intranet.example:80":  true,
		"10.2.3.4:443":           true,
		"192.0.2.1:443":          true,
		"api.example:8443":       true,
		"api.example:443":        false,
		"registry.npmjs.org:443": false,
		"localhost:5432":         true,
	}
	for in, want := range cases {
		h, p, _ := net.SplitHostPort(in)
		var port uint16
		for _, ch := range p {
			port = port*10 + uint16(ch-'0')
		}
		if got := u.bypass(parseHostPortSpec(h, port)); got != want {
			t.Errorf("bypass(%s) = %v, want %v", in, got, want)
		}
	}
}
