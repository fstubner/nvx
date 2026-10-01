package nvx

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Forwarding allowed connections through the user's own proxy.
//
// The egress proxy dialled every allowed host directly, and the contained
// process gets nvx's proxy in place of the user's HTTPS_PROXY. So on a machine
// that reaches the internet only through a corporate proxy, a contained install
// could reach nothing. When nvx's own environment names a proxy, an allowed
// connection now goes through it as a CONNECT tunnel. The allowlist decides
// first, in nvx, and a host it refuses is never sent to the upstream proxy.

// upstreamProxy is the proxy from HTTPS_PROXY or HTTP_PROXY.
type upstreamProxy struct {
	addr string // host:port of the proxy itself, safe to print
	// auth is the Proxy-Authorization value built from the URL's user and
	// password. It is sent to the proxy and nowhere else.
	auth    string
	noProxy []string
}

// upstreamProxyFromEnv reads the proxy the user's environment names for
// HTTPS, then for HTTP, the way Go's and curl's clients do. nil means none.
func upstreamProxyFromEnv() *upstreamProxy {
	raw := firstEnv("HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy")
	if raw == "" {
		return nil
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		LogWarn("Ignoring the HTTPS_PROXY or HTTP_PROXY setting, because it is not a proxy URL. Contained connections are made directly.")
		return nil
	}
	if !strings.EqualFold(u.Scheme, "http") {
		LogWarn("Ignoring the %s:// proxy in HTTPS_PROXY or HTTP_PROXY. nvx forwards contained connections only through an http:// proxy, so they are made directly.", u.Scheme)
		return nil
	}
	addr := u.Host
	if u.Port() == "" {
		addr = net.JoinHostPort(u.Hostname(), "80")
	}
	up := &upstreamProxy{addr: addr}
	if u.User != nil {
		pass, _ := u.User.Password()
		up.auth = "Basic " + base64.StdEncoding.EncodeToString([]byte(u.User.Username()+":"+pass))
	}
	for _, e := range strings.Split(firstEnv("NO_PROXY", "no_proxy"), ",") {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			up.noProxy = append(up.noProxy, e)
		}
	}
	return up
}

func firstEnv(names ...string) string {
	for _, n := range names {
		if v := strings.TrimSpace(os.Getenv(n)); v != "" {
			return v
		}
	}
	return ""
}

// bypass reports whether NO_PROXY, or a destination spelled as this machine,
// keeps a connection off the upstream proxy. The entries follow Go's reading:
// "*" matches everything, "example.com" matches it and its subdomains,
// ".example.com" only its subdomains, an IP or CIDR matches addresses, and a
// ":port" suffix narrows any of them to that port.
func (u *upstreamProxy) bypass(hp hostPort) bool {
	if isLoopback(hp.host) {
		return true
	}
	host := strings.TrimSuffix(strings.ToLower(hp.host), ".")
	for _, entry := range u.noProxy {
		if entry == "*" {
			return true
		}
		if h, p, err := net.SplitHostPort(entry); err == nil {
			if p != strconv.Itoa(int(hp.port)) {
				continue
			}
			entry = h
		}
		entry = strings.TrimPrefix(entry, "*")
		if _, cidr, err := net.ParseCIDR(entry); err == nil {
			if ip := net.ParseIP(host); ip != nil && cidr.Contains(ip) {
				return true
			}
			continue
		}
		if ip := net.ParseIP(strings.Trim(entry, "[]")); ip != nil {
			if hostIP := net.ParseIP(host); hostIP != nil && hostIP.Equal(ip) {
				return true
			}
			continue
		}
		if strings.HasPrefix(entry, ".") {
			if strings.HasSuffix(host, entry) {
				return true
			}
			continue
		}
		if host == entry || strings.HasSuffix(host, "."+entry) {
			return true
		}
	}
	return false
}

// connect opens a tunnel to hp through the upstream proxy. The destination is
// sent by name, so the upstream proxy resolves it, as it would for the user's
// own tools.
func (u *upstreamProxy) connect(hp hostPort) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", u.addr, connectDialTimeout)
	if err != nil {
		return nil, fmt.Errorf("upstream proxy %s: %w", u.addr, err)
	}
	target := net.JoinHostPort(hp.host, strconv.Itoa(int(hp.port)))
	_ = conn.SetDeadline(time.Now().Add(connectDialTimeout))
	req := "CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\n"
	if u.auth != "" {
		req += "Proxy-Authorization: " + u.auth + "\r\n"
	}
	if _, err := conn.Write([]byte(req + "\r\n")); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("upstream proxy %s: %w", u.addr, err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("upstream proxy %s: %w", u.addr, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_ = conn.Close()
		return nil, fmt.Errorf("upstream proxy %s answered %s", u.addr, resp.Status)
	}
	_ = conn.SetDeadline(time.Time{})
	if br.Buffered() > 0 {
		return &bufferedConn{Conn: conn, r: br}, nil
	}
	return conn, nil
}

// bufferedConn keeps bytes the response reader took past the proxy's answer.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(b []byte) (int, error) { return c.r.Read(b) }

// dialAllowed connects to a destination the allowlist has already permitted:
// through the user's proxy when there is one and NO_PROXY does not exclude the
// destination, otherwise to the vetted addresses directly.
func (p *EgressProxy) dialAllowed(ips []net.IP, hp hostPort) (net.Conn, error) {
	if p.upstream != nil && !p.upstream.bypass(hp) {
		return p.upstream.connect(hp)
	}
	return dialVetted(ips, hp.host, hp.port)
}
