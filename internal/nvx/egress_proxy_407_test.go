package nvx

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// git sends its first CONNECT with no credential and waits for a 407 to choose an
// authentication method (http.proxyAuthMethod defaults to anyauth). The proxy
// answered with a 407 that had no Content-Length and then closed the connection,
// and libcurl gave up with "Proxy CONNECT aborted" -- for every host, the
// allowlisted ones included.
//
// Measured on Linux with git 2.39.5 (libcurl 7.88.1), run by a contained Node
// process. A 407 with Content-Length: 0 alone did not help. Adding
// Connection: close did, because libcurl then reconnects with the credential
// instead of reusing a connection that is about to end.

// The response is complete, and says the connection ends.
func TestAnUnauthenticatedConnectGetsACompleteFinalResponse(t *testing.T) {
	p := proxyAllowing(t, "allowed.example.test:443")

	c, err := net.DialTimeout("tcp", p.httpAddr, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(c, "CONNECT allowed.example.test:443 HTTP/1.1\r\nHost: allowed.example.test:443\r\n\r\n")

	br := bufio.NewReader(c)
	tp := textproto.NewReader(br)
	status, err := tp.ReadLine()
	if err != nil {
		t.Fatalf("read status line: %v", err)
	}
	if !strings.Contains(status, " 407 ") {
		t.Fatalf("status line = %q, want a 407", status)
	}
	hdr, err := tp.ReadMIMEHeader()
	if err != nil {
		t.Fatalf("read headers: %v", err)
	}
	if hdr.Get("Proxy-Authenticate") == "" {
		t.Error("the 407 does not say how to authenticate")
	}
	if got := hdr.Get("Content-Length"); got != "0" {
		t.Errorf("Content-Length = %q, want 0: without it libcurl cannot tell where the response ends", got)
	}
	if !strings.EqualFold(hdr.Get("Connection"), "close") {
		t.Errorf("Connection = %q, want close: libcurl then reconnects with the credential, and gives up when the connection ends without having been told it would", hdr.Get("Connection"))
	}
	// And it does end.
	if _, err := br.ReadByte(); err != io.EOF {
		t.Errorf("the connection was still open after the 407: %v", err)
	}
}

// A real git, which is the client that sends the credential-less CONNECT first.
// The target is a plain listener, so once the tunnel opens git's TLS handshake
// fails. What matters is that it got that far: the second CONNECT carried the
// credential and the proxy admitted it.
func TestGitGetsPastTheFirst407(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close() // not TLS: the handshake fails, after the tunnel is open
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())

	// The proxy answers the name from a table, so no DNS is involved.
	resolveAs(t, map[string]string{"allowed.example.test": "127.0.0.1"})
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		t.Setenv(k, "")
	}
	p := proxyAllowing(t, "allowed.example.test:"+port)

	// git reads the proxy from its environment, and from the user's own git
	// configuration too, which has to stay out of it.
	emptyConfig := filepath.Join(tempDir(t), "gitconfig")
	if err := os.WriteFile(emptyConfig, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	env := append(applyProxyEnv(os.Environ(), p, false),
		"https_proxy="+p.HTTProxyURL(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+emptyConfig,
		"NO_PROXY=", "no_proxy=",
	)
	cmd := exec.Command(gitPath, "ls-remote", fmt.Sprintf("https://allowed.example.test:%s/repo.git", port))
	cmd.Env = env
	cmd.Dir = tempDir(t)
	done := make(chan struct{})
	var out []byte
	go func() {
		out, _ = cmd.CombinedOutput()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("git did not finish")
	}

	if strings.Contains(string(out), "Proxy CONNECT aborted") {
		t.Fatalf("git gave up on the proxy's 407: %s", out)
	}
	if got := allowEvents(t, p.nvxHome); len(got) != 1 || !strings.HasPrefix(got[0], "allowed.example.test:"+port+" ") {
		t.Fatalf("the proxy never admitted the second CONNECT, so git did not get past the 407. egress_allow records: %q; git said: %s", got, out)
	}
}
