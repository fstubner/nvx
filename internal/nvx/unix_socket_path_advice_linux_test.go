//go:build linux

package nvx

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// The number the refusal names must work. The sockets have different names, so
// an NVX_HOME that holds the egress socket can still be refused by a longer
// tunnel or loopback name, and advice computed from the socket that failed would
// send a person round again. The advised length is the room the longest socket
// the session creates needs, and every socket is checked to fit under it.
func TestTheAdvisedNvxHomeHoldsEverySocketTheSessionCreates(t *testing.T) {
	const guestRest = "/sandbox_home/0123456789abcdef/"
	homeOfLength := func(n int) string { return "/" + strings.Repeat("h", n-1) }
	guestUnder := func(nvxHome string) string {
		return filepath.Join(getSandboxHomeDir(nvxHome), "0123456789abcdef")
	}
	maxFor := func(name string) int { return unixSocketPathMax - 1 - len(guestRest+name) }
	wantAdvice := func(max, have int) string {
		return fmt.Sprintf("at most %d characters (it is %d)", max, have)
	}
	// Every socket this session creates fits under a home of the advised length.
	assertHolds := func(t *testing.T, max int, netCtx *NetworkLaunchContext) {
		t.Helper()
		guest := guestUnder(homeOfLength(max))
		socks := linuxSessionSockets(guest, netCtx)
		if len(socks) == 0 {
			t.Fatal("the session lists no sockets, so nothing was checked")
		}
		for _, s := range socks {
			if !egressSocketPathFits(s) {
				t.Errorf("following the advice (NVX_HOME of %d) still leaves %s over the limit", max, s)
			}
		}
	}

	// A home the egress socket fits in exactly. The tunnel and loopback names are
	// longer, so each of those is refused here with its own, smaller, maximum.
	fitsEgress := homeOfLength(maxFor(egressSocketName))
	egressSock := filepath.Join(guestUnder(fitsEgress), egressSocketName)
	if !egressSocketPathFits(egressSock) {
		t.Fatalf("a %d-byte NVX_HOME was meant to hold the egress socket", len(fitsEgress))
	}

	t.Run("tunnel socket longer than the egress one", func(t *testing.T) {
		netCtx := &NetworkLaunchContext{Mode: "proxy", ConnectPorts: []connectMapping{{Host: 9222}, {Host: 65535}}}
		if err := linuxSocketTooLong("egress socket", egressSock, guestUnder(fitsEgress), fitsEgress, netCtx); err != nil {
			t.Fatalf("the egress socket fits and was refused: %v", err)
		}
		_, stop, err := openConnectSockets(guestUnder(fitsEgress), fitsEgress, netCtx)
		stop()
		max := maxFor(".nvx-connect-65535.sock")
		if err == nil || !strings.Contains(err.Error(), wantAdvice(max, len(fitsEgress))) {
			t.Fatalf("tunnel socket: got %v, want it to contain %q", err, wantAdvice(max, len(fitsEgress)))
		}
		assertHolds(t, max, netCtx)
	})

	t.Run("loopback socket longer than the egress one", func(t *testing.T) {
		netCtx := &NetworkLaunchContext{Mode: "loopback"}
		stop, err := openLoopbackSocket(guestUnder(fitsEgress), fitsEgress, netCtx)
		stop()
		max := maxFor(loopbackSocketName)
		if err == nil || !strings.Contains(err.Error(), wantAdvice(max, len(fitsEgress))) {
			t.Fatalf("loopback socket: got %v, want it to contain %q", err, wantAdvice(max, len(fitsEgress)))
		}
		assertHolds(t, max, netCtx)
	})

	t.Run("a refused egress socket names the room the tunnel socket needs", func(t *testing.T) {
		long := homeOfLength(maxFor(egressSocketName) + 30)
		netCtx := &NetworkLaunchContext{Mode: "proxy", ConnectPorts: []connectMapping{{Host: 65535}}}
		err := linuxSocketTooLong("egress socket", filepath.Join(guestUnder(long), egressSocketName), guestUnder(long), long, netCtx)
		max := maxFor(".nvx-connect-65535.sock")
		if err == nil || !strings.Contains(err.Error(), wantAdvice(max, len(long))) {
			t.Fatalf("egress socket: got %v, want it to contain %q", err, wantAdvice(max, len(long)))
		}
		assertHolds(t, max, netCtx)
	})
}
