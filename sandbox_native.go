package main

import (
	"fmt"
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
		return 1
	}

	LogInfo("Sandbox session: %s", sandboxID)

	var guestHome string
	if usePersistentProfile(config.ToolName) {
		scope := projectScopeDir()
		guestHome, err = ensurePersistentGuestProfile(config.NvxHome, scope, config.ToolName)
		if err != nil {
			LogError("Failed to create persistent tool profile: %v", err)
			return 1
		}
		// Persistent: intentionally NOT cleaned up, so credentials survive to
		// the next run. Still fully contained; the real home is never used.
		LogInfo("%q: using a persistent profile for this project (contained; your real home is untouched).", config.ToolName)
	} else {
		guestHome, err = createGuestProfile(config.NvxHome, sandboxID)
		if err != nil {
			LogError("Failed to create sandbox guest profile: %v", err)
			return 1
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

	// Platforms that place the sandboxed process in a network namespace need the
	// parent's proxy exposed on a UNIX socket inside the guest home, since a
	// namespace-local TCP address cannot reach out. No-op elsewhere.
	if err := prepareEgressSocket(egress, guestHome, &netCtx); err != nil {
		LogError("Egress proxy setup for namespace isolation failed: %v", err)
		return 1
	}

	cleanEnv := scrubEnvironment(guestHome)
	cleanEnv = applyProxyEnv(cleanEnv, egress)

	cmdPath := resolveSandboxCommand(config, policy)
	if cmdPath == "" {
		return 127
	}

	workDir := config.WorkDir
	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	workDir, _ = filepath.Abs(workDir)

	LogInfo("Running in native sandbox: %s %s", config.Command, strings.Join(config.Args, " "))
	return platformLaunchNative(config, guestHome, workDir, cmdPath, cleanEnv, netCtx)
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
	CmdPath     string
	CmdArgs     []string
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
