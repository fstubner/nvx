//go:build darwin || linux

package nvx

import (
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Shared by the macOS and Linux --connect relay tests. The two platforms carry
// the feature differently -- one relays on the host's own loopback, the other
// tunnels across a network namespace -- but "a service to reach, and a round
// trip through the port the sandbox is given" is the same question for both.

// startEchoService runs a loopback service that echoes what it is sent, standing
// in for the database or browser a developer names.
func startEchoService(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
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
	return ln.Addr().(*net.TCPAddr).Port
}

// roundTrip sends one message to a loopback port and returns what comes back.
func roundTrip(t *testing.T, port int, msg string) string {
	t.Helper()
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 5*time.Second)
	if err != nil {
		t.Fatalf("could not reach 127.0.0.1:%d: %v", port, err)
	}
	defer conn.Close()
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
