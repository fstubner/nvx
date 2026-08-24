//go:build windows

package main

// End-to-end proof (NVX_PROBE=1) that Windows egress is now ENFORCED rather than
// requested: a real AppContainer, launched through nvx's own in-container
// supervisor, reaching an allowlisted host only via the relay and reaching nothing
// else at all.
//
// The three assertions correspond to the three ways this could be fake:
//
//	DIRECT         must fail    -- if the container can dial the target itself,
//	                               the relay is decoration and any package can
//	                               skip it.
//	PROXY_ALLOWED  must be 200  -- if allowlisted traffic does not flow, the
//	                               sandbox is merely broken; "nothing gets out"
//	                               is easy to achieve and useless.
//	PROXY_BLOCKED  must be 403  -- if everything flows once the relay is up, the
//	                               allowlist is not being consulted.
//
// Only the last of those was covered before, and by a test that never ran on
// Windows. F27 is the record of what that costs: a sandbox that denies everything
// passes a deny-only test perfectly.

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

// relayTargetExitCode is returned by the contained target and asserted by the
// parent, which proves the exit status survives both hops -- target to supervisor,
// supervisor to nvx. A sandbox that always reported 0 would hide every failure.
const relayTargetExitCode = 7

func TestAppContainerReachesOnlyAllowlistedHostsThroughTheRelay(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (creates a throwaway AppContainer profile and builds nvx)")
	}

	if os.Getenv("NVX_RELAY_TARGET_CHILD") == "1" {
		runRelayTargetChild()
		return
	}

	// A stand-in "remote host" on a non-loopback address, so the allowlist decision
	// is real: EgressProxy permits every loopback destination unconditionally.
	remote := nonLoopbackListener(t)
	defer remote.Close()
	go func() {
		for {
			c, err := remote.Accept()
			if err != nil {
				return
			}
			// Echo with a marker rather than closing immediately. A CONNECT that
			// returns 200 and then carries nothing is a tunnel in name only, and
			// asserting only on the status line would call that a pass.
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 64)
				n, rerr := c.Read(buf)
				if n == 0 && rerr != nil {
					return
				}
				_, _ = c.Write(append([]byte("echo:"), buf[:n]...))
			}(c)
		}
	}()
	allowedTarget := remote.Addr().String()
	host, _, _ := net.SplitHostPort(allowedTarget)
	blockedTarget := net.JoinHostPort(host, "9")

	policy := DefaultPolicy()
	policy.Isolation.Network.PromptUnknown = false // deny unknown without prompting
	policy.Isolation.Network.AllowHosts = []string{allowedTarget}

	nvxHome := tempDir(t)
	proxy, err := startEgressProxy(context.Background(), policy, Providers["node"], nvxHome)
	if err != nil {
		t.Fatalf("startEgressProxy: %v", err)
	}
	defer proxy.Close()

	const probeProfile = "nvx.sandbox.relaye2e"
	sid, err := ensureAppContainerSID(probeProfile)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	defer deleteAppContainerProfile(probeProfile)

	// Short path: the guest home holds the AF_UNIX socket, which is capped at 108
	// bytes, and tempDir(t) spends most of that on this test's name.
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
	if !egressSocketPathFits(sock) {
		t.Fatalf("socket path is %d bytes, over the AF_UNIX limit: %s", len(sock), sock)
	}
	if err := proxy.ListenUnix(sock); err != nil {
		// GitHub-hosted Windows runners cannot create AF_UNIX sockets at all
		// ("An operation was attempted on something that is not a socket"). That
		// is an environment limitation, not a product failure, and every other
		// probe here skips on the equivalent -- this one used to fail the build
		// instead, which reports the runner's shape as a defect in nvx.
		t.Skipf("this host cannot create AF_UNIX sockets, so the relay cannot be exercised: %v", err)
	}

	// The supervisor is nvx itself, so this needs a real nvx binary rather than the
	// test binary: __appcontainer-exec is a main() subcommand.
	nvxExe := filepath.Join(guestHome, "nvx.exe")
	build := exec.Command("go", "build", "-o", nvxExe, ".")
	if out, berr := build.CombinedOutput(); berr != nil {
		t.Fatalf("build nvx: %v\n%s", berr, out)
	}

	targetExe := stageProbeChild(t, guestHome, "target.exe")

	read, write := makeTestPipe(t)
	defer syscall.CloseHandle(read)
	prevOut, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	const stdOutputHandle = uintptr(0xFFFFFFF5)
	procSetStdHandleTest.Call(stdOutputHandle, uintptr(write))

	env := append(scrubEnvironment(guestHome),
		"NVX_PROBE=1",
		"NVX_RELAY_TARGET_CHILD=1",
		"NVX_RELAY_ALLOWED="+allowedTarget,
		"NVX_RELAY_BLOCKED="+blockedTarget,
		// What applyProxyEnv puts here on a real launch. The supervisor rewrites
		// the address to its in-container relay but carries the credential across,
		// so without this the target reaches the relay anonymously and every
		// destination comes back 407 -- which is a sandbox that blocks everything,
		// the exact false pass this test exists to rule out.
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
		"-test.run=TestAppContainerReachesOnlyAllowlistedHostsThroughTheRelay",
	}
	exitCode, launchErr := launchAppContainerProcess(
		nvxExe, args, env, workDir, sid, 0,
		scopeCaps, // no internetClient: the relay is the only route out
	)

	procSetStdHandleTest.Call(stdOutputHandle, uintptr(prevOut))
	syscall.CloseHandle(write)
	got := readProbeOutput(t, read)

	requireAppContainerLaunch(t, launchErr)
	t.Logf("supervisor exit=%d output:\n%s", exitCode, got)

	// 1. No bypass: the container cannot reach the target on its own.
	if strings.Contains(got, "DIRECT=OK") {
		t.Errorf("the contained process reached %s directly, so the relay can be bypassed and the allowlist enforces nothing", allowedTarget)
	} else if !strings.Contains(got, "DIRECT=DENIED") {
		t.Errorf("inconclusive DIRECT result in:\n%s", got)
	}

	// 2. Allowlisted traffic reaches its destination through the relay...
	if !strings.Contains(got, "PROXY_ALLOWED=200") {
		t.Errorf("allowlisted %s did not tunnel through the relay; the sandbox blocks everything, which is not the same as enforcing an allowlist:\n%s", allowedTarget, got)
	}
	// ...and the tunnel actually carries bytes once established. A CONNECT that
	// returns 200 and then stalls looks identical to success at the status line,
	// and is what a real request would hang on.
	if !strings.Contains(got, "PROXY_PAYLOAD=echo:PING") {
		t.Errorf("the established tunnel did not carry data end to end; a real request would hang here:\n%s", got)
	}

	// 3. Non-allowlisted traffic is refused by the proxy at the far end.
	if !strings.Contains(got, "PROXY_BLOCKED=403") {
		t.Errorf("non-allowlisted %s was not refused; the allowlist is not being consulted:\n%s", blockedTarget, got)
	}

	// 3b. And a client without this session's credential is refused before the
	// allowlist is even consulted. Every sandbox on the machine shares one package
	// identity and therefore one loopback namespace, so reaching this relay is not
	// evidence of being entitled to its allowlist.
	//
	// Two refusal shapes, not one -- the same lesson requireAppContainerLaunch
	// records for CreateProcess. The proxy answers 407, and under load it
	// sometimes closes the connection instead, which arrives here as
	// READ_FAILED:EOF. Both are refusals and the closed connection is if anything
	// the stricter of the two, but only 407 was accepted, so this test failed
	// about one full-suite run in two while the behaviour it guards was correct.
	//
	// What must NOT pass is 200, or a tunnel that carried data. Those are the
	// outcomes that would mean an anonymous client borrowed the allowlist, and
	// they are still failures.
	anonRefused := strings.Contains(got, "PROXY_ANONYMOUS=407") ||
		strings.Contains(got, "PROXY_ANONYMOUS=READ_FAILED") ||
		strings.Contains(got, "PROXY_ANONYMOUS=WRITE_FAILED")
	if !anonRefused {
		t.Errorf("a client with no credential was not refused; another session could borrow this allowlist:\n%s", got)
	}

	// 4. The exit status survived target -> supervisor -> nvx.
	if exitCode != relayTargetExitCode {
		t.Errorf("exit code = %d, want %d: a failing command inside the sandbox would be reported as success", exitCode, relayTargetExitCode)
	}
}

// runRelayTargetChild is the contained target: the process a package's install
// script stands in for. It reports what it can reach and how, then exits with a
// distinctive status.
func runRelayTargetChild() {
	allowed := os.Getenv("NVX_RELAY_ALLOWED")
	blocked := os.Getenv("NVX_RELAY_BLOCKED")

	// Direct, ignoring HTTP_PROXY entirely -- what a malicious package would do.
	c, err := net.DialTimeout("tcp", allowed, 5*time.Second)
	if err != nil {
		fmt.Printf("DIRECT=DENIED %v\n", err)
	} else {
		_ = c.Close()
		fmt.Printf("DIRECT=OK\n")
	}

	// HTTP_PROXY carries this session's credential as userinfo, so split it the way
	// any HTTP client does: the address is dialled, the credential is sent as a
	// Proxy-Authorization header. Stripping only the scheme left "nvx:token@host"
	// as the dial target, which fails.
	proxyAddr := strings.TrimPrefix(os.Getenv("HTTP_PROXY"), "http://")
	cred := ""
	if at := strings.LastIndex(proxyAddr, "@"); at != -1 {
		cred, proxyAddr = proxyAddr[:at+1], proxyAddr[at+1:]
	}
	if proxyAddr == "" {
		fmt.Printf("PROXY=MISSING\n")
		os.Exit(relayTargetExitCode)
	}
	status, payload := connectAndEcho(proxyAddr, allowed, cred)
	fmt.Printf("PROXY_ALLOWED=%s\n", status)
	fmt.Printf("PROXY_PAYLOAD=%s\n", payload)
	blockedStatus, _ := connectAndEcho(proxyAddr, blocked, cred)
	fmt.Printf("PROXY_BLOCKED=%s\n", blockedStatus)
	// A client inside the same container that does NOT have the credential must be
	// refused: the relay is reachable from any sandbox on the machine.
	anonStatus, _ := connectAndEcho(proxyAddr, allowed, "")
	fmt.Printf("PROXY_ANONYMOUS=%s\n", anonStatus)
	os.Exit(relayTargetExitCode)
}

// connectAndEcho issues an HTTP CONNECT for target through proxyAddr, then sends a
// probe through the established tunnel and reads the reply. It returns the status
// code and what came back.
//
// The second half is the part that matters: a relay can complete the CONNECT
// handshake and still fail to move bytes afterwards, and every layer here --
// contained loopback TCP, the AF_UNIX hop, the proxy's own splice -- has to keep
// forwarding for a real request to work.
func connectAndEcho(proxyAddr, target, cred string) (status, payload string) {
	conn, err := net.DialTimeout("tcp", proxyAddr, 5*time.Second)
	if err != nil {
		return "DIAL_FAILED:" + err.Error(), ""
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	authHeader := ""
	if cred != "" {
		authHeader = "Proxy-Authorization: Basic " +
			base64.StdEncoding.EncodeToString([]byte(strings.TrimSuffix(cred, "@"))) + "\r\n"
	}
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n%s\r\n", target, target, authHeader); err != nil {
		return "WRITE_FAILED:" + err.Error(), ""
	}
	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil {
		return "READ_FAILED:" + err.Error(), ""
	}
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) < 2 {
		return "UNPARSEABLE:" + strings.TrimSpace(line), ""
	}
	status = fields[1]
	if status != "200" {
		return status, "NONE"
	}
	// Drain the rest of the response headers, then use the tunnel.
	for {
		l, lerr := br.ReadString('\n')
		if lerr != nil {
			return status, "HEADER_READ_FAILED:" + lerr.Error()
		}
		if l == "\r\n" || l == "\n" {
			break
		}
	}
	if _, err := conn.Write([]byte("PING")); err != nil {
		return status, "TUNNEL_WRITE_FAILED:" + err.Error()
	}
	buf := make([]byte, 64)
	n, rerr := br.Read(buf)
	if n == 0 && rerr != nil {
		return status, "TUNNEL_READ_FAILED:" + rerr.Error()
	}
	return status, string(buf[:n])
}
