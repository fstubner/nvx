package nvx

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
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
//
// Four schemes are understood. http:// and https:// carry a CONNECT request,
// over TLS to the proxy for https://. socks5h:// sends the name to a SOCKS5
// proxy, which resolves it as the http:// proxy does. socks5:// sends the
// addresses nvx itself resolved and vetted, as curl does with that scheme.
// Anything else is ignored with a warning, and connections are made directly.

// upstreamProxy is the proxy from HTTPS_PROXY or HTTP_PROXY.
type upstreamProxy struct {
	scheme string // http, https, socks5 or socks5h
	addr   string // host:port of the proxy itself, safe to print
	// serverName is the proxy's host, which an https:// proxy's certificate
	// must name.
	serverName string
	// auth is the Proxy-Authorization value built from the URL's user and
	// password, for http:// and https://. It is sent to the proxy and nowhere
	// else.
	auth string
	// user and pass are the URL's user and password for socks5:// and
	// socks5h://, sent in the RFC 1929 sub-negotiation and nowhere else.
	// hasUser records that the URL had them at all.
	hasUser    bool
	user, pass string
	// rootCAs verifies an https:// proxy's certificate. nil means the system
	// roots, which is all production ever uses. Tests set a test CA here.
	rootCAs *x509.CertPool
	noProxy []string
}

// upstreamDefaultPorts are the ports Go's own proxy support assumes when the
// URL names none.
var upstreamDefaultPorts = map[string]string{"http": "80", "https": "443", "socks5": "1080", "socks5h": "1080"}

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
	scheme := strings.ToLower(u.Scheme)
	port, known := upstreamDefaultPorts[scheme]
	if !known {
		LogWarn("Ignoring the %s:// proxy in HTTPS_PROXY or HTTP_PROXY. nvx forwards contained connections only through an http://, https://, socks5:// or socks5h:// proxy, so they are made directly.", u.Scheme)
		return nil
	}
	if u.Port() != "" {
		port = u.Port()
	}
	up := &upstreamProxy{scheme: scheme, addr: net.JoinHostPort(u.Hostname(), port), serverName: u.Hostname()}
	if u.User != nil {
		pass, _ := u.User.Password()
		if strings.HasPrefix(scheme, "socks5") {
			// RFC 1929 gives each a one-byte length.
			if len(u.User.Username()) > 255 || len(pass) > 255 {
				LogWarn("Ignoring the SOCKS5 proxy in HTTPS_PROXY or HTTP_PROXY, because its user name or password is longer than the 255 bytes SOCKS5 allows. Contained connections are made directly.")
				return nil
			}
			up.hasUser, up.user, up.pass = true, u.User.Username(), pass
		} else {
			up.auth = "Basic " + base64.StdEncoding.EncodeToString([]byte(u.User.Username()+":"+pass))
		}
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

// connect opens a tunnel to hp through the upstream proxy. ips are the
// addresses nvx resolved and vetted for hp, as dialVetted would dial them.
//
// http://, https:// and socks5h:// send the destination by name, so the
// upstream proxy resolves it, as it would for the user's own tools. socks5://
// sends the vetted addresses instead, so the address nvx judged is the one the
// proxy reaches.
func (u *upstreamProxy) connect(ips []net.IP, hp hostPort) (net.Conn, error) {
	if u.scheme == "socks5" {
		return u.connectSOCKSResolved(ips, hp)
	}
	conn, err := u.dialProxy()
	if err != nil {
		return nil, err
	}
	if u.scheme == "socks5h" {
		err = u.socksConnect(conn, hp.host, hp.port)
	} else {
		conn, err = u.httpConnect(conn, hp)
	}
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

// dialProxy connects to the proxy itself, over TLS for https://, with a
// deadline that bounds the handshake that follows. The caller clears it.
func (u *upstreamProxy) dialProxy() (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", u.addr, connectDialTimeout)
	if err != nil {
		return nil, fmt.Errorf("upstream proxy %s: %w", u.addr, err)
	}
	_ = conn.SetDeadline(time.Now().Add(connectDialTimeout))
	if u.scheme != "https" {
		return conn, nil
	}
	// Verified against the system roots and the proxy's own host name. The
	// proxy sees the Proxy-Authorization credential, so a proxy that cannot
	// prove who it is gets no connection.
	tc := tls.Client(conn, &tls.Config{ServerName: u.serverName, RootCAs: u.rootCAs, MinVersion: tls.VersionTLS12})
	if err := tc.Handshake(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("upstream proxy %s: %w", u.addr, err)
	}
	return tc, nil
}

// httpConnect asks an http:// or https:// proxy for a tunnel to hp.
func (u *upstreamProxy) httpConnect(conn net.Conn, hp hostPort) (net.Conn, error) {
	target := net.JoinHostPort(hp.host, strconv.Itoa(int(hp.port)))
	req := "CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\n"
	if u.auth != "" {
		req += "Proxy-Authorization: " + u.auth + "\r\n"
	}
	if _, err := conn.Write([]byte(req + "\r\n")); err != nil {
		return conn, fmt.Errorf("upstream proxy %s: %w", u.addr, err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		return conn, fmt.Errorf("upstream proxy %s: %w", u.addr, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return conn, fmt.Errorf("upstream proxy %s answered %s", u.addr, resp.Status)
	}
	if br.Buffered() > 0 {
		return &bufferedConn{Conn: conn, r: br}, nil
	}
	return conn, nil
}

// connectSOCKSResolved opens a socks5:// tunnel to the first vetted address
// the proxy can reach, trying each in turn as dialVetted does.
func (u *upstreamProxy) connectSOCKSResolved(ips []net.IP, hp hostPort) (net.Conn, error) {
	ips, err := vettedOrResolve(ips, hp.host)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, ip := range ips {
		conn, err := u.dialProxy()
		if err != nil {
			return nil, err
		}
		err = u.socksConnect(conn, ip.String(), hp.port)
		if err == nil {
			_ = conn.SetDeadline(time.Time{})
			return conn, nil
		}
		_ = conn.Close()
		lastErr = err
		// Only the proxy's answer to one address is worth trying the next for.
		// A refused login would be repeated once per address, and a proxy that
		// locks accounts counts each one.
		if _, ok := err.(socksReplyError); !ok {
			return nil, err
		}
	}
	return nil, lastErr
}

// socksReplyError is a SOCKS5 proxy's refusal to connect to one destination,
// after it accepted nvx's greeting and login.
type socksReplyError struct {
	addr string
	code byte
}

func (e socksReplyError) Error() string {
	reasons := map[byte]string{
		0x01: "general failure", 0x02: "connection not allowed by ruleset", 0x03: "network unreachable",
		0x04: "host unreachable", 0x05: "connection refused", 0x06: "TTL expired",
		0x07: "command not supported", 0x08: "address type not supported",
	}
	reason, ok := reasons[e.code]
	if !ok {
		reason = fmt.Sprintf("reply code %d", e.code)
	}
	return fmt.Sprintf("upstream proxy %s refused the connection: %s", e.addr, reason)
}

// socksConnect runs a SOCKS5 CONNECT (RFC 1928) for host:port on conn, logging
// in with RFC 1929 when the proxy URL had a user. host is an IP address or a
// name. Errors name the proxy's address and never the credentials.
func (u *upstreamProxy) socksConnect(conn net.Conn, host string, port uint16) error {
	fail := func(err error) error { return fmt.Errorf("upstream proxy %s: %w", u.addr, err) }
	const (
		methodNoAuth   = 0x00
		methodUserPass = 0x02
	)
	greeting := []byte{0x05, 1, methodNoAuth}
	if u.hasUser {
		greeting = []byte{0x05, 2, methodNoAuth, methodUserPass}
	}
	if _, err := conn.Write(greeting); err != nil {
		return fail(err)
	}
	choice := make([]byte, 2)
	if _, err := io.ReadFull(conn, choice); err != nil {
		return fail(err)
	}
	if choice[0] != 0x05 {
		return fail(fmt.Errorf("not a SOCKS5 proxy (version %d)", choice[0]))
	}
	switch {
	case choice[1] == methodNoAuth:
	case choice[1] == methodUserPass && u.hasUser:
		msg := []byte{0x01, byte(len(u.user))}
		msg = append(msg, u.user...)
		msg = append(msg, byte(len(u.pass)))
		msg = append(msg, u.pass...)
		if _, err := conn.Write(msg); err != nil {
			return fail(err)
		}
		status := make([]byte, 2)
		if _, err := io.ReadFull(conn, status); err != nil {
			return fail(err)
		}
		if status[1] != 0x00 {
			return fail(errors.New("the proxy refused the user name and password"))
		}
	case choice[1] == methodUserPass:
		return fail(errors.New("the proxy asks for a user name and password, and the proxy URL has none"))
	default:
		return fail(errors.New("the proxy accepts none of the authentication methods nvx offered"))
	}

	req := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			req = append(append(req, 0x01), ip4...)
		} else {
			req = append(append(req, 0x04), ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return fail(errors.New("destination name too long for SOCKS5"))
		}
		req = append(append(req, 0x03, byte(len(host))), host...)
	}
	req = binary.BigEndian.AppendUint16(req, port)
	if _, err := conn.Write(req); err != nil {
		return fail(err)
	}

	// Reply: version, code, reserved, address type, bound address, port. The
	// bound address is read and dropped, so no reply byte reaches the tunnel.
	reply := make([]byte, 4)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return fail(err)
	}
	if reply[0] != 0x05 {
		return fail(fmt.Errorf("not a SOCKS5 reply (version %d)", reply[0]))
	}
	if reply[1] != 0x00 {
		return socksReplyError{addr: u.addr, code: reply[1]}
	}
	var skip int
	switch reply[3] {
	case 0x01:
		skip = 4 + 2
	case 0x04:
		skip = 16 + 2
	case 0x03:
		l := make([]byte, 1)
		if _, err := io.ReadFull(conn, l); err != nil {
			return fail(err)
		}
		skip = int(l[0]) + 2
	default:
		return fail(fmt.Errorf("unknown address type %d in the SOCKS5 reply", reply[3]))
	}
	if _, err := io.ReadFull(conn, make([]byte, skip)); err != nil {
		return fail(err)
	}
	return nil
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
		return p.upstream.connect(ips, hp)
	}
	return dialVetted(ips, hp.host, hp.port)
}
