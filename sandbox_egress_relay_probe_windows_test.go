//go:build windows

package main

// Opt-in probe (NVX_PROBE=1) that decides whether Windows can have REAL allowlisted
// egress -- the kind a malicious package cannot opt out of -- rather than the
// cooperative HTTP_PROXY hint it has today.
//
// Today's default grants the AppContainer the internetClient capability and lets it
// connect straight out. HTTP_PROXY is set, but nothing forces a process to honour
// it: `net.connect()` to any host still works, so the allowlist is advice, not
// enforcement. That is the gap README.md's "Known limitations" now admits to.
//
// The proposed fix mirrors Linux: DON'T grant internetClient, run a supervisor
// inside the container that relays a loopback TCP port to the parent's AF_UNIX
// socket, and point HTTP_PROXY at the relay. Then the allowlist is enforced by the
// OS -- the only route out of the container is through the parent's proxy.
//
// Three things must all hold for that to work, and all three must hold with NO
// capability SIDs granted, because internetClient is exactly what we are removing:
//
//	1. DIRECT_*  must be DENIED  -- otherwise the target bypasses the relay and
//	                                the whole design buys nothing.
//	2. SELFLOOP  must be OK      -- the supervisor's loopback listener has to be
//	                                dialable by its own child.
//	3. AFUNIX    must be OK      -- the relay's only route to the parent proxy.
//
// A previous probe measured (3) with no capabilities and (2) only WITH
// internetClient granted, which does not answer the question that matters. This
// measures all three together, under the capability set the real design would use.

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestAppContainerEgressPrimitivesWithoutInternetClient(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (creates a throwaway AppContainer profile)")
	}

	if os.Getenv("NVX_EGRESS_PROBE_CHILD") == "1" {
		runEgressProbeChild(os.Getenv("NVX_PROBE_SOCK"))
		return
	}

	const probeProfile = "nvx.sandbox.egressprobe"
	sid, err := ensureAppContainerSID(probeProfile)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	defer deleteAppContainerProfile(probeProfile)

	// Deliberately NOT t.TempDir(): it embeds the test name, and an AF_UNIX path is
	// capped at 108 bytes (sun_path) on Windows as on Unix. With this test's name in
	// it the socket path came to 116 and bind failed with "invalid argument", which
	// looks exactly like a capability denial and is not one. The real guest home has
	// the same ceiling -- see egressSocketPathFits.
	guestHome, err := os.MkdirTemp("", "nvxg")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(guestHome)
	workDir := t.TempDir()
	scopeCaps, err := prepareAppContainerFilesystem(sid, "", guestHome, workDir)
	if err != nil {
		t.Fatalf("filesystem prep: %v", err)
	}

	// Stand-in for the parent's egress proxy: an AF_UNIX socket in the guest home,
	// which the container already has full access to.
	sock := filepath.Join(guestHome, "egress.sock")
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("this host cannot create AF_UNIX sockets: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				b := make([]byte, 32)
				_, _ = c.Read(b)
				_, _ = c.Write([]byte("PONG"))
			}(c)
		}
	}()

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	childExe := filepath.Join(guestHome, "egressprobe.exe")
	if err := os.WriteFile(childExe, data, 0o700); err != nil {
		t.Fatal(err)
	}

	read, write := makeTestPipe(t)
	defer syscall.CloseHandle(read)
	prevOut, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	const stdOutputHandle = uintptr(0xFFFFFFF5)
	procSetStdHandleTest.Call(stdOutputHandle, uintptr(write))

	env := append(scrubEnvironment(guestHome),
		"NVX_PROBE=1",
		"NVX_EGRESS_PROBE_CHILD=1",
		"NVX_PROBE_SOCK="+sock,
	)
	exitCode, launchErr := launchAppContainerProcess(
		childExe,
		[]string{"-test.run=TestAppContainerEgressPrimitivesWithoutInternetClient"},
		env, workDir, sid, 0,
		scopeCaps, // the whole point: NO internetClient
	)

	procSetStdHandleTest.Call(stdOutputHandle, uintptr(prevOut))
	syscall.CloseHandle(write)
	got := readProbeOutput(t, read)

	requireAppContainerLaunch(t, launchErr)
	t.Logf("child exit=%d output:\n%s", exitCode, got)

	// 1. Without internetClient the container must not reach the internet at all.
	//    If either of these succeeds, an in-container relay enforces nothing.
	for _, key := range []string{"DIRECT_IP", "DIRECT_DNS"} {
		switch {
		case strings.Contains(got, key+"=OK"):
			t.Errorf("%s: the container reached the internet with NO capability granted; "+
				"removing internetClient does not actually cut off direct egress, so a relay could be bypassed", key)
		case strings.Contains(got, key+"=DENIED"):
		default:
			t.Errorf("inconclusive %s result in:\n%s", key, got)
		}
	}

	// 2. The supervisor must be able to host a loopback listener its own child dials.
	if !strings.Contains(got, "SELFLOOP=OK") {
		t.Errorf("intra-container loopback does not work without internetClient, so no in-container relay is possible:\n%s", got)
	}

	// 3. That relay's only way out is the parent's AF_UNIX socket.
	if !strings.Contains(got, "AFUNIX=OK") {
		t.Errorf("the container cannot reach the parent's AF_UNIX socket without internetClient, so the relay has no upstream:\n%s", got)
	}
}

// runEgressProbeChild is the contained half: it reports on each primitive the
// relay design depends on, one per line, and never fails the run -- the parent
// decides what the results mean.
func runEgressProbeChild(sock string) {
	report := func(label string, err error) {
		if err != nil {
			fmt.Printf("%s=DENIED %v\n", label, err)
			return
		}
		fmt.Printf("%s=OK\n", label)
	}

	// Direct egress by IP: no DNS involved, so a failure here is the network
	// restriction rather than name resolution.
	c, err := net.DialTimeout("tcp", "1.1.1.1:443", 5*time.Second)
	if err == nil {
		_ = c.Close()
	}
	report("DIRECT_IP", err)

	// Direct egress by name: what npm actually does.
	c, err = net.DialTimeout("tcp", "registry.npmjs.org:443", 5*time.Second)
	if err == nil {
		_ = c.Close()
	}
	report("DIRECT_DNS", err)

	report("SELFLOOP", probeSelfLoopback())
	report("AFUNIX", probeParentUnixSocket(sock))
	os.Exit(0)
}

// probeSelfLoopback listens on loopback and dials that listener from the same
// process, which is what an in-container relay requires.
func probeSelfLoopback() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer ln.Close()

	go func() {
		c, aerr := ln.Accept()
		if aerr != nil {
			return
		}
		defer c.Close()
		_, _ = c.Write([]byte("SELF"))
	}()

	c, err := net.DialTimeout("tcp", ln.Addr().String(), 5*time.Second)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer c.Close()
	b := make([]byte, 16)
	n, rerr := c.Read(b)
	if n == 0 && rerr != nil {
		return fmt.Errorf("read: %w", rerr)
	}
	if string(b[:n]) != "SELF" {
		return fmt.Errorf("unexpected payload %q", string(b[:n]))
	}
	return nil
}

func probeParentUnixSocket(sock string) error {
	if sock == "" {
		return fmt.Errorf("no socket path supplied")
	}
	c, err := net.DialTimeout("unix", sock, 5*time.Second)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("PING")); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	b := make([]byte, 16)
	n, rerr := c.Read(b)
	if n == 0 && rerr != nil {
		return fmt.Errorf("read: %w", rerr)
	}
	if string(b[:n]) != "PONG" {
		return fmt.Errorf("unexpected payload %q", string(b[:n]))
	}
	return nil
}

// readProbeOutput drains the pipe until the child closes it. readWithTimeout does
// a single 256-byte read, which truncates a multi-line report and would silently
// turn a missing line into an "inconclusive" verdict. The timeout is generous
// because the child deliberately waits on two connect attempts that should fail.
func readProbeOutput(t *testing.T, read syscall.Handle) string {
	t.Helper()
	done := make(chan string, 1)
	go func() {
		f := os.NewFile(uintptr(read), "pipe")
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := f.Read(buf)
			if n > 0 {
				sb.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		done <- sb.String()
	}()
	select {
	case s := <-done:
		return s
	case <-time.After(60 * time.Second):
		t.Fatal("timed out reading the probe child's output")
		return ""
	}
}
