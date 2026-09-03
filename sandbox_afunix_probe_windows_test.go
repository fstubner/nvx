//go:build windows

package main

// Opt-in experiment (NVX_PROBE=1) for F2: can a process inside an AppContainer
// reach an AF_UNIX socket owned by the unsandboxed parent?
//
// Windows blocks AppContainer -> TCP loopback unless an administrator adds a
// loopback exemption, which is why nvx currently grants the sandbox
// internetClient and lets it connect DIRECTLY, bypassing the egress allowlist --
// while README.md and enforcement-matrix.md claim Windows egress is allowlisted.
//
// AF_UNIX on Windows (afunix.sys, Windows 10 1803+) is a filesystem object rather
// than a TCP/IP endpoint. If the loopback restriction does not cover it, the exact
// relay built for Linux would give Windows real allowlisted egress with no
// elevation -- making the documentation true instead of weakening it. If it is
// blocked, the honest fix is to correct the docs.
//
// This settles which.

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestAppContainerCanReachAFUnixSocket(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (creates a throwaway AppContainer profile)")
	}

	// Child role: dial the socket and report.
	if sock := os.Getenv("NVX_AFUNIX_CLIENT"); sock != "" {
		c, err := net.DialTimeout("unix", sock, 5*time.Second)
		if err != nil {
			fmt.Printf("afunix=DENIED %v\n", err)
			os.Exit(0)
		}
		defer c.Close()
		_, _ = c.Write([]byte("PING"))
		buf := make([]byte, 64)
		n, rerr := c.Read(buf)
		if rerr != nil && rerr != io.EOF {
			fmt.Printf("afunix=CONNECTED_BUT_READ_FAILED %v\n", rerr)
			os.Exit(0)
		}
		fmt.Printf("afunix=REACHED %s\n", string(buf[:n]))
		os.Exit(0)
	}

	const probeProfile = "nvx.sandbox.afunixprobe"
	sid, err := ensureAppContainerSID(probeProfile)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	defer deleteAppContainerProfile(probeProfile)

	guestHome := tempDir(t)
	workDir := tempDir(t)
	scopeCaps, _, err := prepareAppContainerFilesystem(sid, "", guestHome, workDir)
	if err != nil {
		t.Fatalf("filesystem prep: %v", err)
	}

	// The socket lives in the guest home, which the container already has full
	// access to -- exactly where the Linux implementation puts it.
	sock := filepath.Join(guestHome, "egress-probe.sock")
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("this host cannot create AF_UNIX sockets: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
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

	// Run the test binary itself as the contained client. Copy it into the guest
	// home so the container can execute it without granting a temp-dir path.
	childExe := stageProbeChild(t, guestHome, "probe.exe")

	read, write := makeTestPipe(t)
	defer syscall.CloseHandle(read)
	prevOut, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	const stdOutputHandle = uintptr(0xFFFFFFF5)
	procSetStdHandleTest.Call(stdOutputHandle, uintptr(write))

	env := append(scrubEnvironment(guestHome),
		"NVX_PROBE=1",
		"NVX_AFUNIX_CLIENT="+sock,
	)
	exitCode, launchErr := launchAppContainerProcess(
		childExe,
		[]string{"-test.run=TestAppContainerCanReachAFUnixSocket"},
		env, workDir, sid, 0,
		scopeCaps, // deliberately NO internetClient: this is about reaching the parent, not the internet
	)

	procSetStdHandleTest.Call(stdOutputHandle, uintptr(prevOut))
	syscall.CloseHandle(write)
	out := readWithTimeout(t, read)

	t.Logf("child exit=%d err=%v", exitCode, launchErr)
	t.Logf("child output: %q", out)

	requireAppContainerLaunch(t, launchErr)

	switch {
	case contains(out, "afunix=REACHED"):
		t.Log("RESULT: AF_UNIX CROSSES the AppContainer boundary -- the Linux relay design is viable on Windows, and egress could be allowlisted by default without elevation.")
	case contains(out, "afunix=DENIED"):
		t.Log("RESULT: AF_UNIX is BLOCKED for AppContainers -- allowlisted egress needs the elevated loopback exemption, so the documentation must be corrected instead.")
	default:
		t.Logf("RESULT: inconclusive; output was %q", out)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}

// TestAppContainerIntraContainerLoopback decides whether an in-container relay is
// possible on Windows at all. AF_UNIX reaching the parent is necessary but not
// sufficient: unlike Linux, nvx runs no supervisor inside the AppContainer, so a
// relay would have to listen on loopback INSIDE the container and be dialled by the
// target. Windows blocks AppContainer loopback to outside processes; whether it
// blocks a container reaching its own listener is what decides the design.
func TestAppContainerIntraContainerLoopback(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run")
	}

	// Child role B: can a contained process use TCP loopback to reach a listener it
	// started ITSELF, inside the same AppContainer? An in-container relay would need
	// that, and it is a different question from reaching an outside process over
	// loopback (which Windows blocks without an admin exemption).
	if os.Getenv("NVX_LOOPBACK_SELFTEST") == "1" {
		ln, lerr := net.Listen("tcp", "127.0.0.1:0")
		if lerr != nil {
			fmt.Printf("selfloopback=LISTEN_DENIED %v\n", lerr)
			os.Exit(0)
		}
		defer ln.Close()
		go func() {
			c, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			_, _ = c.Write([]byte("SELF"))
			_ = c.Close()
		}()
		c, derr := net.DialTimeout("tcp", ln.Addr().String(), 4*time.Second)
		if derr != nil {
			fmt.Printf("selfloopback=DIAL_DENIED %v\n", derr)
			os.Exit(0)
		}
		defer c.Close()
		b := make([]byte, 16)
		n, _ := c.Read(b)
		fmt.Printf("selfloopback=REACHED %s\n", string(b[:n]))
		os.Exit(0)
	}

	const probeProfile = "nvx.sandbox.loopprobe"
	sid, err := ensureAppContainerSID(probeProfile)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	defer deleteAppContainerProfile(probeProfile)

	guestHome := tempDir(t)
	workDir := tempDir(t)
	scopeCaps, _, err := prepareAppContainerFilesystem(sid, "", guestHome, workDir)
	if err != nil {
		t.Fatalf("filesystem prep: %v", err)
	}

	childExe := stageProbeChild(t, guestHome, "loopprobe.exe")

	read, write := makeTestPipe(t)
	defer syscall.CloseHandle(read)
	prevOut, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	const stdOutputHandle = uintptr(0xFFFFFFF5)
	procSetStdHandleTest.Call(stdOutputHandle, uintptr(write))

	env := append(scrubEnvironment(guestHome), "NVX_PROBE=1", "NVX_LOOPBACK_SELFTEST=1")
	exitCode, launchErr := launchAppContainerProcess(
		childExe,
		[]string{"-test.run=TestAppContainerIntraContainerLoopback"},
		env, workDir, sid, 0,
		append(scopeCaps, capabilityInternetClientSID),
	)

	procSetStdHandleTest.Call(stdOutputHandle, uintptr(prevOut))
	syscall.CloseHandle(write)
	out := readWithTimeout(t, read)

	t.Logf("child exit=%d err=%v", exitCode, launchErr)
	t.Logf("child output: %q", out)
	requireAppContainerLaunch(t, launchErr)

	switch {
	case contains(out, "selfloopback=REACHED"):
		t.Log("RESULT: intra-container loopback WORKS -- an in-container relay is viable, so Windows could get allowlisted egress by default with no elevation.")
	default:
		t.Log("RESULT: intra-container loopback is BLOCKED -- no in-container relay is possible, so allowlisted egress on Windows needs the elevated loopback exemption.")
	}
}
