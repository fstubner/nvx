//go:build windows

package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestExposedPortIsReachableFromTheHost drives the whole published-port path
// through the real binary: a server started inside the AppContainer, reached
// from the host, with containment unchanged.
//
// It exists because none of the parts can be trusted from their own unit tests.
// The mapping rules are pure logic and tested as such; whether Windows lets a
// tunnel dialled outward carry an inbound request is not something a unit test
// can answer, and it was the open question the whole feature rested on.
//
// The negative half is asserted in the same run, deliberately. The tunnel is the
// one mechanism that makes something inside the sandbox reachable, so "did it
// also make the sandbox able to reach out" has to be answered every time rather
// than reasoned about once.
func TestExposedPortIsReachableFromTheHost(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (builds nvx and launches a real AppContainer)")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed; the contained server needs it")
	}

	proj := tempDir(t)
	nvxExe := filepath.Join(tempDir(t), "nvx.exe")
	if out, err := exec.Command("go", "build", "-o", nvxExe, ".").CombinedOutput(); err != nil {
		t.Fatalf("build nvx: %v\n%s", err, out)
	}

	// A port for the server INSIDE the container, and a different one for the
	// host. They cannot match: the container shares this network stack, so one
	// number cannot hold both ends.
	insidePort := freeTCPPort(t)
	hostPort := freeTCPPort(t)
	if insidePort == hostPort {
		t.Skip("the two probe ports collided; nothing to test")
	}

	server := fmt.Sprintf(`
const http = require('http');
const https = require('https');
// Answer the host, and report whether the sandbox can still be escaped outward.
let egress = 'PENDING';
const req = https.get('https://example.com', () => { egress = 'ALLOWED'; });
req.on('error', () => { egress = 'DENIED'; });
req.setTimeout(8000, () => { req.destroy(); egress = 'TIMEOUT'; });
http.createServer((q, s) => s.end('SERVED-FROM-CONTAINER egress=' + egress))
    .listen(%d, '127.0.0.1', () => console.log('listening'));
`, insidePort)
	if err := os.WriteFile(filepath.Join(proj, "server.js"), []byte(server), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(nvxExe,
		"--expose", fmt.Sprintf("%d:%d", insidePort, hostPort),
		"-y", "--strict", "shim", "node", "server.js")
	cmd.Dir = proj
	// Synchronised: os/exec writes this from its copier goroutines while the poll
	// loop below reads it, which is a data race with a plain strings.Builder.
	var out syncBuffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start nvx: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	// The sandbox takes a few seconds to stand up before the server even starts,
	// so poll rather than sleeping on a guess.
	var body string
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), "AppContainer launch failed") {
			t.Skipf("this host cannot create AppContainer children:\n%s", out.String())
		}
		if b, err := httpGetBody(fmt.Sprintf("http://127.0.0.1:%d/", hostPort)); err == nil {
			body = b
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if !strings.Contains(body, "SERVED-FROM-CONTAINER") {
		// Two failures wear this message and they need different fixes, so say
		// which one happened. The server prints "listening" from inside the
		// container the moment it binds: with that line, the container is up and
		// the published-port tunnel is what did not carry the connection; without
		// it, the sandbox never got as far as running the script and the tunnel
		// was never exercised.
		//
		// An independent audit hit this on 2026-09-03 and could only report the
		// ambiguous version. It did not reproduce in nine attempts here -- two
		// NVX_HOME lengths, with and without -race -- where the probe takes 7 to
		// 24 seconds against the 90 below, so a slower or busier machine running
		// out of budget is the likeliest of the two and the message could not
		// distinguish them.
		reached := "the container never reported its server listening, so the sandbox did not " +
			"finish standing up within 90s; this is a budget or startup problem, not the tunnel"
		if strings.Contains(out.String(), "listening") {
			reached = "the container's server WAS listening, so the published-port tunnel did not " +
				"carry the connection; this is --expose itself"
		}
		t.Fatalf("the host could not reach the contained server on 127.0.0.1:%d.\n%s\nnvx output:\n%s",
			hostPort, reached, out.String())
	}
	// Windows refuses connections INTO an AppContainer, so reaching it at all is
	// the claim; a reply proves the tunnel carried both directions.
	t.Logf("host reached the contained server: %q", body)

	// The load-bearing half. If publishing a port had granted the container a
	// network capability, this would say ALLOWED and the feature would be buying
	// reachability with the egress guarantee.
	if strings.Contains(body, "egress=ALLOWED") {
		t.Errorf("publishing a port let the contained process reach the internet: %q", body)
	}
}

// freeTCPPort asks the OS for a port and gives it straight back. Racy in
// principle; in practice the window is microseconds and the alternative is
// hardcoding numbers that collide with whatever the developer is running.
func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func httpGetBody(url string) (string, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}
