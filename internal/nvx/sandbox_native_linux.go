//go:build linux

package nvx

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// egressSocketName is the UNIX socket, inside the guest home, that the parent's
// egress proxy also listens on. The guest home already carries full Landlock
// read/write rights, so no extra rule is needed to reach it.
const egressSocketName = ".nvx-egress.sock"

// prepareEgressSocket exposes the parent's egress proxy on a UNIX socket so
// the contained process can reach it from inside its network namespace.
//
// A loopback-only netns has no route to any allowlisted host, so the proxy cannot
// live inside it. UNIX sockets are filesystem objects and are not namespaced by
// the network namespace, which makes them the one channel that crosses cleanly.
func prepareEgressSocket(egress *EgressProxy, guestHome, nvxHome string, netCtx *NetworkLaunchContext) error {
	if egress == nil || netCtx == nil || guestHome == "" {
		return nil
	}
	if !networkModeRequiresNamespace(netCtx.Mode) {
		return nil // no namespace, so the loopback TCP listeners are reachable as-is
	}
	// offline gets no way to the proxy at all, as on Windows
	// (windowsEgressNeedsRelay). The namespace is still created; only the socket
	// that would carry requests out of it is withheld.
	if strings.EqualFold(strings.TrimSpace(netCtx.Mode), "offline") {
		return nil
	}
	sock := filepath.Join(guestHome, egressSocketName)
	if err := linuxSocketTooLong("egress socket", sock, guestHome, nvxHome, netCtx); err != nil {
		return err
	}
	if err := egress.ListenUnix(sock); err != nil {
		return err
	}
	netCtx.EgressSocketPath = sock
	return nil
}

// linuxSessionSockets lists the sockets this session can create in guestHome:
// the egress one, a tunnel per --connect port, and the loopback one in that mode.
// The egress socket is listed whenever prepareEgressSocket would create it.
func linuxSessionSockets(guestHome string, netCtx *NetworkLaunchContext) []string {
	if netCtx == nil {
		return nil
	}
	var socks []string
	if networkModeRequiresNamespace(netCtx.Mode) && !strings.EqualFold(strings.TrimSpace(netCtx.Mode), "offline") {
		socks = append(socks, filepath.Join(guestHome, egressSocketName))
	}
	for _, m := range netCtx.ConnectPorts {
		socks = append(socks, linuxConnectSocketPath(guestHome, m.Host))
	}
	if networkModeRequiresNamespace(netCtx.Mode) {
		for _, m := range netCtx.ExposePorts {
			socks = append(socks, linuxExposeSocketPath(guestHome, m.Container))
		}
	}
	if loopbackRedirectMode(netCtx.Mode) {
		socks = append(socks, loopbackSocketPath(guestHome))
	}
	return socks
}

// linuxSocketTooLong refuses sock when it will not bind. The NVX_HOME it advises
// leaves room for the longest socket this session creates, as the Windows
// refusal does, so following it cannot meet a second refusal from a longer name.
func linuxSocketTooLong(what, sock, guestHome, nvxHome string, netCtx *NetworkLaunchContext) error {
	longest := sock
	for _, s := range linuxSessionSockets(guestHome, netCtx) {
		if len(s) > len(longest) {
			longest = s
		}
	}
	return unixSocketPathTooLong(what, sock, longest, nvxHome)
}

// platformLaunchNative re-execs nvx as a Landlock child so restrictions are
// applied in the process that runs the target command.
func platformLaunchNative(config SandboxConfig, guestHome, workDir, cmdPath string, cleanEnv []string, netCtx NetworkLaunchContext) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		LogError("Failed to resolve nvx executable: %v", err)
		return 1, refusedToStart("the nvx executable could not be resolved")
	}

	// Host services this run may reach. Opened here, outside the namespace, and
	// the in-sandbox port is resolved here too, so both numbers reach the
	// supervisor already decided.
	connectEnv, stopConnect, err := openConnectSockets(guestHome, config.NvxHome, &netCtx)
	if err != nil {
		LogError("Could not open a path to a host service for the sandbox: %v", err)
		return 1, refusedToStart("a path to a host service could not be opened")
	}
	defer stopConnect()
	cleanEnv = append(cleanEnv, connectEnv...)

	// Ports the developer asked to publish. Opened here too, outside the
	// namespace, so the host listener and the tunnel socket exist before the
	// target starts.
	stopExpose, err := publishExposedPorts(guestHome, config.NvxHome, &netCtx)
	if err != nil {
		LogError("Could not publish a port from the sandbox: %v", err)
		return 1, refusedToStart("a port could not be published from the sandbox")
	}
	defer stopExpose()

	// network.mode loopback reaches host services at their own addresses, over a
	// relay the supervisor installs inside the namespace. This is the parent's
	// half: the socket it carries them to, and the check that only loopback
	// addresses are dialled.
	if loopbackRedirectMode(netCtx.Mode) {
		stopLoopback, lerr := openLoopbackSocket(guestHome, config.NvxHome, &netCtx)
		if lerr != nil {
			LogError("Could not open the loopback path for the sandbox: %v", lerr)
			return 1, refusedToStart("the loopback path could not be opened")
		}
		defer stopLoopback()
	}

	args := []string{
		"__landlock-exec",
		"--guest-home=" + guestHome,
		"--work-dir=" + workDir,
		"--nvx-home=" + config.NvxHome,
		"--network-mode=" + netCtx.Mode,
		"--command=" + config.Command,
		"--egress-socket=" + netCtx.EgressSocketPath,
	}
	for _, m := range netCtx.ConnectPorts {
		args = append(args, fmt.Sprintf("--connect=%d:%d", m.Host, m.Inside))
	}
	// Only the port inside crosses. The supervisor dials the parent's socket for
	// it, and the host port means nothing in there.
	for _, m := range netCtx.ExposePorts {
		args = append(args, "--expose="+strconv.Itoa(m.Container))
	}
	for _, root := range config.ReadExecRoots {
		args = append(args, "--read-exec="+root)
		// Said on Linux as well as Windows. Granting a contained process the right
		// to execute something from outside every default root is worth one line
		// of output, and its absence is how a policy entry that silently did
		// nothing would go unnoticed.
		LogInfo("Sandbox may read and execute from %s", root)
	}
	args = append(args, "--", cmdPath)
	args = append(args, config.Args...)

	cmd := exec.Command(exe, args...)
	cmd.Env = cleanEnv
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if workDir != "" {
		cmd.Dir = workDir
	}

	// Create the namespaces here, as clone flags, rather than having the child
	// unshare itself.
	//
	// unshare(CLONE_NEWNET) moves only the CALLING THREAD, and the Go runtime
	// schedules goroutines across threads freely -- so a self-unsharing child ends
	// up with some of its threads (and anything they open) still in the original
	// namespace. Measured: after an in-process unshare, 52 of 64 goroutines were
	// still in the old namespace and one reached the public internet. Supplying the
	// flag at clone time puts the whole child process in the new namespace from
	// birth, which is deterministic and needs no thread pinning.
	// CLONE_NEWUSER, with this user mapped to root inside it, is what makes the
	// rest of these flags possible without privileges.
	//
	// CLONE_NEWPID and CLONE_NEWNET both require CAP_SYS_ADMIN in the current
	// user namespace. Without CLONE_NEWUSER an ordinary user has none, so the
	// clone failed with EPERM and nvx fail-closed -- the Linux sandbox could not
	// start at all except as root. Measured on WSL2 Ubuntu 24.04 and on a hosted
	// Ubuntu runner: NEWPID|NEWNET is refused, NEWUSER|NEWPID|NEWNET succeeds,
	// for the same user in the same shell.
	//
	// It went unnoticed because every Linux smoke script skips when a namespace
	// cannot be created, so the one condition that proved the sandbox unusable
	// was also the condition that stopped anything checking it.
	//
	// The target-side clone in applyLinuxNamespaces has always done this; only
	// the supervisor's was missing it.
	cmd.SysProcAttr = supervisorSysProcAttr(netCtx.Mode)

	if err := runSupervisor(cmd, guestHome); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return childExitCode(exitErr), nil
		}
		// The common refusal here is a host that will not let the sandbox create
		// its namespaces (default Docker, AppArmor-hardened Ubuntu). Left as the
		// raw "fork/exec ...: operation not permitted" it tells the user nothing;
		// name what happened and point at the diagnosis doctor already prints.
		if namespaceSetupRefused(err, "") {
			LogError("nvx could not create the sandbox to contain this command, so it did not run.")
			LogRefusalDetail("%s", namespaceRefusedHint())
			return 1, refusedToStart("the kernel refused the sandbox its namespaces")
		}
		LogError("Landlock sandbox execution failed: %v", err)
		return 1, refusedToStart("the landlock sandbox could not be launched")
	}
	return 0, nil
}

// runSupervisor runs the supervisor to completion, passing nvx's own
// termination on to it and recording its pid beside nvx's in the session
// records.
//
// Before this, the parent used cmd.Run: a SIGTERM to nvx killed nvx and left the
// supervisor, and with it the contained process, running re-parented to init, and
// the session marker named only the dead nvx, so the next run's cleanup deleted
// the guest home underneath it. The supervisor's Pdeathsig (set in
// supervisorSysProcAttr) covers SIGKILL. Signals that can be caught are forwarded
// instead, so the contained process gets to shut down.
func runSupervisor(cmd *exec.Cmd, guestHome string) error {
	return startChildForwardingSignals(cmd, func(pid int) {
		recordSupervisorPID(guestHome, pid)
	}, supervisorInterruptSignal)
}

// supervisorInterruptSignal is what nvx sends the supervisor to have it interrupt
// the target.
//
// The target is in the terminal's foreground group with nvx and the supervisor, so
// Ctrl-C at a terminal reaches all three from the kernel. The supervisor cannot
// tell that SIGINT from one nvx forwards because the terminal did not deliver it,
// such as `kill -INT` sent to a backgrounded nvx. Passing on both would interrupt
// the target twice. So the supervisor ignores a SIGINT of its own, and nvx asks for
// an interrupt under this number when it has one to forward.
const supervisorInterruptSignal = syscall.SIGUSR1
