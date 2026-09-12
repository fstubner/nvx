//go:build linux

package nvx

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

// The two halves join up: a connection to the in-sandbox port reaches the real
// service, and bytes come back.
//
// Both halves run in this process, which is the honest scope of the test. The
// namespace is what they exist to cross, and a test cannot create one without
// the privileges the sandbox itself needs; scripts/sandbox-smoke.sh drives the
// real thing on CI. What is checked here is that the parent's socket, the
// supervisor's listener and the splice in between carry traffic at all -- the
// part that would be silently wrong in either direction.
func TestConnectHalvesCarryTrafficToTheHostService(t *testing.T) {
	guestHome := tempDir(t)
	service := startEchoService(t)

	netCtx := NetworkLaunchContext{
		Mode:         "proxy",
		ConnectPorts: []connectMapping{{Host: service}},
	}
	env, stopParent, err := openConnectSockets(guestHome, &netCtx)
	if err != nil {
		t.Fatalf("the parent could not open the tunnel socket: %v", err)
	}
	defer stopParent()

	inside := netCtx.ConnectPorts[0].Inside
	if inside == 0 {
		t.Fatal("the in-sandbox port was never resolved, so the supervisor has nothing to listen on")
	}
	want := fmt.Sprintf("NVX_CONNECT_%d=%d", service, inside)
	if len(env) != 1 || env[0] != want {
		t.Fatalf("the contained tool is not told where to dial: got %v, want [%s]", env, want)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopChild, err := startContainedConnectListeners(ctx, guestHome, netCtx.ConnectPorts)
	if err != nil {
		t.Fatalf("the supervisor could not listen: %v", err)
	}
	defer stopChild()

	if got := roundTrip(t, inside, "hello"); got != "hello" {
		t.Fatalf("the tunnel did not carry the traffic: got %q", got)
	}
}

// The socket lives in this run's guest home, which is what makes it private.
//
// Not decoration: the guest home is per-run and carries the Landlock rights the
// contained process has, so a socket there needs no extra rule and no peer check
// -- the reasoning the Windows tunnel needs an explicit identity check for. A
// socket placed anywhere else would quietly lose that.
func TestConnectSocketLivesInTheGuestHome(t *testing.T) {
	guestHome := tempDir(t)
	netCtx := NetworkLaunchContext{ConnectPorts: []connectMapping{{Host: 9222, Inside: 19222}}}
	_, stop, err := openConnectSockets(guestHome, &netCtx)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	sock := linuxConnectSocketPath(guestHome, 9222)
	if !strings.HasPrefix(sock, guestHome) {
		t.Fatalf("the tunnel socket is outside the guest home: %s", sock)
	}
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("the tunnel socket was not created: %v", err)
	}
}

// A stale socket file from a killed run does not stop the next one.
//
// The guest home is this run's own, so a file at that path is debris rather than
// another live tunnel. Left in place it fails Listen with EADDRINUSE, and
// --connect would break for reasons nobody could see, after any run that was
// killed rather than closed.
func TestConnectReplacesAStaleSocketFile(t *testing.T) {
	guestHome := tempDir(t)
	sock := linuxConnectSocketPath(guestHome, 9222)
	if err := os.WriteFile(sock, []byte("debris"), 0o600); err != nil {
		t.Fatal(err)
	}

	netCtx := NetworkLaunchContext{ConnectPorts: []connectMapping{{Host: 9222, Inside: 19222}}}
	_, stop, err := openConnectSockets(guestHome, &netCtx)
	if err != nil {
		t.Fatalf("a leftover socket file stopped the run: %v", err)
	}
	stop()
}

// The modes that deny the sandbox every IP socket are named, and the default is
// not one of them.
//
// This decides whether a developer is warned or silently given nothing, and the
// list has to match buildOfflineNetworkFilter's own switch. Trimming is part of
// it: "offline " with a trailing space reaching the wrong branch is a defect
// this codebase has already had once, in networkModeRequiresNamespace.
func TestConnectNamesTheModesThatCannotCarryIt(t *testing.T) {
	for _, mode := range []string{"offline", "loopback", "OFFLINE", " offline "} {
		if !connectUnsupportedForMode(mode) {
			t.Errorf("mode %q denies the sandbox an IP socket, so --connect must be refused there", mode)
		}
	}
	for _, mode := range []string{"proxy", "open", "", "PROXY"} {
		if connectUnsupportedForMode(mode) {
			t.Errorf("mode %q can carry --connect, but it is refused", mode)
		}
	}
}
