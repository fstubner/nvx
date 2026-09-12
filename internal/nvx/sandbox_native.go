package nvx

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// usePersistentProfile reports whether a run should use a persistent per-tool
// guest profile instead of an ephemeral one. ToolName is set (in runShim) only
// for an approved trusted tool, so its presence is the signal.
func usePersistentProfile(toolName string) bool {
	return toolName != ""
}

// runNativeSandbox is the hardened default sandbox: platform-specific OS
// primitives (AppContainer on Windows, Landlock on Linux, Seatbelt on macOS)
// layered on env scrubbing and an ephemeral guest profile.
// exitCode is named so the deferred log rescue can see whether the command
// failed; a successful run's debug logs are noise nobody asks for.
func runNativeSandbox(config SandboxConfig, policy Policy, egress *EgressProxy, netCtx NetworkLaunchContext) (exitCode int) {
	sandboxID, err := generateSandboxID()
	if err != nil {
		LogError("Sandbox initialization failed: %v", err)
		return sandboxDidNotStart(config, "sandbox id could not be generated", 1)
	}

	LogDetail("Sandbox session: %s", sandboxID)

	var guestHome string
	if usePersistentProfile(config.ToolName) {
		scope := projectScopeDir()
		guestHome, err = ensurePersistentGuestProfile(config.NvxHome, scope, config.ToolName)
		if err != nil {
			LogError("Failed to create persistent tool profile: %v", err)
			return sandboxDidNotStart(config, "persistent tool profile could not be created", 1)
		}
		// Persistent: intentionally NOT cleaned up, so credentials survive to
		// the next run. Still fully contained; the real home is never used.
		LogInfo("%q: using a persistent profile for this project (contained; your real home is untouched).", config.ToolName)
	} else {
		guestHome, err = createGuestProfile(config.NvxHome, sandboxID)
		if err != nil {
			LogError("Failed to create sandbox guest profile: %v", err)
			return sandboxDidNotStart(config, "guest profile could not be created", 1)
		}
		// Rescue debug logs before the guest home goes, and only on failure.
		//
		// npm writes its debug log into the cache, which lives in the guest home,
		// so a failed install printed a path that was deleted moments later --
		// exactly when the user wanted to read it. Ordered before the cleanup
		// defer so it runs first: defers run last-in-first-out.
		defer cleanupGuestProfile(config.NvxHome, sandboxID)
		defer func() {
			if exitCode == 0 {
				return
			}
			if dest := rescueSandboxLogs(config.NvxHome, guestHome, sandboxID); dest != "" {
				LogInfo("The sandbox's debug logs were copied out before its home was removed: %s", dest)
			}
		}()
	}

	// Platforms that cut the sandboxed process off from the parent's loopback
	// listeners need the proxy exposed on a UNIX socket inside the guest home:
	// a Linux network namespace has no route out of itself, and a Windows
	// AppContainer is refused loopback without an elevated exemption. No-op
	// elsewhere.
	if err := prepareEgressSocket(egress, guestHome, &netCtx); err != nil {
		// "namespace isolation" until 2026-09-03, which named the Linux mechanism
		// on every platform. The one failure a person actually meets here is a
		// Windows one -- an NVX_HOME too long for an AF_UNIX path -- and it arrived
		// under a heading describing something Windows does not do.
		LogError("Could not put the egress proxy where the sandbox can reach it: %v", err)
		return sandboxDidNotStart(config, "the egress proxy could not be reached from the sandbox", 1)
	}

	scrubbed := scrubEnvironmentAllowing(guestHome, config.PassEnv)
	reportEnvScrub(config.NvxHome, scrubbed)
	cleanEnv := applyProxyEnv(scrubbed.Env, egress)

	cmdPath := resolveSandboxCommand(config, policy)
	if cmdPath == "" {
		return sandboxDidNotStart(config, "the command could not be resolved", 127)
	}

	// The runtime's own bin directory leads the contained PATH, so a nested
	// lookup finds the pinned runtime rather than nvx's shim. See
	// withRuntimeBinOnPath for the install this was measured to break.
	cleanEnv = withRuntimeBinOnPath(cleanEnv, cmdPath, config.NvxHome)

	workDir := config.WorkDir
	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	workDir, _ = filepath.Abs(workDir)

	LogInfo("Running in native sandbox: %s %s", config.Command, strings.Join(config.Args, " "))
	code, err := platformLaunchNative(config, guestHome, workDir, cmdPath, cleanEnv, netCtx)
	if err != nil {
		return sandboxDidNotStart(config, err.Error(), 1)
	}
	return code
}

func resolveSandboxCommand(config SandboxConfig, policy Policy) string {
	rt := runtimeForShim(config.Command)
	pinned := policy.PinnedRuntimeVersion(rt.Name())
	if pinned != "" {
		if policy.Runtime.Command == "" || strings.EqualFold(config.Command, policy.Runtime.Command) {
			if p := resolvePinnedCommandPath(config.Command, config.NvxHome, pinned, rt); p != "" {
				return preferWindowsRuntimeExe(p)
			}
		}
	}

	activeVer := getActiveShellVersionFor(config.NvxHome, rt.Name())
	if activeVer == "" {
		activeVer = getGlobalDefaultVersionFor(config.NvxHome, rt.Name())
	}
	if p := resolvePinnedCommandPath(config.Command, config.NvxHome, activeVer, rt); p != "" {
		return preferWindowsRuntimeExe(p)
	}
	if p := resolveProjectBinCommand(config.Command); p != "" {
		return preferWindowsRuntimeExe(p)
	}

	cmdPath, err := lookPathSkippingNvxShims(config.Command, config.NvxHome)
	if err != nil {
		LogError("Command not found: %s", config.Command)
		return ""
	}
	return preferWindowsRuntimeExe(cmdPath)
}

// supervisorExecArgs is what the parent hands the in-sandbox supervisor.
//
// A struct rather than a return tuple: the parser returned nine values before
// ExposePorts made it ten, and every caller had to spell out the ones it did not
// want as blanks. Two of them already discarded GuestHome that way, which is a
// field this needed back.
type supervisorExecArgs struct {
	GuestHome    string
	WorkDir      string
	NvxHome      string
	NetworkMode  string
	ShimCommand  string
	EgressSocket string
	// ExposePorts are ports inside the sandbox that the parent is publishing on
	// the host's loopback, given as --expose=<port> and repeatable. Windows
	// refuses connections INTO an AppContainer, so reaching them is a reverse
	// tunnel the contained side dials outward; see runExposeTunnels.
	ExposePorts []int
	// ReadExecRoots are extra directories the contained process may read and
	// execute from, given as --read-exec=<abs path> and repeatable.
	ReadExecRoots []string
	// ConnectPorts are host services this sandbox may reach, given as
	// --connect=<hostPort>:<insidePort> and repeatable. Both numbers are decided
	// by the parent, so the supervisor never chooses either.
	ConnectPorts []connectMapping
	CmdPath      string
	CmdArgs      []string
}

// parseSupervisorExecArgs parses internal __landlock-exec / __appcontainer-exec
// arguments. ok is false unless the guest home, work dir and command are all
// present.
func parseSupervisorExecArgs(argv []string) (supervisorExecArgs, bool) {
	var a supervisorExecArgs
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		switch {
		case strings.HasPrefix(arg, "--guest-home="):
			a.GuestHome = strings.TrimPrefix(arg, "--guest-home=")
		case strings.HasPrefix(arg, "--work-dir="):
			a.WorkDir = strings.TrimPrefix(arg, "--work-dir=")
		case strings.HasPrefix(arg, "--nvx-home="):
			a.NvxHome = strings.TrimPrefix(arg, "--nvx-home=")
		case strings.HasPrefix(arg, "--network-mode="):
			a.NetworkMode = strings.TrimPrefix(arg, "--network-mode=")
		case strings.HasPrefix(arg, "--command="):
			a.ShimCommand = strings.TrimPrefix(arg, "--command=")
		case strings.HasPrefix(arg, "--egress-socket="):
			a.EgressSocket = strings.TrimPrefix(arg, "--egress-socket=")
		case strings.HasPrefix(arg, "--connect="):
			if m, err := parseConnectSpec(strings.TrimPrefix(arg, "--connect=")); err == nil && m.Inside != 0 {
				a.ConnectPorts = append(a.ConnectPorts, m)
			}
		case strings.HasPrefix(arg, "--read-exec="):
			if v := strings.TrimPrefix(arg, "--read-exec="); v != "" {
				a.ReadExecRoots = append(a.ReadExecRoots, v)
			}
		case strings.HasPrefix(arg, "--expose="):
			// Ignore anything unparseable rather than failing the launch: these
			// arguments are built by nvx itself a few lines away, so a bad value
			// is a bug here rather than user input, and refusing to start is a
			// worse response to it than running without the tunnel.
			if p, err := strconv.Atoi(strings.TrimPrefix(arg, "--expose=")); err == nil && validExposePort(p) {
				a.ExposePorts = append(a.ExposePorts, p)
			}
		case arg == "--":
			if i+1 < len(argv) {
				a.CmdPath = argv[i+1]
				a.CmdArgs = argv[i+2:]
				return a, a.GuestHome != "" && a.WorkDir != "" && a.CmdPath != ""
			}
			return supervisorExecArgs{}, false
		}
	}
	return supervisorExecArgs{}, false
}

// validExposePort rejects what cannot be a listening TCP port. 0 is excluded
// deliberately: it means "pick one" to the kernel, and a tunnel to a port nobody
// can predict is not something the parent can publish.
func validExposePort(p int) bool { return p > 0 && p < 65536 }

// exposeMapping is one published port: the port a server listens on INSIDE the
// sandbox, and the port the host reaches it on.
//
// They cannot be the same number, which is not a stylistic choice. An
// AppContainer shares the host's network stack rather than getting its own the
// way a Linux network namespace does -- so a port bound inside the container
// occupies it for the host too, and the parent's listener and the contained
// server collide on it. Measured: with both on 51733 the contained server died
// with EADDRINUSE, having lost the race to the parent, which binds first.
//
// Host 0 means "pick a free one and report it".
type exposeMapping struct {
	Container int
	Host      int
}

// parseExposeSpec reads "5173" or "5173:8080" as container[:host].
//
// Ordered container-first to match `docker -p`'s mental model being the other
// way round on purpose: docker writes host:container because the host is what
// you type in a browser. Here the container port is the one a developer knows
// from their dev server's own output, and the host port is the part nvx can
// choose, so the known value comes first and the optional one second.
func parseExposeSpec(s string) (exposeMapping, error) {
	s = strings.TrimSpace(s)
	container, host, hasHost := strings.Cut(s, ":")
	c, err := strconv.Atoi(strings.TrimSpace(container))
	if err != nil || !validExposePort(c) {
		return exposeMapping{}, fmt.Errorf("%q is not a TCP port between 1 and 65535", s)
	}
	m := exposeMapping{Container: c}
	if hasHost {
		h, herr := strconv.Atoi(strings.TrimSpace(host))
		if herr != nil || !validExposePort(h) {
			return exposeMapping{}, fmt.Errorf("%q has an unusable host port", s)
		}
		if h == c {
			return exposeMapping{}, fmt.Errorf(
				"%q maps a port to itself; an AppContainer shares the host's network stack, so the "+
					"contained server and the published port cannot both hold %d", s, c)
		}
		m.Host = h
	}
	return m, nil
}

// normalizeExposePorts parses entries, dropping bad ones with a warning and
// de-duplicating by container port. A policy file is user input, so a typo
// should say so rather than be ignored silently or take the launch down.
func normalizeExposePorts(specs []string) []exposeMapping {
	var out []exposeMapping
	seen := map[int]bool{}
	for _, s := range specs {
		m, err := parseExposeSpec(s)
		if err != nil {
			LogWarn("Ignoring expose_ports entry: %v.", err)
			continue
		}
		if seen[m.Container] {
			continue
		}
		seen[m.Container] = true
		out = append(out, m)
	}
	return out
}

// connectMapping is one service on the host that the sandbox may reach: the port
// it actually listens on, and the port the contained process dials to get there.
//
// Two numbers for the same reason exposeMapping needs two. The container shares
// the host's network stack, so nvx cannot put its in-sandbox listener on the
// same port the real service occupies. The contained tool therefore has to be
// told which local port to use; nvx publishes that in the environment as
// NVX_CONNECT_<hostPort> so a wrapper script or a tool that reads its endpoint
// from the environment needs no hardcoding.
//
// Inside 0 means "pick a free one and report it".
type connectMapping struct {
	Host   int
	Inside int
}

// connectEnvVar names the variable that tells the contained tool where to dial.
//
// The in-sandbox port cannot be the host's, so a tool cannot simply use the
// number its documentation gives. Publishing it in the environment means a
// wrapper script or a tool that reads its endpoint from configuration needs
// nothing hardcoded: NVX_CONNECT_9222=19222 for `--connect 9222`.
func connectEnvVar(hostPort int) string {
	return "NVX_CONNECT_" + strconv.Itoa(hostPort)
}

// connectRefusalFor reports why --connect cannot be honoured for this
// combination, or "" when it can. A pure function for the reason dockerRunArgs
// and seccompFilterForMode are: the decision is reachable in a test on any
// machine, where the code path around it needs Docker installed, a sandbox that
// starts, and the right operating system.
//
// Nothing here is a refusal to RUN. The command still runs contained; what the
// caller loses is the one host service they named, which they are told about
// rather than left to find.
func connectRefusalFor(provider, goos, mode string) (warn, hint string) {
	if strings.EqualFold(strings.TrimSpace(provider), "docker") {
		// Structural, and not a `docker run` flag away. The container gets a
		// network namespace of its own, so reaching a host service means relaying
		// across that boundary -- and the relay's in-sandbox half is a process nvx
		// runs INSIDE the sandbox, which this provider does not have: it launches
		// the target command as the container's only process. In offline and
		// loopback, the two modes it enforces, `--network none` leaves the
		// container nothing but its own loopback anyway.
		return "--connect is not carried by the docker provider; the sandbox cannot reach 127.0.0.1 on your machine.",
			"Use the native provider (isolation.filesystem.provider, or --filesystem-provider=native) for a run that needs a host service."
	}
	// Linux only. Windows and macOS carry --connect in every mode, because
	// neither one's containment refuses the contained process a loopback socket:
	// the AppContainer tunnel and the Seatbelt relay are both independent of the
	// network mode. Linux's seccomp filter is not.
	if goos == "linux" && connectUnsupportedForMode(mode) {
		return fmt.Sprintf("--connect cannot be honoured in network.mode %q on Linux: that mode denies the sandbox every IP socket, including the one it would use to reach the tunnel.", mode),
			`Use network.mode "proxy" (the default) or "open" for this run.`
	}
	return "", ""
}

// connectUnsupportedForMode reports the Linux network modes whose seccomp filter
// denies the sandbox the socket --connect needs. Lives here rather than in
// sandbox_connect_linux.go because the dispatcher that warns compiles everywhere.
//
// offline and loopback both install buildOfflineNetworkFilter, which refuses
// connect() outright and refuses to create any AF_INET or AF_INET6 socket. A
// contained tool therefore cannot dial the in-namespace listener at all, and
// nothing on nvx's side of the boundary can change that. Making it work would
// mean granting those modes an IP socket, which is the thing they exist to
// withhold -- so the flag is refused out loud instead.
//
// Trimmed as well as lowercased, like every other reader of this field: a policy
// carrying "offline " with a trailing space was once enough to make a mode mean
// something else entirely.
func connectUnsupportedForMode(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "offline", "loopback":
		return true
	}
	return false
}

// parseConnectSpec reads "9222" or "9222:19222" as host[:inside].
//
// Host-first, the mirror of parseExposeSpec's container-first: in both cases the
// number the developer already knows comes first, and the one nvx can choose
// comes second. For --expose that is the port their dev server prints; here it
// is the port the service on their machine is already listening on.
func parseConnectSpec(s string) (connectMapping, error) {
	s = strings.TrimSpace(s)
	host, inside, hasInside := strings.Cut(s, ":")
	h, err := strconv.Atoi(strings.TrimSpace(host))
	if err != nil || !validExposePort(h) {
		return connectMapping{}, fmt.Errorf("%q is not a TCP port between 1 and 65535", s)
	}
	m := connectMapping{Host: h}
	if hasInside {
		in, ierr := strconv.Atoi(strings.TrimSpace(inside))
		if ierr != nil || !validExposePort(in) {
			return connectMapping{}, fmt.Errorf("%q has an unusable in-sandbox port", s)
		}
		if in == h {
			return connectMapping{}, fmt.Errorf(
				"%q maps a port to itself; the container shares the host's network stack, so the "+
					"in-sandbox listener and the real service cannot both hold %d", s, h)
		}
		m.Inside = in
	}
	return m, nil
}

// normalizeConnectPorts parses entries, dropping bad ones with a warning and
// de-duplicating by host port.
func normalizeConnectPorts(specs []string) []connectMapping {
	var out []connectMapping
	seen := map[int]bool{}
	for _, s := range specs {
		m, err := parseConnectSpec(s)
		if err != nil {
			LogWarn("Ignoring connect_ports entry: %v.", err)
			continue
		}
		if seen[m.Host] {
			continue
		}
		seen[m.Host] = true
		out = append(out, m)
	}
	return out
}

// loopbackRedirectMode reports the mode whose meaning is "the services on this
// machine are reachable". Portable, because the parent's half of the Linux
// redirect is decided in a file that compiles everywhere.
//
// Trimmed and lowercased, like every other reader of this field.
func loopbackRedirectMode(mode string) bool {
	return strings.ToLower(strings.TrimSpace(mode)) == "loopback"
}

// portOfAddr pulls the port out of a "host:port" the relay reported, returning 0
// when there is none. 0 is skipped by the rule builder rather than excluded, so
// an absent proxy relay costs nothing.
func portOfAddr(addr string) int {
	if addr == "" {
		return 0
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		return 0
	}
	return p
}
