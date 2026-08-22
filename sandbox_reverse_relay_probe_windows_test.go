//go:build windows

package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// Prototype: can the host reach a server running inside the AppContainer?
//
// Windows refuses connections INTO an AppContainer, which is why `nvx npx vite`
// binds a port, reports itself listening, and serves nobody (README, Known
// limitations). An implementation plan proposed swapping AppContainer for a
// restricted token to fix that. The cost of the swap is the whole Windows egress
// guarantee: AppContainer is what makes "no network capability granted, the OS
// refuses direct connections" true, and a restricted token has no equivalent.
//
// This probes the alternative. Nothing needs to connect inward if the connection
// is established outward and reused: the supervisor already lives inside the
// container, and two facts are already proven here --
//
//   - a contained process can reach the parent over AF_UNIX
//     (TestAppContainerCanReachAFUnixSocket)
//   - loopback inside the container works
//     (TestAppContainerIntraContainerLoopback)
//
// which is everything a reverse tunnel needs. The contained side dials out, the
// parent holds those connections, and each inbound host request is spliced onto
// one. Same shape as the egress relay, inverted.
//
// The load-bearing constraint is the last assertion: this must work with NO
// network capability granted. If it needed internetClient it would reopen the
// egress hole to close a convenience gap, and would be worth nothing.
func TestReverseRelayReachesAServerInsideTheContainer(t *testing.T) {
	// Behind its own switch, not NVX_PROBE, and deliberately not run by CI.
	//
	// This is a feasibility prototype -- it demonstrated that a host can reach a
	// server inside the container without granting a network capability -- and it
	// is not a regression guard for anything shipped. It is also flaky: measured
	// about 1 run in 8, in two different modes, one where the AppContainer launch
	// cannot find its own working directory and one where the host's polling
	// deadline expires under load while the child is serving correctly. Widening
	// CI to run every probe was right; letting a prototype turn the pipeline red
	// at random is not, because a suite that cries wolf gets re-run instead of
	// read.
	//
	// Run it deliberately with NVX_PROBE_PROTOTYPES=1. Fixing the flakiness, or
	// promoting this into a real feature with a real test, are both fine futures;
	// gating other people's builds on it while it is neither is not.
	if os.Getenv("NVX_PROBE_PROTOTYPES") != "1" {
		t.Skip("set NVX_PROBE_PROTOTYPES=1 to run (flaky feasibility prototype, not a regression guard)")
	}
	if os.Getenv("NVX_REVERSE_CHILD") == "1" {
		runReverseRelayChild()
		os.Exit(0)
	}

	const probeProfile = "nvx.sandbox.reverseprobe"
	sid, err := ensureAppContainerSID(probeProfile)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	defer deleteAppContainerProfile(probeProfile)

	// Deliberately NOT tempDir(t): an AF_UNIX path is capped at 108 bytes and a
	// test-name-derived directory overflows it, which fails as a bind error that
	// looks exactly like a permission denial.
	guestHome, err := os.MkdirTemp("", "nvxrr")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(guestHome)
	workDir := tempDir(t)

	scopeCaps, err := prepareAppContainerFilesystem(sid, "", guestHome, workDir)
	if err != nil {
		t.Fatalf("filesystem prep: %v", err)
	}

	sock := filepath.Join(guestHome, "rr.sock")
	_ = os.Remove(sock)
	tunnelLn, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("this host cannot create AF_UNIX sockets: %v", err)
	}
	defer tunnelLn.Close()

	// Connections the contained side has dialled out and parked, waiting to carry
	// an inbound request. This is the whole trick: the parent never connects in.
	tunnels := make(chan net.Conn, 8)
	go func() {
		for {
			c, aerr := tunnelLn.Accept()
			if aerr != nil {
				return
			}
			select {
			case tunnels <- c:
			default:
				_ = c.Close() // pool full; the child will dial again
			}
		}
	}()

	// What a developer's browser would talk to.
	hostLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer hostLn.Close()

	go func() {
		for {
			inbound, aerr := hostLn.Accept()
			if aerr != nil {
				return
			}
			select {
			case tun := <-tunnels:
				go spliceBoth(inbound, tun)
			case <-time.After(15 * time.Second):
				_ = inbound.Close()
			}
		}
	}()

	childExe := stageProbeChild(t, guestHome, "reverseprobe.exe")
	report := filepath.Join(workDir, "child-report.txt")

	env := append(scrubEnvironment(guestHome),
		"NVX_PROBE=1",
		"NVX_REVERSE_CHILD=1",
		"NVX_REVERSE_SOCK="+sock,
		"NVX_REVERSE_REPORT="+report,
	)

	launched := make(chan error, 1)
	go func() {
		// scopeCaps ONLY. No internetClient: if the tunnel needed a network
		// capability it would reopen egress, and the whole point is to fix
		// inbound reachability without touching that.
		_, lerr := launchAppContainerProcess(
			childExe,
			[]string{"-test.run=TestReverseRelayReachesAServerInsideTheContainer"},
			env, workDir, sid, 0, scopeCaps,
		)
		launched <- lerr
	}()

	// The host side of the experiment: talk to the contained server as a browser
	// would, through the port the parent owns.
	deadline := time.Now().Add(60 * time.Second)
	var got string
	var lastErr error
	for time.Now().Before(deadline) {
		select {
		case lerr := <-launched:
			requireAppContainerLaunch(t, lerr)
			t.Fatalf("the contained child exited before serving; report: %s", readReport(report))
		default:
		}
		got, lastErr = askThroughTunnel(hostLn.Addr().String())
		if lastErr == nil && got != "" {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if lastErr != nil || !strings.Contains(got, "PONG-FROM-CONTAINER") {
		t.Fatalf("the host could not reach the contained server through the reverse tunnel: got %q, err %v\nchild report:\n%s",
			got, lastErr, readReport(report))
	}
	t.Log("RESULT: the host reached a server inside the AppContainer through an outward tunnel -- " +
		"a contained dev server is reachable without swapping the sandbox primitive.")

	// Let the child finish: it stops once it has served and the parent is gone,
	// and until it does it holds the socket inside the guest home open.
	_ = tunnelLn.Close()
	_ = os.Remove(sock)
	select {
	case lerr := <-launched:
		requireAppContainerLaunch(t, lerr)
	case <-time.After(20 * time.Second):
		t.Log("the contained child did not exit promptly; its guest home may be left behind")
	}

	// The other half of the claim, and the reason this is worth anything: the
	// container still has no way out on its own.
	rep := readReport(report)
	t.Logf("child report:\n%s", rep)
	if !strings.Contains(rep, "egress=BLOCKED") {
		t.Errorf("the contained side reached the internet directly; the tunnel must not come at the cost of "+
			"egress enforcement. Report:\n%s", rep)
	}
}

// runReverseRelayChild is the contained half: a server nobody outside can dial,
// plus the outward tunnels that make it reachable anyway.
func runReverseRelayChild() {
	report := os.Getenv("NVX_REVERSE_REPORT")
	var lines []string
	// Flushed on every line, not at exit. The child outlives the assertions by
	// design -- it holds the tunnels open -- so a report written on exit is a
	// report the test never sees, which made the egress check look like a
	// failure when it had simply not been written yet.
	say := func(f string, a ...any) {
		lines = append(lines, fmt.Sprintf(f, a...))
		if report != "" {
			_ = os.WriteFile(report, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
		}
	}

	// Stand-in for a dev server: bound inside the container, unreachable from
	// the host by any direct route.
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		say("inner-listen=FAILED %v", err)
		return
	}
	defer inner.Close()
	say("inner-listen=OK %s", inner.Addr())

	go func() {
		for {
			c, aerr := inner.Accept()
			if aerr != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 64)
				_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
				_, _ = c.Read(buf)
				_, _ = c.Write([]byte("PONG-FROM-CONTAINER"))
			}(c)
		}
	}()

	// Prove the container is still boxed in. If this succeeded, the tunnel would
	// be buying reachability at the price of the egress guarantee.
	if c, derr := net.DialTimeout("tcp", "1.1.1.1:443", 4*time.Second); derr == nil {
		_ = c.Close()
		say("egress=REACHED (the sandbox is not restricting outbound traffic)")
	} else {
		say("egress=BLOCKED %v", derr)
	}

	sock := os.Getenv("NVX_REVERSE_SOCK")
	say("tunnel-socket=%s", sock)

	// Keep a few tunnels parked so an inbound request never waits for a dial.
	//
	// The child must also end promptly once the test is done with it: it holds the
	// AF_UNIX socket inside the guest home, so a child that lingers makes the
	// parent's cleanup fail and leaves a temp directory behind on every run. It
	// therefore stops as soon as it has served a request and the parent has closed
	// the listener -- which the test does immediately after asserting.
	var served atomic.Bool
	var wg sync.WaitGroup
	// Comfortably longer than the host's attempt window below, and that ordering
	// is the whole point. At 30s against a 40s window the child gave up first
	// whenever a loaded machine made the AppContainer launch slow, which read as
	// "the contained child exited before serving" -- a flake that passed in
	// isolation every time and failed 1 run in 2 under the full suite, found once
	// CI started running every probe.
	hardStop := time.Now().Add(120 * time.Second)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			failedSince := time.Time{}
			for time.Now().Before(hardStop) {
				tun, derr := net.DialTimeout("unix", sock, 5*time.Second)
				if derr != nil {
					if served.Load() {
						if failedSince.IsZero() {
							failedSince = time.Now()
						} else if time.Since(failedSince) > time.Second {
							return // work done and the parent has gone away
						}
					}
					time.Sleep(200 * time.Millisecond)
					continue
				}
				failedSince = time.Time{}
				// Bridge this tunnel to the server that only exists in here.
				local, lerr := net.DialTimeout("tcp", inner.Addr().String(), 5*time.Second)
				if lerr != nil {
					_ = tun.Close()
					continue
				}
				spliceBoth(tun, local)
				served.Store(true)
			}
		}(i)
	}
	wg.Wait()
	say("tunnels=finished")
}

// spliceBoth joins two connections and returns once either direction ends.
func spliceBoth(a, b net.Conn) {
	defer a.Close()
	defer b.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(a, b); done <- struct{}{} }()
	go func() { _, _ = io.Copy(b, a); done <- struct{}{} }()
	<-done
}

func askThroughTunnel(addr string) (string, error) {
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return "", err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(8 * time.Second))
	if _, err := c.Write([]byte("GET / HTTP/1.0\r\n\r\n")); err != nil {
		return "", err
	}
	buf := make([]byte, 128)
	n, err := c.Read(buf)
	if n > 0 {
		return string(buf[:n]), nil
	}
	return "", err
}

func readReport(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "(no report written)"
	}
	return strings.TrimSpace(string(b))
}
