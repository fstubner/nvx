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

	nvxHome := t.TempDir()
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
	workDir := t.TempDir()
	scopeCaps, err := prepareAppContainerFilesystem(sid, guestHome, workDir)
	if err != nil {
		t.Fatalf("filesystem prep: %v", err)
	}

	sock := windowsEgressSocketPath(guestHome)
	if err := proxy.ListenUnix(sock); err != nil {
		t.Fatalf("ListenUnix: %v", err)
	}

	nvxExe := filepath.Join(guestHome, "nvx.exe")
	if out, berr := exec.Command("go", "build", "-o", nvxExe, ".").CombinedOutput(); berr != nil {
		t.Fatalf("build nvx: %v\n%s", berr, out)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	targetExe := filepath.Join(guestHome, "target.exe")
	if err := os.WriteFile(targetExe, data, 0o700); err != nil {
		t.Fatal(err)
	}

	read, write := makeTestPipe(t)
	defer syscall.CloseHandle(read)
	prevOut, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	const stdOutputHandle = uintptr(0xFFFFFFF5)
	procSetStdHandleTest.Call(stdOutputHandle, uintptr(write))

	env := append(scrubEnvironment(guestHome),
		"NVX_PROBE=1",
		"NVX_LOOPBACK_TARGET_CHILD=1",
		"NVX_HOST_SERVICE="+hostTarget,
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
	_, launchErr := launchAppContainerProcess(nvxExe, args, env, workDir, sid, 0, scopeCaps)

	procSetStdHandleTest.Call(stdOutputHandle, uintptr(prevOut))
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

	proxyAddr := strings.TrimPrefix(os.Getenv("HTTP_PROXY"), "http://")
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
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", host, host); err != nil {
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
