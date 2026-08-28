//go:build windows

package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"testing"
	"time"
)

// The claim worth testing is end to end: something dialling the in-sandbox port
// reaches the real service on the host, and nothing else does. Both halves run
// in this process -- the AppContainer is not what the plumbing depends on, and
// a test that needed one could only run elevated on a machine with a runtime
// installed, which is how the expose tunnels went untested for a release.
func TestAContainedDialReachesTheHostServiceItWasGranted(t *testing.T) {
	host := startEchoService(t)

	// Stand in for the peer check; this test is about the plumbing carrying bytes
	// end to end. The check's own behaviour is tested with the real one.
	prev := verifyTunnelPeer
	verifyTunnelPeer = func(uint16, uint16) (bool, error) { return true, nil }
	t.Cleanup(func() { verifyTunnelPeer = prev })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	guestHome := shortTempDir(t)

	m := connectMapping{Host: host}
	parent, err := openConnectPort(ctx, t.TempDir(), guestHome, m)
	if err != nil {
		t.Fatalf("openConnectPort: %v", err)
	}
	defer parent.Close()

	inside, err := startConnectListeners(ctx, guestHome, m)
	if err != nil {
		t.Fatalf("startConnectListeners: %v", err)
	}
	if inside == host {
		t.Fatalf("the in-sandbox port is the host's own (%d); they share a network stack and cannot both hold it", host)
	}

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", inside), 5*time.Second)
	if err != nil {
		t.Fatalf("dial the in-sandbox port: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("ping\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	got := make([]byte, 5)
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("read back through the tunnel: %v", err)
	}
	if string(got) != "ping\n" {
		t.Fatalf("got %q through the tunnel, want the echo of what was sent", got)
	}
}

// Cancelling the run closes the way in. Without this the tunnel outlives the
// command that was granted it, and the next thing to bind that port inherits a
// live path to the host -- the "one port, for one run" claim is only true if
// the run's end actually ends it.
func TestTheWayOutClosesWhenTheRunDoes(t *testing.T) {
	host := startEchoService(t)

	ctx, cancel := context.WithCancel(context.Background())
	guestHome := shortTempDir(t)
	m := connectMapping{Host: host}

	parent, err := openConnectPort(ctx, t.TempDir(), guestHome, m)
	if err != nil {
		t.Fatalf("openConnectPort: %v", err)
	}
	inside, err := startConnectListeners(ctx, guestHome, m)
	if err != nil {
		t.Fatalf("startConnectListeners: %v", err)
	}

	cancel()
	parent.Close()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, derr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", inside), time.Second)
		if derr != nil {
			return // refused, which is the point
		}
		_ = conn.Close()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("127.0.0.1:%d still accepted connections after the run was cancelled", inside)
}

// shortTempDir is t.TempDir() with a path an AF_UNIX socket fits inside.
//
// t.TempDir() names the directory after the test, and these test names are long
// enough on their own to push the socket past the 107-character limit -- which
// surfaces as "bind: invalid argument" and reads exactly like a broken tunnel.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "nvxc")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// startEchoService stands in for whatever the developer is already running, and
// returns its port.
func startEchoService(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("start the stand-in host service: %v", err)
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

// The tunnel must refuse a peer it cannot place inside this sandbox. Without
// this, a --connect grant is reachable by every other nvx sandbox on the
// machine: they all share one AppContainer package identity, and Windows permits
// loopback within a package. Measured before the check existed -- a sandbox from
// an unrelated project with no grant of its own read the granted service.
func TestTheTunnelRefusesAPeerItCannotPlaceInThisSandbox(t *testing.T) {
	host := startEchoService(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	guestHome := shortTempDir(t)
	m := connectMapping{Host: host}

	parent, err := openConnectPort(ctx, t.TempDir(), guestHome, m)
	if err != nil {
		t.Fatalf("openConnectPort: %v", err)
	}
	defer parent.Close()

	// No session job is published in a test process, so every peer is
	// unverifiable -- which must fail closed, not open.
	if sessionJob.Load() != 0 {
		t.Fatalf("test precondition: sessionJob should be unset, got %v", sessionJob.Load())
	}

	tun, err := net.DialTimeout("unix", windowsConnectSocketPath(guestHome, m.Host), 5*time.Second)
	if err != nil {
		t.Fatalf("dial the tunnel socket: %v", err)
	}
	defer tun.Close()
	if err := writePeerHeader(tun, 1234, 5678); err != nil {
		t.Fatalf("write peer header: %v", err)
	}
	if _, err := tun.Write([]byte("give me the service\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	_ = tun.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 1)
	if _, rerr := tun.Read(buf); rerr == nil {
		t.Fatal("an unverifiable peer was spliced to the host service; the tunnel must fail closed")
	}
}

// The verifier itself must not answer "yes" when it cannot tell.
func TestPeerCheckFailsClosedWithoutAJob(t *testing.T) {
	if sessionJob.Load() != 0 {
		t.Skip("a session job is published; this checks the no-job path")
	}
	ok, err := peerBelongsToThisSandbox(1234, 5678)
	if ok {
		t.Fatal("peerBelongsToThisSandbox said yes with no job to check against")
	}
	if err == nil {
		t.Fatal("no reason given for the refusal")
	}
}
