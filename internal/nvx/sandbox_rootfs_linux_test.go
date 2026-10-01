//go:build linux

package nvx

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"
)

// The bind plan shows each granted path once, under both names when it goes
// through a symlink, and skips what is missing or already inside another bind.
func TestSandboxBindPlan(t *testing.T) {
	root, err := filepath.EvalSymlinks(tempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "a")
	if err := os.MkdirAll(filepath.Join(dir, "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink("a", link); err != nil {
		t.Fatal(err)
	}

	got := sandboxBindPlan([]string{
		filepath.Join(dir, "b"), // inside dir: shown by dir's bind
		dir,
		file,
		link,
		filepath.Join(root, "missing"),
		"",
		"relative",
	})
	want := []sandboxBind{
		{src: dir, dst: dir, dir: true},
		{src: file, dst: file, dir: false},
		{src: dir, dst: link, dir: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sandboxBindPlan:\n got %+v\nwant %+v", got, want)
	}
}

// The host resolver sockets are in the view only when the network is open.
func TestSandboxVisiblePathsKeepResolverSocketsOnlyInOpenMode(t *testing.T) {
	for mode, want := range map[string]bool{"open": true, " Open ": true, "proxy": false, "offline": false, "loopback": false} {
		paths := sandboxVisiblePaths("/g", "/w", "", nil, false, mode)
		has := false
		for _, p := range paths {
			if p == "/run/systemd/resolve" {
				has = true
			}
		}
		if has != want {
			t.Errorf("mode %q: resolver directory in view = %v, want %v", mode, has, want)
		}
	}
}

// A contained process cannot connect to a UNIX socket the host created outside
// the sandbox, and nvx's own egress relay keeps working in the same run.
//
// The supervisor here is runLandlockExecChild itself, launched with the
// namespaces platformLaunchNative gives it, and its target is this test binary.
// The host socket stands in for /var/run/docker.sock: Landlock below ABI v9
// does not restrict connect() to a pathname socket, so before the sandbox got a
// filesystem view of its own the target reached it.
func TestContainedProcessCannotReachHostUnixSockets(t *testing.T) {
	switch {
	case os.Getenv("NVX_TEST_UNIXSOCK_TARGET") == "1":
		runHostUnixSocketProbe()
		return
	case os.Getenv("NVX_TEST_UNIXSOCK_SUPERVISOR") == "1":
		os.Exit(runHostUnixSocketSupervisor())
	}

	if _, err := exec.LookPath("ip"); err != nil {
		t.Skip("iproute2 not installed; bringUpLoopback needs `ip`")
	}
	if fd, err := landlockCreateRuleset(landlockHandledAccess()); err != nil {
		t.Skipf("landlock unavailable on this kernel: %v", err)
	} else {
		_ = syscall.Close(fd)
	}
	attr := supervisorSysProcAttr("proxy")
	requireLoopbackControl(t, attr)

	// The host service, outside every root the sandbox is granted.
	hostSock := filepath.Join(tempDir(t), "host.sock")
	hostLn, err := net.Listen("unix", hostSock)
	if err != nil {
		t.Fatal(err)
	}
	defer hostLn.Close()
	go func() {
		for {
			c, aerr := hostLn.Accept()
			if aerr != nil {
				return
			}
			_, _ = io.WriteString(c, "host-service\n")
			_ = c.Close()
		}
	}()

	// The positive control: an allowlisted host reached through the egress relay.
	remote := nonLoopbackListener(t)
	defer remote.Close()
	go func() {
		for {
			c, aerr := remote.Accept()
			if aerr != nil {
				return
			}
			_ = c.Close()
		}
	}()
	allowed := remote.Addr().String()
	policy := DefaultPolicy()
	policy.Isolation.Network.PromptUnknown = false
	policy.Isolation.Network.AllowHosts = []string{allowed}
	proxy, err := startEgressProxy(context.Background(), policy, Providers["node"], tempDir(t))
	if err != nil {
		t.Fatalf("startEgressProxy: %v", err)
	}
	defer proxy.Close()

	guestHome := tempDir(t)
	netCtx := &NetworkLaunchContext{Mode: "proxy"}
	if err := prepareEgressSocket(proxy, guestHome, netCtx); err != nil {
		t.Fatalf("prepareEgressSocket: %v", err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestContainedProcessCannotReachHostUnixSockets$")
	cmd.Env = append(os.Environ(),
		"NVX_TEST_UNIXSOCK_SUPERVISOR=1",
		"NVX_TEST_GUEST="+guestHome,
		"NVX_TEST_WORK="+tempDir(t),
		"NVX_TEST_NVXHOME="+tempDir(t),
		"NVX_TEST_EGRESS_SOCK="+netCtx.EgressSocketPath,
		"NVX_TEST_HOST_SOCK="+hostSock,
		"NVX_TEST_ALLOWED="+allowed,
		// Where production carries the session credential to the relay.
		"HTTP_PROXY="+proxy.HTTProxyURL(),
	)
	cmd.SysProcAttr = attr
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("supervisor failed: %v\noutput:\n%s", err, out)
	}
	got := parseProbeResults(string(out))

	for _, want := range []struct{ key, val, why string }{
		{"host_socket", "blocked", "a UNIX socket the host created outside the sandbox must not be reachable"},
		{"via_relay_allowed", "200", "the egress relay must still reach an allowlisted host"},
	} {
		if got[want.key] != want.val {
			t.Errorf("%s = %q, want %q -- %s\nfull output:\n%s", want.key, got[want.key], want.val, want.why, out)
		}
	}
}

// runHostUnixSocketSupervisor runs the production supervisor with this test
// binary as its target.
func runHostUnixSocketSupervisor() int {
	exe, err := os.Executable()
	if err != nil {
		fmt.Printf("setup_failed=%v\n", err)
		return 1
	}
	// Inherited by the target through os.Environ, which is how it knows to probe.
	_ = os.Setenv("NVX_TEST_UNIXSOCK_TARGET", "1")
	return runLandlockExecChild(supervisorExecArgs{
		GuestHome:    os.Getenv("NVX_TEST_GUEST"),
		WorkDir:      os.Getenv("NVX_TEST_WORK"),
		NvxHome:      os.Getenv("NVX_TEST_NVXHOME"),
		NetworkMode:  "proxy",
		EgressSocket: os.Getenv("NVX_TEST_EGRESS_SOCK"),
		// The test binary lives outside every default root.
		ReadExecRoots: []string{filepath.Dir(exe)},
		CmdPath:       exe,
		CmdArgs:       []string{"-test.run=^TestContainedProcessCannotReachHostUnixSockets$"},
	})
}

// runHostUnixSocketProbe runs as the contained target.
func runHostUnixSocketProbe() {
	if c, err := net.DialTimeout("unix", os.Getenv("NVX_TEST_HOST_SOCK"), 2*time.Second); err == nil {
		_ = c.Close()
		fmt.Println("host_socket=reached")
	} else {
		fmt.Println("host_socket=blocked")
		fmt.Printf("host_socket_error=%v\n", err)
	}

	proxyURL, err := url.Parse(os.Getenv("HTTP_PROXY"))
	if err != nil || proxyURL.User == nil {
		fmt.Printf("via_relay_allowed=no-proxy-env (%q)\n", os.Getenv("HTTP_PROXY"))
		return
	}
	cred := proxyURL.User.String() + "@"
	fmt.Printf("via_relay_allowed=%s\n", statusCodeVia(proxyURL.Host, os.Getenv("NVX_TEST_ALLOWED"), cred))
}
