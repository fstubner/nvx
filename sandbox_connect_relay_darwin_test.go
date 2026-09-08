//go:build darwin

package main

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"testing"
	"time"
)

// The relay carries traffic to the real service, both directions.
//
// A stand-in service rather than a real one: what is being checked is that a
// connection to the in-sandbox port reaches the host port and that bytes flow
// back, which is the whole of what the relay owes its caller. Whether the
// Seatbelt profile then permits that connection is a separate claim, asserted
// against the generated profile in sandbox_seatbelt_connect_test.go and end to
// end by scripts/sandbox-smoke-macos.sh.
func TestConnectRelayReachesTheHostService(t *testing.T) {
	service := startEchoService(t)

	netCtx := NetworkLaunchContext{
		Mode:         "proxy",
		ConnectPorts: []connectMapping{{Host: service}},
	}
	env, stop, err := startSeatbeltConnectRelays(&netCtx)
	if err != nil {
		t.Fatalf("the relay did not start: %v", err)
	}
	defer stop()

	inside := netCtx.ConnectPorts[0].Inside
	if inside == 0 {
		t.Fatal("the in-sandbox port was never resolved, so the Seatbelt profile has nothing to name")
	}
	if inside == service {
		t.Fatal("the relay bound the service's own port")
	}

	// The variable is how a tool that reads its endpoint from the environment
	// finds the port, and the port nvx chose is not one anybody could hardcode.
	want := fmt.Sprintf("NVX_CONNECT_%d=%d", service, inside)
	if len(env) != 1 || env[0] != want {
		t.Fatalf("the contained tool is not told where to dial: got %v, want [%s]", env, want)
	}

	got := roundTrip(t, inside, "hello")
	if got != "hello" {
		t.Fatalf("the relay did not carry the traffic: got %q", got)
	}
}

// Closing the relay ends the sandbox's route to the service.
//
// The listener outlives nothing: --connect is granted for one run, and a
// listener still accepting after the command exits would leave a path to the
// developer's service open on their loopback for anything on the machine.
func TestConnectRelayStopsWithTheRun(t *testing.T) {
	service := startEchoService(t)
	netCtx := NetworkLaunchContext{ConnectPorts: []connectMapping{{Host: service}}}
	_, stop, err := startSeatbeltConnectRelays(&netCtx)
	if err != nil {
		t.Fatalf("the relay did not start: %v", err)
	}
	inside := netCtx.ConnectPorts[0].Inside
	stop()

	// Retry briefly: the listener closes asynchronously with respect to this
	// goroutine, so a single immediate dial can still be accepted by a socket
	// the kernel has not torn down yet.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, derr := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(inside), 200*time.Millisecond)
		if derr != nil {
			return // refused, which is the point
		}
		_ = c.Close()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("127.0.0.1:%d still accepts connections after the run ended", inside)
}

// A --connect naming a port with nothing behind it does not stop the run.
//
// The service being down is an ordinary state -- a developer starts the tool
// before the database -- and nvx refusing to launch over it would be worse than
// the connection failing when the tool tries. What must not happen is a silent
// success: the relay accepts and then closes, so the tool sees a closed
// connection rather than hanging.
func TestConnectRelayToADeadPortClosesTheConnection(t *testing.T) {
	dead := freeLoopbackPort()
	netCtx := NetworkLaunchContext{ConnectPorts: []connectMapping{{Host: dead}}}
	_, stop, err := startSeatbeltConnectRelays(&netCtx)
	if err != nil {
		t.Fatalf("the relay refused to start for a service that is not up: %v", err)
	}
	defer stop()

	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(netCtx.ConnectPorts[0].Inside), 2*time.Second)
	if err != nil {
		t.Fatalf("the relay did not accept: %v", err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadAll(conn); err != nil {
		t.Fatalf("the connection neither closed nor carried data: %v", err)
	}
}
