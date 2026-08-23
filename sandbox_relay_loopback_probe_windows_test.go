//go:build windows

package main

// Adversarial probe (NVX_PROBE=1): does the egress relay hand the contained
// process a route to services on the HOST's loopback?
//
// EgressProxy.allowed short-circuits every loopback destination to "permitted"
// (F38). That was harmless on Windows while the AppContainer could not reach the
// proxy at all -- it had no network capability and no loopback exemption, so it
// reached nothing. The relay changes the premise: the proxy now runs in the
// parent, OUTSIDE the container, and dials on the contained process's behalf. The
// parent can reach 127.0.0.1. So a contained install may now reach every service
// bound to the host's loopback -- a database, a dev server, another agent's MCP
// server, a metadata endpoint -- none of which appear in any allowlist.
//
// If so, closing internet egress opened loopback egress, and this probe is the
// evidence for a finding against my own change rather than an inherited one.

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRelayDoesNotExposeHostLoopbackServices(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run")
	}

	if os.Getenv("NVX_LOOPBACK_TARGET_CHILD") == "1" {
		runLoopbackTargetChild()
		return
	}

	// A stand-in for whatever the developer happens to be running: a service on
	// the host's loopback that no policy mentions.
	secretService, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer secretService.Close()
	go func() {
		for {
			c, aerr := secretService.Accept()
			if aerr != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				b := make([]byte, 64)
				n, _ := c.Read(b)
				if n > 0 {
					_, _ = c.Write([]byte("HOST-SERVICE-SECRET"))
				}
			}(c)
		}
	}()
	hostTarget := secretService.Addr().String()

	// An empty allowlist: nothing is permitted by policy at all.
	policy := DefaultPolicy()
	policy.Isolation.Network.PromptUnknown = false
	policy.Isolation.Network.AllowHosts = nil
	policy.Isolation.Network.DefaultAllow = nil
	policy.Isolation.Network.DefaultAllowSet = true

	nvxHome := tempDir(t)
	proxy, err := startEgressProxy(context.Background(), policy, Providers["node"], nvxHome)
	if err != nil {
		t.Fatalf("startEgressProxy: %v", err)
	}
	defer proxy.Close()

	const probeProfile = "nvx.sandbox.loopexposure"
	sid, err := ensureAppContainerSID(probeProfile)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	defer deleteAppContainerProfile(probeProfile)

	guestHome, err := os.MkdirTemp("", "nvxg")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(guestHome)
	workDir := tempDir(t)
	scopeCaps, err := prepareAppContainerFilesystem(sid, "", guestHome, workDir)
	if err != nil {
		t.Fatalf("filesystem prep: %v", err)
	}

	sock := windowsEgressSocketPath(guestHome)
	if err := proxy.ListenUnix(sock); err != nil {
		// GitHub-hosted Windows runners cannot create AF_UNIX sockets at all
		// ("An operation was attempted on something that is not a socket"). That
		// is an environment limitation, not a product failure, and every other
		// probe here skips on the equivalent -- this one used to fail the build
		// instead, which reports the runner's shape as a defect in nvx.
		t.Skipf("this host cannot create AF_UNIX sockets, so the relay cannot be exercised: %v", err)
	}

	nvxExe := filepath.Join(guestHome, "nvx.exe")
	if out, berr := exec.Command("go", "build", "-o", nvxExe, ".").CombinedOutput(); berr != nil {
		t.Fatalf("build nvx: %v\n%s", berr, out)
	}
	targetExe := stageProbeChild(t, guestHome, "target.exe")

	read, write := makeTestPipe(t)
	defer syscall.CloseHandle(read)
	prevOut, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	const stdOutputHandle = uintptr(0xFFFFFFF5)
	// Deferred, not only restored inline further down. This redirects the whole
	// TEST PROCESS's stdout, so anything that stops the inline restore being
	// reached takes every later line of test output with it: on 2026-08-23 the
	// launch below hung, and the log showed "=== RUN" for this test and then
	// nothing at all until the package was killed 11 minutes later, with no
	// indication of which test was stuck.
	defer procSetStdHandleTest.Call(stdOutputHandle, uintptr(prevOut))
	procSetStdHandleTest.Call(stdOutputHandle, uintptr(write))

	env := append(scrubEnvironment(guestHome),
		"NVX_PROBE=1",
		"NVX_LOOPBACK_TARGET_CHILD=1",
		"NVX_HOST_SERVICE="+hostTarget,
		// As applyProxyEnv would set it. Without the credential the child is
		// refused with 407 before the allowlist is consulted, so this probe would
		// report "not exposed" without ever testing the exposure it is named for.
		"HTTP_PROXY="+proxy.HTTProxyURL(),
	)
	args := []string{
		"__appcontainer-exec",
		"--guest-home=" + guestHome,
		"--work-dir=" + workDir,
		"--nvx-home=" + nvxHome,
		"--network-mode=proxy",
		"--egress-socket=" + sock,
		"--",
		targetExe,
		"-test.run=TestRelayDoesNotExposeHostLoopbackServices",
	}
	// Bounded, because an unbounded launch here killed the package.
	//
	// Every AppContainer probe calls launchAppContainerProcess and reads the
	// error to decide whether the host can host one; none of them consider that
	// it might not come back. On a GitHub-hosted runner this call normally
	// refuses in about a second, and on 2026-08-23 it hung instead, so `go test`
	// hit its 10-minute package timeout and reported FAIL for the whole suite --
	// a runner's shape presented as a product defect, which is what the skip
	// paths exist to avoid.
	//
	// A timeout is treated as the host being unable to host the probe, like the
	// refusals are, but says so distinctly: "hung" and "refused" are different
	// observations and collapsing them would hide a real regression in the launch
	// path behind an environment excuse.
	type launchResult struct{ err error }
	launched := make(chan launchResult, 1)
	go func() {
		_, err := launchAppContainerProcess(nvxExe, args, env, workDir, sid, 0, scopeCaps)
		launched <- launchResult{err}
	}()

	var launchErr error
	timedOut := false
	select {
	case r := <-launched:
		launchErr = r.err
	case <-time.After(90 * time.Second):
		timedOut = true
	}

	procSetStdHandleTest.Call(stdOutputHandle, uintptr(prevOut))
	if timedOut {
		syscall.CloseHandle(write)
		t.Skip("the AppContainer launch did not return within 90s; this host can neither host " +
			"the probe nor refuse it promptly, so the relay exposure cannot be exercised here")
	}
	syscall.CloseHandle(write)
	got := readProbeOutput(t, read)

	requireAppContainerLaunch(t, launchErr)
	t.Logf("child output:\n%s", got)

	// Direct is expected to fail: the container has no network capability, and
	// Windows blocks AppContainer loopback to a listener outside the container.
	// This is the control -- it establishes that the relay is the only route.
	if strings.Contains(got, "DIRECT=OK") {
		t.Log("note: the container reached the host service directly, so the relay is not the only route here")
	}

	if strings.Contains(got, "VIA_PROXY=REACHED") {
		t.Errorf("a contained process reached a host loopback service (%s) through the egress relay, "+
			"with an EMPTY allowlist.\nEgressProxy.allowed permits every loopback destination "+
			"unconditionally (F38), and the relay gives the container a route to the parent, which can "+
			"reach 127.0.0.1. Closing internet egress opened loopback egress.\n%s", hostTarget, got)
	} else if !strings.Contains(got, "VIA_PROXY=BLOCKED") {
		t.Errorf("inconclusive result:\n%s", got)
	}
}

func runLoopbackTargetChild() {
	host := os.Getenv("NVX_HOST_SERVICE")

	c, err := net.DialTimeout("tcp", host, 5*time.Second)
	if err != nil {
		fmt.Printf("DIRECT=DENIED %v\n", err)
	} else {
		_ = c.Close()
		fmt.Printf("DIRECT=OK\n")
	}

	// HTTP_PROXY carries this session's credential as userinfo; split it off so the
	// address is dialable and the credential can be sent as a header.
	proxyAddr := strings.TrimPrefix(os.Getenv("HTTP_PROXY"), "http://")
	cred := ""
	if at := strings.LastIndex(proxyAddr, "@"); at != -1 {
		cred, proxyAddr = proxyAddr[:at+1], proxyAddr[at+1:]
	}
	if proxyAddr == "" {
		fmt.Printf("VIA_PROXY=NO_PROXY_SET\n")
		os.Exit(0)
	}
	conn, err := net.DialTimeout("tcp", proxyAddr, 5*time.Second)
	if err != nil {
		fmt.Printf("VIA_PROXY=BLOCKED dial relay: %v\n", err)
		os.Exit(0)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	authHeader := ""
	if cred != "" {
		authHeader = "Proxy-Authorization: Basic " +
			base64.StdEncoding.EncodeToString([]byte(strings.TrimSuffix(cred, "@"))) + "\r\n"
	}
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n%s\r\n", host, host, authHeader); err != nil {
		fmt.Printf("VIA_PROXY=BLOCKED write: %v\n", err)
		os.Exit(0)
	}
	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil {
		fmt.Printf("VIA_PROXY=BLOCKED read: %v\n", err)
		os.Exit(0)
	}
	if !strings.Contains(status, "200") {
		fmt.Printf("VIA_PROXY=BLOCKED %s\n", strings.TrimSpace(status))
		os.Exit(0)
	}
	for {
		line, lerr := br.ReadString('\n')
		if lerr != nil || line == "\r\n" || line == "\n" {
			break
		}
	}
	// The tunnel is open; prove data actually comes back from the host service.
	if _, err := conn.Write([]byte("GET")); err != nil {
		fmt.Printf("VIA_PROXY=BLOCKED tunnel write: %v\n", err)
		os.Exit(0)
	}
	buf := make([]byte, 64)
	n, _ := br.Read(buf)
	fmt.Printf("VIA_PROXY=REACHED payload=%q\n", string(buf[:n]))
	os.Exit(0)
}
