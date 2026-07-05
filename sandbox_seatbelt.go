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

	profilePath := filepath.Join(guestHome, "nvx.sb")
	profile := buildSeatbeltProfile(netCtx, guestHome, cwd)
	if err := os.WriteFile(profilePath, []byte(profile), 0644); err != nil {
		LogError("Failed to write Seatbelt profile: %v", err)
		return 1
	}

	cmdPath, err := exec.LookPath(config.Command)
	if err != nil {
		LogError("Command not found: %s", config.Command)
		return 127
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
	roots := append([]string{
		"/dev",
		"/private/tmp",
		"/private/var/tmp",
		"/private/var/folders",
	}, writableRoots...)

	var b strings.Builder
	b.WriteString("(version 1)\n")
	b.WriteString("(allow default)\n")
	b.WriteString("(deny file-write*)\n")
	b.WriteString("(allow file-write*\n")
	for _, root := range roots {
		if root == "" {
			continue
		}
		fmt.Fprintf(&b, "  (subpath %q)\n", root)
	}
	b.WriteString(")\n")

	// Read confinement: deny reading the real user's HOME (where ~/.ssh, cloud
	// creds, tokens, browser data, and other repos live), then re-allow only the
	// safe, needed spots. System paths (/usr, /System, /Library, /private) remain
	// readable under (allow default) so the runtime can load. This makes the
	// sensitive area deny-by-default for reads instead of relying on a blocklist.
	// Seatbelt is last-match-wins, so the re-allows below override the home deny.
	// NOTE: validate on macOS before relying on it; carve-outs are conservative.
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		fmt.Fprintf(&b, "(deny file-read* (subpath %q))\n", home)
		b.WriteString("(allow file-read*\n")
		// The redirected guest HOME and the working directory (passed as writable
		// roots) must stay readable even when they live under the real home.
		for _, root := range writableRoots {
			if root == "" {
				continue
			}
			fmt.Fprintf(&b, "  (subpath %q)\n", root)
		}
		// Registry config that installs legitimately need.
		fmt.Fprintf(&b, "  (literal %q)\n", filepath.Join(home, ".npmrc"))
		b.WriteString(")\n")
	}
	// Also deny credential stores that live OUTSIDE the user home (system keychain,
	// host SSH keys) — these aren't covered by the home deny above.
	b.WriteString("(deny file-read*\n")
	b.WriteString("  (subpath \"/Library/Keychains\")\n")
	b.WriteString("  (subpath \"/private/etc/ssh\")\n")
	b.WriteString(")\n")

	mode := strings.ToLower(netCtx.Mode)
	if mode == "proxy" || mode == "offline" || mode == "loopback" {
		b.WriteString("(deny network*)\n")
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
