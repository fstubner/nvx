//go:build linux

package nvx

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// A server in the sandbox is unreachable from the host until --expose publishes it.
//
// Outside network.mode open the sandbox runs in a network namespace of its own,
// and the 127.0.0.1 in there is not the host's. The docs said a contained server
// on Linux was reachable without a flag. Measured on Linux 6.18, `npx -y
// http-server -p 8099 -a 127.0.0.1` printed that it was serving, and curl from
// outside got exit 7 on 127.0.0.1 and on localhost. There was no way to publish
// the port.
//
// This runs the real supervisor in the namespaces platformLaunchNative gives it,
// with a server of this test binary's own inside, and the parent's half run here.
// It asserts four things together, as the Windows probe does, because any one
// alone is satisfiable by something broken. The host cannot reach the port
// without publishing, which is the claim the docs now make. It reaches it once
// published, and over one connection that carries several exchanges. And the
// sandbox still cannot reach out, because nothing in the tunnel runs that way.
//
// It needs `ip` for the loopback in the namespace, and skips without it. CI runs
// it again under sudo in the privileged step, where a skip fails the job.

const (
	exposeRoleEnv    = "NVX_TEST_EXPOSE_ROLE"
	exposeDirEnv     = "NVX_TEST_EXPOSE_DIR"
	exposeGuestEnv   = "NVX_TEST_EXPOSE_GUEST"
	exposeHomeEnv    = "NVX_TEST_EXPOSE_NVXHOME"
	exposePortEnv    = "NVX_TEST_EXPOSE_PORT"
	exposeOutsideEnv = "NVX_TEST_EXPOSE_OUTSIDE"
	exposeTestName   = "^TestContainedProcessServerIsReachableFromTheHostOnlyWhenPublished$"
)

func TestContainedProcessServerIsReachableFromTheHostOnlyWhenPublished(t *testing.T) {
	switch os.Getenv(exposeRoleEnv) {
	case "supervisor":
		port, _ := strconv.Atoi(os.Getenv(exposePortEnv))
		// The target starts with this process's environment, so the role changes here.
		_ = os.Setenv(exposeRoleEnv, "target")
		os.Exit(runLandlockExecChild(supervisorExecArgs{
			GuestHome:     os.Getenv(exposeGuestEnv),
			WorkDir:       os.Getenv(exposeDirEnv),
			NvxHome:       os.Getenv(exposeHomeEnv),
			NetworkMode:   "proxy",
			ExposePorts:   []int{port},
			ReadExecRoots: []string{filepath.Dir(os.Args[0])},
			CmdPath:       os.Args[0],
			CmdArgs:       []string{"-test.run=" + exposeTestName},
		}))
	case "target":
		runExposeTarget()
		os.Exit(0)
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

	// An address outside the host's loopback, to prove the sandbox still cannot
	// reach out. The listener does not matter. Whether anything connects does.
	outside := nonLoopbackListener(t)
	defer outside.Close()
	go func() {
		for {
			c, err := outside.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	inside, err := freeLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	hostPort, err := freeLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	guest, work, nvxHome := tempDir(t), tempDir(t), tempDir(t)

	// The parent's half, as platformLaunchNative runs it.
	netCtx := &NetworkLaunchContext{
		Mode:        "proxy",
		ExposePorts: []exposeMapping{{Container: inside, Host: hostPort}},
	}
	stop, err := publishExposedPorts(guest, nvxHome, netCtx)
	if err != nil {
		t.Fatalf("publishExposedPorts: %v", err)
	}
	defer stop()
	if len(netCtx.ExposePorts) != 1 {
		t.Fatalf("the mapping was dropped for network.mode proxy: %+v", netCtx.ExposePorts)
	}

	out := &syncBuffer{}
	cmd := exec.Command(os.Args[0], "-test.run="+exposeTestName)
	cmd.Env = append(os.Environ(),
		exposeRoleEnv+"=supervisor",
		exposeDirEnv+"="+work,
		exposeGuestEnv+"="+guest,
		exposeHomeEnv+"="+nvxHome,
		exposePortEnv+"="+strconv.Itoa(inside),
		exposeOutsideEnv+"="+outside.Addr().String(),
	)
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = attr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the supervisor: %v", err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		// The supervisor is PID 1 of its namespace, so this ends the server with it.
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
		}
	})

	// The server has listened once it says so, and says so after it has tried to
	// reach the outside.
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(work, "ready")); err == nil {
			break
		}
		select {
		case <-done:
			t.Fatalf("the sandbox ended before the server was listening\noutput:\n%s", out.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("the server never started listening\noutput:\n%s", out.String())
		}
		time.Sleep(20 * time.Millisecond)
	}

	if b, _ := os.ReadFile(filepath.Join(work, "egress")); string(b) != "blocked" {
		t.Errorf("the sandbox reached an address outside the host's loopback (%q): publishing a port must grant no way out", b)
	}

	// Not published, not reachable. The server is listening, in the namespace, on
	// the number it asked for, and the host has no route to that.
	if c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(inside)), 2*time.Second); err == nil {
		_ = c.Close()
		t.Errorf("the host reached the sandbox's own port %d without it being published", inside)
	}

	// Published and reachable, over one connection with several exchanges.
	host := net.JoinHostPort("127.0.0.1", strconv.Itoa(hostPort))
	c, err := net.DialTimeout("tcp", host, 5*time.Second)
	if err != nil {
		t.Fatalf("the host could not reach the published port %s: %v\noutput:\n%s", host, err, out.String())
	}
	defer c.Close()
	r := bufio.NewReader(c)
	for _, word := range []string{"one", "two", "three"} {
		if got := exposeExchange(t, c, r, word); got != "echo:"+word {
			t.Fatalf("sent %q, got %q", word, got)
		}
	}

	// Several at once, which is what a page load does, and more than the pool
	// parks, so the tunnels have to be replaced as they are used.
	var wg sync.WaitGroup
	errs := make(chan string, 32)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			conn, err := net.DialTimeout("tcp", host, 5*time.Second)
			if err != nil {
				errs <- fmt.Sprintf("connection %d: %v", i, err)
				return
			}
			defer conn.Close()
			word := fmt.Sprintf("n%d", i)
			if got := exposeExchange(t, conn, bufio.NewReader(conn), word); got != "echo:"+word {
				errs <- fmt.Sprintf("connection %d: sent %q, got %q", i, word, got)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

// exposeExchange sends one line and reads the line that comes back.
func exposeExchange(t *testing.T, c net.Conn, r *bufio.Reader, word string) string {
	t.Helper()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := fmt.Fprintf(c, "%s\n", word); err != nil {
		return "write failed: " + err.Error()
	}
	line, err := r.ReadString('\n')
	if err != nil {
		return "read failed: " + err.Error()
	}
	return strings.TrimSpace(line)
}

// runExposeTarget is the server inside the sandbox. It echoes each line it is
// sent, with a prefix, until it is killed.
func runExposeTarget() {
	dir := os.Getenv(exposeDirEnv)
	write := func(name, body string) {
		tmp := filepath.Join(dir, name+".tmp")
		_ = os.WriteFile(tmp, []byte(body), 0o600)
		_ = os.Rename(tmp, filepath.Join(dir, name))
	}

	// Whether it can reach an address outside the host's loopback, before it
	// reports itself listening.
	if c, err := net.DialTimeout("tcp", os.Getenv(exposeOutsideEnv), 2*time.Second); err == nil {
		_ = c.Close()
		write("egress", "reached")
	} else {
		write("egress", "blocked")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:"+os.Getenv(exposePortEnv))
	if err != nil {
		write("ready", "listen failed: "+err.Error())
		return
	}
	write("ready", "ok")
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			r := bufio.NewReader(c)
			for {
				line, err := r.ReadString('\n')
				if err != nil {
					return
				}
				_, _ = fmt.Fprintf(c, "echo:%s", line)
			}
		}(c)
	}
}

// publishExposedPorts publishes where the sandbox has a namespace to cross, and
// not where it has none. Nothing here needs a namespace.
func TestPublishExposedPortsOnlyWhereTheSandboxHasANamespace(t *testing.T) {
	for _, tc := range []struct {
		mode      string
		published bool
	}{
		{"proxy", true},
		{"loopback", true},
		{"", true}, // an empty mode is proxy, as it is everywhere else
		{"open", false},
		{"offline", false},
	} {
		t.Run("network.mode "+tc.mode, func(t *testing.T) {
			guest, nvxHome := tempDir(t), tempDir(t)
			inside, err := freeLoopbackPort()
			if err != nil {
				t.Fatal(err)
			}
			hostPort, err := freeLoopbackPort()
			if err != nil {
				t.Fatal(err)
			}
			netCtx := &NetworkLaunchContext{
				Mode:        tc.mode,
				ExposePorts: []exposeMapping{{Container: inside, Host: hostPort}},
			}
			stop, err := publishExposedPorts(guest, nvxHome, netCtx)
			if err != nil {
				t.Fatalf("publishExposedPorts: %v", err)
			}
			host := net.JoinHostPort("127.0.0.1", strconv.Itoa(hostPort))
			sock := linuxExposeSocketPath(guest, inside)

			c, dialErr := net.DialTimeout("tcp", host, time.Second)
			if c != nil {
				_ = c.Close()
			}
			_, statErr := os.Stat(sock)
			if tc.published {
				if dialErr != nil || statErr != nil {
					t.Errorf("not published: dial %v, tunnel socket %v", dialErr, statErr)
				}
				if len(netCtx.ExposePorts) != 1 {
					t.Errorf("the mapping was dropped: %+v", netCtx.ExposePorts)
				}
			} else {
				if dialErr == nil || statErr == nil {
					t.Errorf("published where there is nothing to cross: dial %v, tunnel socket %v", dialErr, statErr)
				}
				if len(netCtx.ExposePorts) != 0 {
					t.Errorf("the supervisor would be told to tunnel a port that was not published: %+v", netCtx.ExposePorts)
				}
			}

			stop()
			if c, err := net.DialTimeout("tcp", host, time.Second); err == nil {
				_ = c.Close()
				t.Error("the host listener is still open after stop")
			}
			if _, err := os.Stat(sock); err == nil {
				t.Error("the tunnel socket is still there after stop")
			}
		})
	}
}
