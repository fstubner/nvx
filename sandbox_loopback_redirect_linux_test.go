//go:build linux

package main

import (
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The parent dials a loopback address the relay asks for, and carries the
// traffic.
func TestLoopbackSocketReachesAHostService(t *testing.T) {
	guestHome := tempDir(t)
	service := startEchoService(t)

	stop, err := openLoopbackSocket(guestHome, tempDir(t))
	if err != nil {
		t.Fatalf("the parent could not open its loopback socket: %v", err)
	}
	defer stop()

	conn := dialLoopbackSocket(t, guestHome)
	defer conn.Close()
	if _, err := conn.Write(loopbackHeader(net.IPv4(127, 0, 0, 1), service)); err != nil {
		t.Fatal(err)
	}
	if got := writeThenRead(t, conn, "hello"); got != "hello" {
		t.Fatalf("the parent did not carry the traffic: got %q", got)
	}
}

// The parent refuses an address that is not loopback, and dials nothing.
//
// This is the enforcement point for the whole mode. The socket lives in the
// guest home, which the contained process can read and write -- so a sandbox can
// skip the relay, open the socket itself and name any address it likes. Without
// this check, "the services on your machine are reachable" would be "anything is
// reachable", which is `network.mode: open` reached by a side door and with no
// allowlist in the way.
//
// The stand-in is a real listener on a routable-looking address rather than a
// dead one, so the test fails if the parent dials it -- rather than passing
// because nothing happened to be listening.
func TestLoopbackSocketRefusesAnythingThatIsNotLoopback(t *testing.T) {
	guestHome := tempDir(t)

	// 127.0.0.1 is what the sandbox is allowed; this listener is reached through
	// a NON-loopback address of this machine, so a parent that skipped the check
	// would connect and the round trip would succeed.
	elsewhere, addr := startEchoServiceOnAnyAddress(t)
	if addr.IP.IsLoopback() {
		t.Skip("this machine has no non-loopback address to prove the refusal against")
	}

	stop, err := openLoopbackSocket(guestHome, tempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	conn := dialLoopbackSocket(t, guestHome)
	defer conn.Close()
	if _, err := conn.Write(loopbackHeader(addr.IP, elsewhere)); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte("hello")); err == nil {
		got, _ := io.ReadAll(conn)
		if len(got) > 0 {
			t.Fatalf("the parent reached a non-loopback address and returned %q", got)
		}
	}
}

// The skips come before the redirect, in both address families.
//
// netfilter evaluates a chain in order, so a redirect placed first swallows the
// rules meant to exempt nvx's own listeners -- and the egress proxy relay is one
// of them. That failure would not look like a rule ordering problem: egress
// would take a silent extra hop through this relay, and the symptom would be a
// proxy that sometimes did not answer.
func TestLoopbackRulesSkipBeforeTheyRedirect(t *testing.T) {
	const relayPort = 40001
	const proxyPort = 40002

	for _, cmd := range []string{"iptables", "ip6tables"} {
		var markIdx, excludeIdx, redirectIdx = -1, -1, -1
		i := 0
		for _, r := range loopbackRedirectRules(relayPort, []int{proxyPort}) {
			if r.Cmd != cmd {
				continue
			}
			joined := strings.Join(r.Args, " ")
			switch {
			case strings.Contains(joined, "--mark"):
				markIdx = i
			case strings.Contains(joined, "--dport "+strconv.Itoa(proxyPort)):
				excludeIdx = i
			case strings.Contains(joined, "REDIRECT"):
				redirectIdx = i
			}
			i++
		}
		if markIdx < 0 || excludeIdx < 0 || redirectIdx < 0 {
			t.Fatalf("%s is missing a rule: mark=%d exclude=%d redirect=%d", cmd, markIdx, excludeIdx, redirectIdx)
		}
		if markIdx > redirectIdx {
			t.Errorf("%s: the relay's own connections are redirected back to it", cmd)
		}
		if excludeIdx > redirectIdx {
			t.Errorf("%s: nvx's own listener on %d is redirected; the egress proxy would take a hop through this relay", cmd, proxyPort)
		}
	}
}

// A port of 0 contributes no rule.
//
// portOfAddr returns 0 when there is no egress relay, and `--dport 0` is a rule
// matching nothing that would sit in front of the redirect for no reason.
func TestLoopbackRulesIgnoreAnAbsentPort(t *testing.T) {
	for _, r := range loopbackRedirectRules(40001, []int{0, -1}) {
		if strings.Contains(strings.Join(r.Args, " "), "--dport 0") {
			t.Errorf("a rule was built for port 0: %v", r.Args)
		}
	}
}

// Both families are covered.
//
// `localhost` resolves to ::1 before 127.0.0.1 on an ordinary Linux resolver, so
// an IPv4-only redirect leaves a tool told to reach "localhost:5432" going
// nowhere -- working for some callers and not others, which is worse than a
// clean failure.
func TestLoopbackRulesCoverBothAddressFamilies(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range loopbackRedirectRules(40001, nil) {
		seen[r.Cmd] = true
	}
	for _, cmd := range []string{"iptables", "ip6tables"} {
		if !seen[cmd] {
			t.Errorf("no %s rules; that address family is not redirected", cmd)
		}
	}
}

func loopbackHeader(ip net.IP, port int) []byte {
	h := make([]byte, loopbackRedirectHeaderLen)
	if v4 := ip.To4(); v4 != nil {
		h[0] = 4
		copy(h[1:], v4)
	} else {
		h[0] = 6
		copy(h[1:], ip.To16())
	}
	// #nosec G115 -- a port from a listener this test just created
	binary.BigEndian.PutUint16(h[17:19], uint16(port))
	return h
}

func dialLoopbackSocket(t *testing.T, guestHome string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("unix", loopbackSocketPath(guestHome), 5*time.Second)
	if err != nil {
		t.Fatalf("could not reach the parent's loopback socket: %v", err)
	}
	return conn
}

func writeThenRead(t *testing.T, conn net.Conn, msg string) string {
	t.Helper()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(conn, msg); err != nil {
		t.Fatalf("write: %v", err)
	}
	closeWrite(conn)
	got, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return strings.TrimSpace(string(got))
}

// startEchoServiceOnAnyAddress listens on every interface and reports the
// address a non-loopback client would use.
func startEchoServiceOnAnyAddress(t *testing.T) (port int, addr *net.TCPAddr) {
	t.Helper()
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(c)
		}
	}()
	port = ln.Addr().(*net.TCPAddr).Port

	ifaceAddrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range ifaceAddrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() || ipNet.IP.To4() == nil {
			continue
		}
		return port, &net.TCPAddr{IP: ipNet.IP, Port: port}
	}
	return port, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}
}
