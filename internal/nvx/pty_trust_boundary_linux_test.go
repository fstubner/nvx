//go:build linux

package nvx

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

const ptyChildEnv = "NVX_TEST_PTY_CHILD"

// A y typed into a terminal cannot widen the sandbox.
//
// nvx took a terminal on stdin to mean a person, and asked there before
// trusting a loosening project policy, allowing an unknown host or keeping a
// tool's profile. An agent harness that runs commands in a pseudo-terminal
// presents exactly that terminal, and the model answers. This plays that agent:
// it starts the three decisions in a child whose stdin and controlling terminal
// are a pseudo-terminal, and writes y into it whenever [y/N] appears.
//
// The verdict is read from what nvx recorded, not from what it printed.
func TestATerminalAnswerCannotWidenTheSandbox(t *testing.T) {
	if os.Getenv(ptyChildEnv) == "1" {
		ptyWideningChild(t)
		return
	}
	master, slave, err := openTestPty()
	if err != nil {
		t.Skipf("no pseudo-terminal here: %v", err)
	}
	defer master.Close()

	home := tempDir(t)
	project := tempDir(t)
	writePolicyFixture(t, project, "package.json", `{"name":"p"}`)
	writePolicyFixture(t, project, ".nvx-policy.json", `{"isolation":{"network":{"mode":"open"}}}`)

	cmd := exec.Command(os.Args[0], "-test.run=^TestATerminalAnswerCannotWidenTheSandbox$", "-test.count=1")
	cmd.Dir = project
	cmd.Env = append(os.Environ(), ptyChildEnv+"=1", "NVX_HOME="+home,
		"NVX_TRUST_YES=", "NVX_YES=", "NVX_AGENT_MODE=", "NVX_NONINTERACTIVE=")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		slave.Close()
		t.Fatal(err)
	}
	slave.Close()

	output := answerYesOnTerminal(t, master, cmd)
	if !strings.Contains(output, "PTY-CHILD-INTERACTIVE=true") {
		t.Fatalf("the child did not see a terminal on stdin, so this proves nothing:\n%s", output)
	}
	if !strings.Contains(output, "PTY-CHILD-DONE") {
		t.Fatalf("the child did not finish the three decisions:\n%s", output)
	}

	// The child records grants under the folder it found itself in, which is
	// the temp folder with any symlink in its path resolved.
	scopes := []string{project}
	if resolved, err := filepath.EvalSymlinks(project); err == nil && resolved != project {
		scopes = append(scopes, resolved)
	}
	for _, scope := range scopes {
		g := loadProjectGrants(home, scope)
		if len(g.PolicyPins) != 0 {
			t.Errorf("a y typed into the terminal trusted a project policy that sets network.mode open: %v", g.PolicyPins)
		}
		if g.hasTrustedTool("wrangler") {
			t.Error("a y typed into the terminal granted a persistent tool profile")
		}
	}
	if !strings.Contains(output, "PTY-CHILD-EGRESS=refused") {
		t.Errorf("a y typed into the terminal allowed a host the allowlist does not name:\n%s", output)
	}
}

// ptyWideningChild runs in the child, on the pseudo-terminal.
func ptyWideningChild(t *testing.T) {
	home := os.Getenv("NVX_HOME")
	fmt.Printf("PTY-CHILD-INTERACTIVE=%v\n", stdinIsInteractive())

	_ = ensureProjectPolicyTrust(home)
	ensureTrustedToolGrant(home, "wrangler")

	p := newPromptingProxy(t, nil)
	verdict := "refused"
	if p.allowed(parseHostPortSpec("example.com", 443), []net.IP{net.ParseIP("104.16.0.1")}) {
		verdict = "allowed"
	}
	fmt.Printf("PTY-CHILD-EGRESS=%s\n", verdict)
	fmt.Println("PTY-CHILD-DONE")
}

// answerYesOnTerminal reads the child's terminal, types y at every [y/N], and
// returns everything the terminal showed once the child has exited.
func answerYesOnTerminal(t *testing.T, master *os.File, cmd *exec.Cmd) string {
	t.Helper()
	var mu sync.Mutex
	var buf bytes.Buffer
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		chunk := make([]byte, 4096)
		answered := 0
		for {
			n, err := master.Read(chunk)
			if n > 0 {
				mu.Lock()
				buf.Write(chunk[:n])
				asked := strings.Count(buf.String(), "[y/N]")
				mu.Unlock()
				for ; answered < asked; answered++ {
					_, _ = master.Write([]byte("y\n"))
				}
			}
			if err != nil {
				return // EIO once the child's side of the terminal is closed
			}
		}
	}()

	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case <-waited:
	case <-time.After(2 * time.Minute):
		_ = cmd.Process.Kill()
		<-waited
		t.Fatal("the child did not finish in two minutes")
	}
	select {
	case <-readDone:
	case <-time.After(5 * time.Second):
	}
	mu.Lock()
	defer mu.Unlock()
	return buf.String()
}

// openTestPty opens a pseudo-terminal pair through /dev/ptmx.
func openTestPty() (master, slave *os.File, err error) {
	master, err = os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	var unlock int32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		master.Close()
		return nil, nil, errno
	}
	var n uint32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); errno != 0 {
		master.Close()
		return nil, nil, errno
	}
	slave, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, nil, err
	}
	return master, slave, nil
}
