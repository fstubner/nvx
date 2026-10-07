//go:build linux

package nvx

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// A contained process gets the terminal and its interrupt the way an uncontained
// one does.
//
// The target used to be started in a process group of its own. That kept it out
// of the terminal's foreground group, which broke two things measured on Linux
// 6.18. Ctrl-C reached nvx and the supervisor and not the processes the target
// had started, so `nvx npx -y http-server` was still running 15 seconds later,
// because the shell that npm runs a script with passes nothing on. And reading
// the terminal from a background group stops the reader with SIGTTIN, so a
// contained `node` REPL hung. On a kernel that cannot scope signals the target
// has a group of its own again, and the supervisor signals that whole group (see
// applyLinuxNamespaces).
//
// These run the real chain on a real pseudo-terminal. A stand-in for nvx runs
// the supervisor through runSupervisor, the supervisor is runLandlockExecChild,
// and the target is this test binary or a real program. The terminal is a pty,
// with Ctrl-C written to it as the byte the line discipline turns into SIGINT
// for the foreground group.
//
// Roles share the one test function, as in the other supervisor tests.
//
//	shell       owns the terminal and starts the nvx stand-in in the background
//	parent      stands in for nvx
//	supervisor  runs runLandlockExecChild
//	target      the contained command, a stand-in in one of several modes
//	leaf        what the tree-mode target starts
//
// The cases run on the kernel's own Landlock ABI and again on ABI v5, which has
// no signal scope. A case that needs the shared group is left out where the
// target has a group of its own.
//
// Unprivileged on Ubuntu 24.04 the mount namespace is refused and these skip.
// CI runs them again under sudo in the privileged step, where a skip fails the
// job.

const (
	terminalRoleEnv  = "NVX_TEST_TERMINAL_ROLE"
	terminalModeEnv  = "NVX_TEST_TERMINAL_MODE"
	terminalDirEnv   = "NVX_TEST_TERMINAL_DIR"
	terminalGuestEnv = "NVX_TEST_TERMINAL_GUEST"
	terminalHomeEnv  = "NVX_TEST_TERMINAL_NVXHOME"
	terminalABIEnv   = "NVX_TEST_TERMINAL_ABI"
	terminalCmdEnv   = "NVX_TEST_TERMINAL_COMMAND"
	terminalRootsEnv = "NVX_TEST_TERMINAL_ROOTS"
	terminalTestName = "^TestContainedProcessGetsTheTerminalsInterruptOnce$"
)

// terminalBranch is one of the two ways the supervisor can start the target.
type terminalBranch struct {
	name string
	abi  int // 0 is the running kernel's
}

// terminalBranches are the kernel's own ABI and the newest one with no signal
// scope. On a kernel below v6 both run the target in a group of its own.
var terminalBranches = []terminalBranch{
	{"on this kernel", 0},
	{"on Landlock ABI v5", 5},
}

// ownGroup reports whether the target gets a process group of its own on this
// branch.
func (b terminalBranch) ownGroup() bool {
	abi := b.abi
	if abi == 0 {
		abi = landlockABIVersion()
	}
	return landlockScopesForABI(abi)&landlockScopeSignal == 0
}

func TestContainedProcessGetsTheTerminalsInterruptOnce(t *testing.T) {
	runTerminalRole()

	for _, branch := range terminalBranches {
		t.Run(branch.name, func(t *testing.T) { testTerminalInterrupts(t, branch) })
	}
}

func testTerminalInterrupts(t *testing.T, branch terminalBranch) {
	for _, tc := range []struct {
		name       string
		mode       string
		terminal   bool
		background bool
		// sharedGroup marks a case that holds only when the target shares nvx's
		// process group.
		sharedGroup bool
		// act is what the test does once the target is running, and what it must
		// leave behind in the work directory.
		act      func(r *terminalRun)
		wantFile string
		want     string
		why      string
	}{
		{
			name: "Ctrl-C reaches what the target started", mode: "tree", terminal: true,
			act:      func(r *terminalRun) { r.typeBytes("\x03") },
			wantFile: "count", want: "1",
			why: "the target is started like npm, which runs sh, which runs the server, and passes SIGINT to neither. " +
				"Only the terminal reaches the server, and only when the whole tree is in the foreground group",
		},
		{
			name: "Ctrl-C reaches the target once", mode: "count", terminal: true,
			act:      func(r *terminalRun) { r.typeBytes("\x03") },
			wantFile: "count", want: "1",
			why: "the kernel already sent it to the target, so the supervisor must not send a second",
		},
		{
			name: "the target can read the terminal", mode: "read", terminal: true, sharedGroup: true,
			act:      func(r *terminalRun) { r.typeBytes("hello\n") },
			wantFile: "line", want: "hello\n",
			why: "a process in a background group is stopped by SIGTTIN when it reads the terminal",
		},
		{
			name: "an interrupt sent to nvx without a terminal is passed on", mode: "count", terminal: false,
			act:      func(r *terminalRun) { r.signalNvx(syscall.SIGINT) },
			wantFile: "count", want: "1",
			why: "nothing else delivers it, so the supervisor must forward it",
		},
		{
			name: "an interrupt sent to a background nvx is passed on", mode: "count", terminal: true, background: true,
			act:      func(r *terminalRun) { r.signalNvx(syscall.SIGINT) },
			wantFile: "count", want: "1",
			why: "the terminal did not deliver it, so the supervisor cannot take it for the terminal's own",
		},
	} {
		// The cost of a group of its own, which applyLinuxNamespaces accepts on
		// kernels that cannot scope signals.
		if tc.sharedGroup && branch.ownGroup() {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			r := startTerminalRun(t, terminalOpts{mode: tc.mode, terminal: tc.terminal, background: tc.background, abi: branch.abi})
			if _, ok := r.waitFile("ready", 30*time.Second); !ok {
				r.failOrSkip("the contained target never started")
			}
			tc.act(r)
			got, ok := r.waitFile(tc.wantFile, 10*time.Second)
			if !ok {
				t.Fatalf("%s was never written 10 seconds on. %s\noutput:\n%s", tc.wantFile, tc.why, r.out.String())
			}
			if got != tc.want {
				t.Errorf("%s = %q, want %q. %s\noutput:\n%s", tc.wantFile, got, tc.want, tc.why, r.out.String())
			}
			if !r.waitExit(10 * time.Second) {
				t.Errorf("the run was still going 10 seconds after the target finished\noutput:\n%s", r.out.String())
			}
		})
	}
}

// terminalRun is one launch of the chain.
type terminalRun struct {
	t      *testing.T
	cmd    *exec.Cmd
	master *os.File
	out    *syncBuffer
	work   string
	guest  string
	done   chan struct{}
}

// terminalOpts is how one launch of the chain is set up.
type terminalOpts struct {
	mode       string // what the stand-in target does
	terminal   bool   // nvx runs on a pseudo-terminal
	background bool   // nvx runs as a background job of a shell that owns the terminal
	abi        int    // the Landlock ABI the supervisor builds for, 0 for the kernel's
	// command is a real program to contain in place of the stand-in target,
	// started in the work directory. execRoots are the directories it needs to
	// read and execute from.
	command   []string
	execRoots []string
	env       []string // more environment for every process in the chain
	// prepare, when set, writes what the command needs into the work directory
	// and the guest home before anything starts.
	prepare func(work, guest string)
	// joinGroup puts nvx into this existing process group, without a terminal.
	joinGroup int
}

func startTerminalRun(t *testing.T, o terminalOpts) *terminalRun {
	t.Helper()
	if fd, err := landlockCreateRuleset(landlockHandledAccess()); err != nil {
		t.Skipf("landlock unavailable on this kernel: %v", err)
	} else {
		_ = syscall.Close(fd)
	}
	requireNamespaceSupport(t, supervisorSysProcAttr("open"))

	r := &terminalRun{t: t, out: &syncBuffer{}, work: tempDir(t), guest: tempDir(t), done: make(chan struct{})}
	if o.prepare != nil {
		o.prepare(r.work, r.guest)
	}
	role := "parent"
	if o.background {
		role = "shell"
	}
	r.cmd = exec.Command(os.Args[0], "-test.run="+terminalTestName)
	r.cmd.Env = append(os.Environ(),
		terminalRoleEnv+"="+role,
		terminalModeEnv+"="+o.mode,
		terminalDirEnv+"="+r.work,
		terminalGuestEnv+"="+r.guest,
		terminalHomeEnv+"="+tempDir(t),
		terminalABIEnv+"="+strconv.Itoa(o.abi),
	)
	if len(o.command) > 0 {
		cmdJSON, _ := json.Marshal(o.command)
		rootsJSON, _ := json.Marshal(o.execRoots)
		r.cmd.Env = append(r.cmd.Env, terminalCmdEnv+"="+string(cmdJSON), terminalRootsEnv+"="+string(rootsJSON))
	}
	r.cmd.Env = append(r.cmd.Env, o.env...)
	switch {
	case o.joinGroup != 0:
		r.cmd.Stdout, r.cmd.Stderr = r.out, r.out
		r.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: o.joinGroup}
		if err := r.cmd.Start(); err != nil {
			t.Fatalf("start: %v", err)
		}
	case o.terminal:
		master, slave := openPty(t)
		r.master = master
		r.cmd.Stdin, r.cmd.Stdout, r.cmd.Stderr = slave, slave, slave
		// A session of its own with the pty as its terminal, so the first process
		// is the foreground group's leader, as a shell's job is.
		r.cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
		if err := r.cmd.Start(); err != nil {
			t.Fatalf("start: %v", err)
		}
		_ = slave.Close()
		go func() { _, _ = io.Copy(r.out, master) }()
	default:
		r.cmd.Stdout, r.cmd.Stderr = r.out, r.out
		r.cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := r.cmd.Start(); err != nil {
			t.Fatalf("start: %v", err)
		}
	}
	go func() {
		_ = r.cmd.Wait()
		close(r.done)
	}()
	t.Cleanup(func() {
		// The session leader's group, and the stand-in's when the shell started it
		// in one of its own. The supervisor's namespace goes with the stand-in,
		// since its parent is gone. A stand-in that joined a group is killed alone,
		// and the group is the caller's to end.
		if o.joinGroup != 0 {
			_ = r.cmd.Process.Kill()
		} else {
			_ = syscall.Kill(-r.cmd.Process.Pid, syscall.SIGKILL)
		}
		if b, err := os.ReadFile(filepath.Join(r.work, "parent.pid")); err == nil {
			if pid, perr := strconv.Atoi(strings.TrimSpace(string(b))); perr == nil {
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
		}
		select {
		case <-r.done:
		case <-time.After(10 * time.Second):
		}
		if r.master != nil {
			_ = r.master.Close()
		}
	})
	return r
}

// openPty returns both ends of a new pseudo-terminal, or skips the test.
func openPty(t *testing.T) (master, slave *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pseudo-terminal here: %v", err)
	}
	var unlock, n int32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		t.Fatalf("unlock the pty: %v", errno)
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); errno != 0 {
		t.Fatalf("name the pty: %v", errno)
	}
	slave, err = os.OpenFile("/dev/pts/"+strconv.Itoa(int(n)), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open the pty: %v", err)
	}
	return master, slave
}

func (r *terminalRun) typeBytes(s string) {
	r.t.Helper()
	if _, err := r.master.WriteString(s); err != nil {
		r.t.Fatalf("type into the terminal: %v", err)
	}
}

// signalNvx signals the stand-in for nvx by its pid, as `kill` does. In the
// background case that pid is the one the shell wrote down.
func (r *terminalRun) signalNvx(sig syscall.Signal) {
	r.t.Helper()
	pid := r.cmd.Process.Pid
	if b, err := os.ReadFile(filepath.Join(r.work, "parent.pid")); err == nil {
		pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
	}
	if err := syscall.Kill(pid, sig); err != nil {
		r.t.Fatalf("signal nvx: %v", err)
	}
}

// waitFile waits for a file the target writes. It stops early when the run has
// ended, because a file that is not there by then never will be.
func (r *terminalRun) waitFile(name string, limit time.Duration) (string, bool) {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(filepath.Join(r.work, name)); err == nil {
			return string(b), true
		}
		select {
		case <-r.done:
			b, err := os.ReadFile(filepath.Join(r.work, name))
			return string(b), err == nil
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	return "", false
}

func (r *terminalRun) waitExit(limit time.Duration) bool {
	select {
	case <-r.done:
		return true
	case <-time.After(limit):
		return false
	}
}

// failOrSkip ends a test whose chain did not start. Ubuntu 24.04 refuses a mount
// namespace inside an unprivileged user namespace, and that is a skip.
func (r *terminalRun) failOrSkip(why string) {
	r.t.Helper()
	out := r.out.String()
	if strings.Contains(out, "unshare mount namespace") && strings.Contains(out, "operation not permitted") {
		r.t.Skipf("this host refuses a mount namespace inside an unprivileged user namespace "+
			"(Ubuntu 24.04 AppArmor), and the privileged CI step covers it:\n%s", out)
	}
	r.t.Fatalf("%s\noutput:\n%s", why, out)
}

// runTerminalRole runs one role of the chain when this process is one, and does
// not return then.
func runTerminalRole() {
	switch os.Getenv(terminalRoleEnv) {
	case "shell":
		os.Exit(terminalShell())
	case "parent":
		os.Exit(terminalParent())
	case "supervisor":
		os.Exit(terminalSupervisor())
	case "target":
		os.Exit(terminalTarget())
	case "leaf":
		os.Exit(terminalCountInterrupts(os.Getenv(terminalDirEnv)))
	}
}

// terminalShell owns the terminal and runs nvx in the background, a group that is
// not the terminal's foreground one, and notes its pid.
func terminalShell() int {
	cmd := terminalChild("parent")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return 1
	}
	pid := strconv.Itoa(cmd.Process.Pid)
	if err := terminalWrite(filepath.Join(os.Getenv(terminalDirEnv), "parent.pid"), []byte(pid), 0o600); err != nil {
		return 1
	}
	_ = cmd.Wait()
	return 0
}

// terminalParent is nvx as platformLaunchNative runs it.
func terminalParent() int {
	guest := os.Getenv(terminalGuestEnv)
	writeSessionOwner(guest, time.Now())
	cmd := terminalChild("supervisor")
	cmd.SysProcAttr = supervisorSysProcAttr("open")
	if err := runSupervisor(cmd, guest); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return childExitCode(exitErr)
		}
		return 1
	}
	return 0
}

func terminalSupervisor() int {
	// The target starts with this process's environment, so the role changes here.
	_ = os.Setenv(terminalRoleEnv, "target")
	if abi, err := strconv.Atoi(os.Getenv(terminalABIEnv)); err == nil && abi > 0 {
		supervisorLandlockABI = func() int { return abi }
	}
	command := []string{os.Args[0], "-test.run=" + terminalTestName}
	roots := []string{filepath.Dir(os.Args[0])}
	if c := os.Getenv(terminalCmdEnv); c != "" {
		var extra []string
		if json.Unmarshal([]byte(c), &command) != nil || json.Unmarshal([]byte(os.Getenv(terminalRootsEnv)), &extra) != nil {
			return 1
		}
		roots = append(roots, extra...)
		// The home and temp directory nvx gives a real command.
		guest := os.Getenv(terminalGuestEnv)
		_ = os.Setenv("HOME", guest)
		_ = os.Setenv("TMPDIR", filepath.Join(guest, "tmp"))
	}
	return runLandlockExecChild(supervisorExecArgs{
		GuestHome:     os.Getenv(terminalGuestEnv),
		WorkDir:       os.Getenv(terminalDirEnv),
		NvxHome:       os.Getenv(terminalHomeEnv),
		NetworkMode:   "open",
		ReadExecRoots: roots,
		CmdPath:       command[0],
		CmdArgs:       command[1:],
	})
}

// terminalChild is this binary again in another role, on this process's terminal.
func terminalChild(role string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run="+terminalTestName)
	cmd.Env = append(os.Environ(), terminalRoleEnv+"="+role)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd
}

// terminalWrite creates the file whole, so the test never reads one that has been
// created and not yet written.
func terminalWrite(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func terminalTarget() int {
	dir := os.Getenv(terminalDirEnv)
	switch os.Getenv(terminalModeEnv) {
	case "tree":
		return terminalTree()
	case "read":
		_ = terminalWrite(filepath.Join(dir, "ready"), nil, 0o600)
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		_ = terminalWrite(filepath.Join(dir, "line"), []byte(line), 0o600)
		return 0
	case "killgroup":
		// kill(0) signals every process in the caller's group, this one included.
		_ = terminalWrite(filepath.Join(dir, "ready"), nil, 0o600)
		_ = syscall.Kill(0, syscall.SIGKILL)
		return 0
	case "abstract":
		// The result of connecting to the abstract socket the test listens on.
		_ = terminalWrite(filepath.Join(dir, "ready"), nil, 0o600)
		result := "connected"
		c, err := net.Dial("unix", os.Getenv(abstractSocketEnv))
		if err != nil {
			result = err.Error()
		} else {
			_ = c.Close()
		}
		_ = terminalWrite(filepath.Join(dir, "abstract"), []byte(result), 0o600)
		return 0
	}
	return terminalCountInterrupts(dir)
}

// terminalTree is what npm does with a script. It starts the command and waits,
// takes an interrupt itself and does not pass it on, so the command only gets one
// from the terminal.
func terminalTree() int {
	swallow := make(chan os.Signal, 8)
	signal.Notify(swallow, syscall.SIGINT)
	cmd := terminalChild("leaf")
	if err := cmd.Start(); err != nil {
		return 1
	}
	_ = cmd.Wait()
	return 0
}

// terminalCountInterrupts waits for an interrupt, listens a little longer for a
// second, and writes how many it saw.
func terminalCountInterrupts(dir string) int {
	sigs := make(chan os.Signal, 8)
	signal.Notify(sigs, syscall.SIGINT)
	_ = terminalWrite(filepath.Join(dir, "ready"), nil, 0o600)
	<-sigs
	n := 1
	quiet := time.After(700 * time.Millisecond)
	for {
		select {
		case <-sigs:
			n++
		case <-quiet:
			_ = terminalWrite(filepath.Join(dir, "count"), []byte(strconv.Itoa(n)), 0o600)
			return 0
		}
	}
}

// The supervisor drops the SIGINT it gets from the terminal when the target
// shares its group, because the target has Ctrl-C already, and passes it on when
// the target has a group of its own. It turns the interrupt nvx forwards into one
// for the target. Terminate and hangup go on as they are.
//
// The pty test above asserts the same thing end to end and can miss it. Two
// SIGINTs that reach a process before it has handled the first are delivered as
// one. This cannot miss.
func TestSupervisorPassesOnAnInterruptOnlyWhenNvxAsksForOne(t *testing.T) {
	for _, tc := range []struct {
		name     string
		in       os.Signal
		ownGroup bool
		want     syscall.Signal
		ok       bool
	}{
		{"the terminal's own Ctrl-C", syscall.SIGINT, false, 0, false},
		{"the interrupt nvx forwards", supervisorInterruptSignal, false, syscall.SIGINT, true},
		{"terminate", syscall.SIGTERM, false, syscall.SIGTERM, true},
		{"hangup", syscall.SIGHUP, false, syscall.SIGHUP, true},
		{"the terminal's own Ctrl-C, to a group of its own", syscall.SIGINT, true, syscall.SIGINT, true},
		{"the interrupt nvx forwards, to a group of its own", supervisorInterruptSignal, true, syscall.SIGINT, true},
		{"terminate, to a group of its own", syscall.SIGTERM, true, syscall.SIGTERM, true},
	} {
		got, ok := signalForTarget(tc.in, tc.ownGroup)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: signalForTarget(%v, %v) = %v, %v, want %v, %v", tc.name, tc.in, tc.ownGroup, got, ok, tc.want, tc.ok)
		}
	}
}
