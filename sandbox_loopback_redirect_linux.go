//go:build linux

package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// network.mode "loopback": the services on your machine are reachable from the
// sandbox, at their own addresses, over any TCP protocol.
//
// The mode already meant that on macOS, where the sandbox shares the host's
// loopback and the Seatbelt profile simply permits it. Linux has a network
// namespace of its own, so 127.0.0.1 in there is a different 127.0.0.1, and
// permitting something does not create a route. Until this, the reach came from
// nvx's HTTP proxy allowing loopback destinations -- which covers what a
// proxy-aware client sends and leaves a raw connection to a local database with
// nowhere to go.
//
// So every loopback TCP connection is redirected to a relay inside the
// namespace, which asks the kernel what the connection was originally for and
// carries it to the parent over AF_UNIX:
//
//	contained tool → 127.0.0.1:5432
//	   ↓ iptables REDIRECT (nat/OUTPUT, inside the namespace)
//	relay → SO_ORIGINAL_DST says 127.0.0.1:5432
//	   ↓ AF_UNIX in the guest home
//	nvx, outside → 127.0.0.1:5432   (the real service)
//
// The address the tool dials is the address that is reached, which is the whole
// difference from --connect: no port to name, no second number, nothing to put
// in the command line.
//
// Two things this must not break, both handled below rather than discovered
// later:
//
//   - A server the sandbox runs itself. A contained `npm run dev` binding
//     127.0.0.1:3000 and a contained test connecting to it must reach each
//     other, not the host's port 3000 or a refusal. loopbackRelay.forward tries
//     the namespace first for exactly this.
//   - nvx's own listeners inside the namespace -- the egress proxy relay, and any
//     --connect port. Those are excluded from the rules, so egress does not take
//     an extra hop through this.
//
// Not a widening of what the sandbox may reach beyond the mode's definition. The
// parent refuses anything that is not a loopback address, which matters because
// the socket sits in the guest home, where the contained process can reach it
// and ask for whatever it likes.

// loopbackRelayMark tags the relay's own connections so the redirect rules skip
// them. Without it the relay's attempt to reach an in-namespace service is
// redirected straight back to itself.
const loopbackRelayMark = 0x4e56 // "NV"

// soOriginalDst is SO_ORIGINAL_DST from linux/netfilter_ipv4.h, and
// IP6T_SO_ORIGINAL_DST from netfilter_ipv6/ip6_tables.h. The same number for
// both families; the level differs.
const soOriginalDst = 80

// loopbackRedirectHeaderLen is the fixed header the relay sends the parent:
// one byte of address family, sixteen of address, two of port.
const loopbackRedirectHeaderLen = 19

// loopbackSocketName is the parent's socket for this mode, beside the egress one
// and in the same place for the same reason: the guest home already carries the
// rights the contained process has, and belongs to this run alone.
const loopbackSocketName = ".nvx-loopback.sock"

func loopbackSocketPath(guestHome string) string {
	return guestHome + string(os.PathSeparator) + loopbackSocketName
}

// ---------------------------------------------------------------------------
// Parent side, outside the namespace.
// ---------------------------------------------------------------------------

// openLoopbackSocket serves the relay's requests: read a destination, refuse
// anything that is not loopback, dial it, splice.
//
// The refusal is the enforcement point for this mode, not a formality. The
// socket lives in the guest home, so the contained process can open it directly
// and send whatever header it wants; without this check "loopback mode" would be
// "any address the sandbox names", which is `open` with extra steps.
func openLoopbackSocket(guestHome, nvxHome string) (stop func(), err error) {
	sock := loopbackSocketPath(guestHome)
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return func() {}, fmt.Errorf("loopback socket: %w", err)
	}
	s := &loopbackServer{nvxHome: nvxHome, reported: map[int]bool{}}
	go func() {
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	return func() { _ = ln.Close() }, nil
}

type loopbackServer struct {
	nvxHome string

	mu       sync.Mutex
	reported map[int]bool
}

func (s *loopbackServer) serve(conn net.Conn) {
	header := make([]byte, loopbackRedirectHeaderLen)
	// Bounded, because the other end of this socket is the contained process.
	//
	// Without a deadline, a sandbox that opens connections and never sends the
	// header holds a goroutine and a descriptor in the PARENT for each one, for
	// as long as the run lasts -- untrusted code choosing how much of nvx's own
	// process to occupy. The Windows tunnel bounds the same read for the same
	// reason (readPeerHeader in sandbox_connect_windows.go); this one did not.
	//
	// Cleared once the header is in, so the splice that follows is not cut off
	// mid-transfer: the limit is on how long the sandbox may take to say what it
	// wants, never on how long the traffic then runs.
	_ = conn.SetReadDeadline(time.Now().Add(connectDialTimeout))
	if _, err := io.ReadFull(conn, header); err != nil {
		_ = conn.Close()
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	ip := net.IP(header[1:17])
	if header[0] == 4 {
		ip = ip[:4]
	}
	port := int(binary.BigEndian.Uint16(header[17:19]))

	if !ip.IsLoopback() {
		// The contained process reached the socket and asked for something else.
		// Audited every time: one attempt is a bug somewhere, a stream of them is
		// a sandbox trying addresses.
		auditLog(s.nvxHome, "loopback_redirect_refused", map[string]string{
			"address": net.JoinHostPort(ip.String(), strconv.Itoa(port)),
		})
		LogWarn("Refused a request to reach %s: network.mode loopback carries loopback addresses only.",
			net.JoinHostPort(ip.String(), strconv.Itoa(port)))
		_ = conn.Close()
		return
	}

	target := net.JoinHostPort(ip.String(), strconv.Itoa(port))
	svc, derr := net.DialTimeout("tcp", target, connectDialTimeout)
	if derr != nil {
		_ = conn.Close()
		return
	}
	s.reportOnce(port, target)
	spliceConns(conn, svc)
}

// reportOnce names each host service the sandbox actually reached, one line per
// port. Every one is audited; the console gets the first of each, because a
// client that reconnects per query would otherwise bury the rest of the output.
func (s *loopbackServer) reportOnce(port int, target string) {
	auditLog(s.nvxHome, "loopback_redirect", map[string]string{"address": target})
	s.mu.Lock()
	seen := s.reported[port]
	s.reported[port] = true
	s.mu.Unlock()
	if !seen {
		LogDetail("The sandbox reached %s on this machine (network.mode loopback).", target)
	}
}

// ---------------------------------------------------------------------------
// Child side, inside the namespace.
// ---------------------------------------------------------------------------

// startLoopbackRedirect installs the redirect rules and serves what they catch.
//
// excludePorts are nvx's own listeners inside the namespace, which must reach
// what they were built to reach rather than being carried to the host.
//
// A host where the rules cannot be installed is reported and left alone. That is
// the safe direction: without them the sandbox keeps the reach it had before
// this existed -- loopback destinations through nvx's HTTP proxy -- which is
// narrower than intended rather than wider, so there is nothing to fail closed
// against.
func startLoopbackRedirect(ctx context.Context, guestHome string, excludePorts []int) (stop func(), err error) {
	sock := loopbackSocketPath(guestHome)
	if _, serr := os.Stat(sock); serr != nil {
		return func() {}, fmt.Errorf("the parent did not open its loopback socket: %w", serr)
	}

	// Bound to every interface, which inside this namespace is loopback and
	// nothing else, so one listener answers both families.
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		return func() {}, fmt.Errorf("loopback relay listener: %w", err)
	}
	relayPort := ln.Addr().(*net.TCPAddr).Port

	if err := installLoopbackRedirectRules(relayPort, excludePorts); err != nil {
		_ = ln.Close()
		return func() {}, err
	}

	r := &loopbackRelay{sock: sock, ctx: ctx}
	go func() {
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go r.forward(conn)
		}
	}()
	return func() { _ = ln.Close() }, nil
}

type loopbackRelay struct {
	sock string
	ctx  context.Context
}

// forward sends one connection where it was going: to a service inside this
// sandbox if there is one, otherwise to the host.
//
// The namespace is tried first, and the order is the point. A contained `npm run
// dev` on 127.0.0.1:3000 and a contained test client must find each other; if the
// host were tried first, they would reach the developer's own port 3000 instead,
// which is both wrong and silent.
func (r *loopbackRelay) forward(conn net.Conn) {
	ip, port, err := originalDestination(conn)
	if err != nil {
		_ = conn.Close()
		return
	}

	if local, lerr := dialInsideNamespace(ip, port); lerr == nil {
		spliceConns(conn, local)
		return
	}

	upstream, derr := net.Dial("unix", r.sock)
	if derr != nil {
		_ = conn.Close()
		return
	}
	header := make([]byte, loopbackRedirectHeaderLen)
	if v4 := ip.To4(); v4 != nil {
		header[0] = 4
		copy(header[1:], v4)
	} else {
		header[0] = 6
		copy(header[1:], ip.To16())
	}
	// #nosec G115 -- a TCP port, from the kernel's own sockaddr
	binary.BigEndian.PutUint16(header[17:19], uint16(port))
	if _, werr := upstream.Write(header); werr != nil {
		_ = conn.Close()
		_ = upstream.Close()
		return
	}
	spliceConns(conn, upstream)
}

// dialInsideNamespace connects without being redirected back to the relay, by
// marking the socket the way the first rule skips.
func dialInsideNamespace(ip net.IP, port int) (net.Conn, error) {
	d := net.Dialer{
		Control: func(_, _ string, c syscall.RawConn) error {
			var serr error
			if cerr := c.Control(func(fd uintptr) {
				serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_MARK, loopbackRelayMark)
			}); cerr != nil {
				return cerr
			}
			return serr
		},
		Timeout: connectDialTimeout,
	}
	return d.Dial("tcp", net.JoinHostPort(ip.String(), strconv.Itoa(port)))
}

// originalDestination asks the kernel where a redirected connection was going.
//
// The address is gone from the socket itself by the time it is accepted: the
// kernel rewrote the destination, so LocalAddr is the relay. netfilter keeps the
// original and hands it back through this one socket option.
func originalDestination(conn net.Conn) (net.IP, int, error) {
	tcp, ok := conn.(*net.TCPConn)
	if !ok {
		return nil, 0, fmt.Errorf("not a TCP connection")
	}
	raw, err := tcp.SyscallConn()
	if err != nil {
		return nil, 0, err
	}

	// sockaddr_in is 16 bytes and sockaddr_in6 is 28; ask for the larger and read
	// the family the kernel reports back.
	var buf [28]byte
	size := uint32(len(buf))
	var errno syscall.Errno
	level := syscall.IPPROTO_IP
	if isIPv6Conn(tcp) {
		level = syscall.IPPROTO_IPV6
	}
	if cerr := raw.Control(func(fd uintptr) {
		_, _, errno = syscall.Syscall6(
			syscall.SYS_GETSOCKOPT,
			fd,
			uintptr(level),
			uintptr(soOriginalDst),
			uintptr(unsafe.Pointer(&buf[0])),
			uintptr(unsafe.Pointer(&size)),
			0,
		)
	}); cerr != nil {
		return nil, 0, cerr
	}
	if errno != 0 {
		return nil, 0, errno
	}

	// sockaddr_in / sockaddr_in6: family (2), port (2, network order), then the
	// address -- at offset 4 for IPv4, offset 8 for IPv6.
	family := binary.LittleEndian.Uint16(buf[0:2])
	port := int(binary.BigEndian.Uint16(buf[2:4]))
	switch family {
	case syscall.AF_INET:
		return net.IP(buf[4:8]), port, nil
	case syscall.AF_INET6:
		return net.IP(buf[8:24]), port, nil
	default:
		return nil, 0, fmt.Errorf("unexpected address family %d", family)
	}
}

func isIPv6Conn(c *net.TCPConn) bool {
	addr, ok := c.LocalAddr().(*net.TCPAddr)
	if !ok {
		return false
	}
	return addr.IP.To4() == nil
}

// ---------------------------------------------------------------------------
// The rules.
// ---------------------------------------------------------------------------

// installLoopbackRedirectRules points loopback TCP at the relay, in both address
// families.
//
// Order matters and is the reason these are appended one at a time rather than
// restored as a table: the skip rules have to precede the redirect, or nvx's own
// listeners and the relay's own connections are caught by it.
//
// IPv6 is not optional-by-accident. `localhost` resolves to ::1 before 127.0.0.1
// on a normal Linux resolver, so a tool told to reach "localhost:5432" would miss
// an IPv4-only redirect entirely -- which is the kind of half-working that is
// worse than not working.
func installLoopbackRedirectRules(relayPort int, excludePorts []int) error {
	if _, err := exec.LookPath("iptables"); err != nil {
		return fmt.Errorf("iptables is not installed")
	}

	var ipv6Err error
	for _, r := range loopbackRedirectRules(relayPort, excludePorts) {
		out, err := runIptables(r.Cmd, r.Args...)
		if err == nil {
			continue
		}
		if r.Cmd == "ip6tables" {
			// Recorded and carried on. A kernel built without IPv6, or without its
			// nat table, is a real configuration; losing ::1 there is worth saying
			// and is not a reason to lose 127.0.0.1 as well.
			ipv6Err = fmt.Errorf("%v: %s", err, out)
			continue
		}
		return fmt.Errorf("%s %s: %v: %s", r.Cmd, strings.Join(r.Args, " "), err, out)
	}
	if ipv6Err != nil {
		LogWarn("Loopback redirection covers 127.0.0.1 but not ::1 on this host (%v); a tool that resolves localhost to ::1 will not reach your services.", ipv6Err)
	}
	return nil
}

// iptablesRule is one invocation. A struct rather than a []string so the order
// the rules are appended in is visible to a test: netfilter evaluates a chain in
// order, so the skips have to precede the redirect, and getting that wrong sends
// nvx's own egress relay through this relay instead of to the proxy.
type iptablesRule struct {
	Cmd  string
	Args []string
}

// loopbackRedirectRules builds the chain, in order, for both address families.
//
// Pure, for the reason dockerRunArgs is: the ordering is the part that is easy
// to get wrong and impossible to see afterwards, and reaching the real thing
// needs a network namespace, CAP_NET_ADMIN and a working nat table.
func loopbackRedirectRules(relayPort int, excludePorts []int) []iptablesRule {
	var rules []iptablesRule
	for _, spec := range []struct{ cmd, dest string }{
		{"iptables", "127.0.0.0/8"},
		{"ip6tables", "::1/128"},
	} {
		// First: the relay's own connections, or its attempt to reach a service
		// inside this sandbox is redirected straight back to itself.
		rules = append(rules, iptablesRule{spec.cmd, []string{
			"-t", "nat", "-A", "OUTPUT", "-m", "mark", "--mark", strconv.Itoa(loopbackRelayMark), "-j", "RETURN",
		}})
		// Then nvx's own listeners inside the namespace: the egress proxy relay and
		// any --connect port. They reach what they were built to reach.
		for _, p := range excludePorts {
			if p <= 0 {
				continue
			}
			rules = append(rules, iptablesRule{spec.cmd, []string{
				"-t", "nat", "-A", "OUTPUT", "-p", "tcp", "-d", spec.dest,
				"--dport", strconv.Itoa(p), "-j", "RETURN",
			}})
		}
		// Everything else loopback goes to the relay.
		rules = append(rules, iptablesRule{spec.cmd, []string{
			"-t", "nat", "-A", "OUTPUT", "-p", "tcp", "-d", spec.dest,
			"-j", "REDIRECT", "--to-ports", strconv.Itoa(relayPort),
		}})
	}
	return rules
}

func runIptables(cmd string, args ...string) (string, error) {
	c := exec.Command(cmd, args...)
	c.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin"}
	out, err := c.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
