package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// runSeatbeltSandbox wraps execution with macOS sandbox-exec (Seatbelt).
func runSeatbeltSandbox(config SandboxConfig, netCtx NetworkLaunchContext) int {
	if runtime.GOOS != "darwin" {
		LogError("The 'sandbox-exec' isolation provider is only available on macOS.")
		return 1
	}
	sandboxExec := "/usr/bin/sandbox-exec"
	if _, err := os.Stat(sandboxExec); err != nil {
		LogError("sandbox-exec not found at %s.", sandboxExec)
		return 1
	}

	sandboxID, err := generateSandboxID()
	if err != nil {
		LogError("Sandbox initialization failed: %v", err)
		return 1
	}

	guestHome, err := createGuestProfile(config.NvxHome, sandboxID)
	if err != nil {
		LogError("Failed to create sandbox guest profile: %v", err)
		return 1
	}
	defer cleanupGuestProfile(config.NvxHome, sandboxID)

	scrubbed := scrubEnvironmentAllowing(guestHome, config.PassEnv)
	reportEnvScrub(config.NvxHome, scrubbed)
	cleanEnv := scrubbed.Env

	cwd := config.WorkDir
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	cmdPath, err := exec.LookPath(config.Command)
	if err != nil {
		LogError("Command not found: %s", config.Command)
		return 127
	}

	// Only the guest home and the working directory are writable — matching
	// the Windows AppContainer and Linux Landlock write scope. nvxHome (and
	// therefore versions/*/npm_global, grants/, policy.json) and the runtime
	// binary's own directory must NOT be writable: this profile used to pass
	// both as writable roots, which let any sandboxed process rewrite the
	// global policy, self-approve grants, or trojan the node/npm binaries
	// themselves — a full, persistent sandbox defeat. Reads remain broad
	// (file-read* below) so the dynamic linker and tooling can still find
	// everything they need; only writes are scoped down.
	profile := buildSeatbeltProfile(netCtx, guestHome, cwd)
	profileFile, err := os.CreateTemp("", "nvx-*.sb")
	if err != nil {
		LogError("Failed to create Seatbelt profile file: %v", err)
		return 1
	}
	profilePath := profileFile.Name()
	defer os.Remove(profilePath)
	if _, err := profileFile.Write([]byte(profile)); err != nil {
		profileFile.Close() // #nosec G104 -- the write error below is what matters; a close error on top of it adds nothing
		LogError("Failed to write Seatbelt profile: %v", err)
		return 1
	}
	if err := profileFile.Close(); err != nil {
		LogError("Failed to close Seatbelt profile file: %v", err)
		return 1
	}
	if err := os.Chmod(profilePath, 0600); err != nil {
		LogError("Failed to set permissions on Seatbelt profile file: %v", err)
		return 1
	}

	args := []string{"-f", profilePath, cmdPath}
	args = append(args, config.Args...)

	cmd := exec.Command(sandboxExec, args...)
	cmd.Env = cleanEnv
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if config.WorkDir != "" {
		cmd.Dir = config.WorkDir
	}

	LogInfo("Running in Seatbelt sandbox (session %s): %s %s", sandboxID, config.Command, strings.Join(config.Args, " "))
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		LogError("Seatbelt execution failed: %v", err)
		return 1
	}
	return 0
}

// buildSeatbeltProfile renders the Seatbelt policy. The writable roots are named
// parameters rather than a variadic tail on purpose: the tail let one caller pass
// nvxHome and the runtime binary directory as writable while the other passed the
// intended two, and the compiler had no reason to object. Adding a writable root
// should now require editing this signature and every caller with it.
func buildSeatbeltProfile(netCtx NetworkLaunchContext, guestHome, workDir string) string {
	writeRoots := append([]string{
		"/dev",
		"/private/tmp",
		"/private/var/tmp",
		"/private/var/folders",
	}, sandboxWritableRoots(guestHome, workDir)...)

	var b strings.Builder
	b.WriteString("(version 1)\n")
	b.WriteString("(deny default)\n")
	b.WriteString("(allow process*)\n")
	b.WriteString("(allow signal (target self))\n")
	b.WriteString("(allow sysctl-read)\n")
	b.WriteString("(allow file-read-metadata)\n")
	// Process-launch primitives. Modern macOS (especially Apple Silicon) kills a
	// process during dynamic linking if it cannot reach system Mach services or
	// map the shared cache, so a default-deny profile must permit these for any
	// binary to start. They do not weaken the filesystem-write or egress
	// containment, which are nvx's actual guarantees.
	b.WriteString("(allow mach-lookup)\n")
	b.WriteString("(allow ipc-posix-shm*)\n")
	b.WriteString("(allow iokit-open)\n")
	// Mapping a file's pages as executable is a distinct Seatbelt operation from
	// reading it; under (deny default) the linker can read but not execute its
	// libraries, so the process is killed during load. Required to run any binary.
	b.WriteString("(allow file-map-executable)\n")
	// Reads are allowed broadly. The dynamic linker must read system libraries
	// and the dyld shared cache, whose paths vary by macOS version (e.g. the
	// Cryptexes firmlink on Apple Silicon) and are impractical to enumerate
	// reliably. nvx's enforced guarantees are filesystem-WRITE containment and
	// egress control, both kept strict below; environment secrets are separately
	// scrubbed and $HOME is redirected to an ephemeral guest profile.
	b.WriteString("(allow file-read*)\n")
	b.WriteString("(allow file-write*\n")
	for _, root := range dedupeStrings(writeRoots) {
		if root == "" {
			continue
		}
		fmt.Fprintf(&b, "  (subpath %q)\n", root)
	}
	b.WriteString(")\n")

	// Trimmed, like every other reader of this field (policy.go, egress_proxy.go,
	// sandbox_native_windows.go, fs_provider.go). Without it a policy carrying
	// "mode": "proxy " was proxy on Windows and Linux and matched no case here, so
	// macOS silently emitted no network rule at all -- fail-closed, but a
	// platform-divergent behaviour change from one trailing space in a config file.
	mode := strings.ToLower(strings.TrimSpace(netCtx.Mode))
	if mode == "open" || mode == "" {
		b.WriteString("(allow network*)\n")
	}
	// Loopback is granted per mode, narrowly.
	//
	// Until 2026-08-20 every restricted mode emitted `(allow network-outbound
	// (remote tcp "localhost:*"))`, which let contained code reach every service on
	// the developer's machine -- their database, their other projects' dev servers
	// -- with no allowlist entry, and made `network.mode: offline` not offline. The
	// two per-port rules below it were dead: the wildcard already permitted them.
	//
	// It is the same exposure as the Windows loopback exemption, and worth being
	// precise about why it is worse than it sounds: any reachable service that
	// forwards traffic (a debugging proxy, `ssh -D`, a dev server's proxy route)
	// turns it into unrestricted egress, so the allowlist stops meaning anything.
	switch mode {
	case "proxy":
		// Only the proxy itself. If its ports are unknown, nothing is allowed and
		// egress fails closed rather than falling back to all of loopback.
		if netCtx.HTTPProxyPort > 0 {
			fmt.Fprintf(&b, "(allow network-outbound (remote tcp \"localhost:%d\"))\n", netCtx.HTTPProxyPort)
		}
		if netCtx.SOCKSProxyPort > 0 {
			fmt.Fprintf(&b, "(allow network-outbound (remote tcp \"localhost:%d\"))\n", netCtx.SOCKSProxyPort)
		}
	case "loopback":
		// This mode's entire meaning is "loopback is reachable", so the wildcard is
		// correct here and only here. It is not the default.
		b.WriteString("(allow network-outbound (remote tcp \"localhost:*\"))\n")
		b.WriteString("(allow network-outbound (remote udp \"localhost:*\"))\n")
		b.WriteString("(allow network-bind (local tcp \"localhost:*\"))\n")
		b.WriteString("(allow network-bind (local udp \"localhost:*\"))\n")
	case "offline":
		// Nothing. `(deny default)` above already refuses everything; emitting no
		// network rule at all is what makes offline mean offline.
	}

	return b.String()
}
