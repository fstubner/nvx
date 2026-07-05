package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

	cleanEnv := scrubEnvironment(guestHome)

	cwd := config.WorkDir
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	cmdPath, err := exec.LookPath(config.Command)
	if err != nil {
		LogError("Command not found: %s", config.Command)
		return 127
	}

	profilePath := filepath.Join(guestHome, "nvx.sb")
	profile := buildSeatbeltProfile(netCtx, guestHome, cwd, config.NvxHome, filepath.Dir(cmdPath))
	if err := os.WriteFile(profilePath, []byte(profile), 0600); err != nil {
		LogError("Failed to write Seatbelt profile: %v", err)
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

// buildSeatbeltProfile generates a Seatbelt policy with filesystem and optional network rules.
func buildSeatbeltProfile(netCtx NetworkLaunchContext, writableRoots ...string) string {
	writeRoots := append([]string{
		"/dev",
		"/private/tmp",
		"/private/var/tmp",
		"/private/var/folders",
	}, writableRoots...)
	readRoots := append([]string{
		"/bin",
		"/sbin",
		"/usr",
		"/System",
		"/Library",
		"/opt",
		"/dev",
		"/private/tmp",
		"/private/var/tmp",
		"/private/var/folders",
	}, writableRoots...)

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
	b.WriteString("(allow file-read*\n")
	for _, root := range dedupeStrings(readRoots) {
		if root == "" {
			continue
		}
		fmt.Fprintf(&b, "  (subpath %q)\n", root)
	}
	b.WriteString(")\n")
	b.WriteString("(allow file-write*\n")
	for _, root := range dedupeStrings(writeRoots) {
		if root == "" {
			continue
		}
		fmt.Fprintf(&b, "  (subpath %q)\n", root)
	}
	b.WriteString(")\n")

	mode := strings.ToLower(netCtx.Mode)
	if mode == "open" || mode == "" {
		b.WriteString("(allow network*)\n")
	}
	if mode == "proxy" || mode == "offline" || mode == "loopback" {
		b.WriteString("(allow network-outbound (remote tcp \"localhost:*\"))\n")
		b.WriteString("(allow network-outbound (remote udp \"localhost:*\"))\n")
		b.WriteString("(allow network-bind (local tcp \"localhost:*\"))\n")
		b.WriteString("(allow network-bind (local udp \"localhost:*\"))\n")
		if netCtx.HTTPProxyPort > 0 {
			fmt.Fprintf(&b, "(allow network-outbound (remote tcp \"localhost:%d\"))\n", netCtx.HTTPProxyPort)
		}
		if netCtx.SOCKSProxyPort > 0 {
			fmt.Fprintf(&b, "(allow network-outbound (remote tcp \"localhost:%d\"))\n", netCtx.SOCKSProxyPort)
		}
	}

	return b.String()
}
