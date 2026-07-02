package main

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// runNspawnSandbox runs the command inside a systemd-nspawn container with a
// volatile overlay root: the guest reads the host filesystem, but writes
// outside the explicitly bound working directory are discarded on exit.
// systemd-nspawn requires root privileges; nvx fails closed otherwise.
func runNspawnSandbox(config SandboxConfig) int {
	if runtime.GOOS != "linux" {
		LogError("The 'systemd-nspawn' isolation provider is only available on Linux.")
		return 1
	}
	nspawnPath, err := exec.LookPath("systemd-nspawn")
	if err != nil {
		LogError("systemd-nspawn not found. Install the systemd-container package to use this provider.")
		return 1
	}
	if os.Geteuid() != 0 {
		LogError("The 'systemd-nspawn' provider requires root privileges. Re-run with sudo, or use the 'native' or 'docker' provider.")
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

	args := []string{
		"--quiet",
		"--register=no",
		"--as-pid2",
		"--directory=/",
		"--volatile=overlay",
		"--bind=" + guestHome,
		"--bind=" + cwd,
		"--chdir=" + cwd,
	}
	for _, envVar := range cleanEnv {
		args = append(args, "--setenv="+envVar)
	}
	args = append(args, "--", config.Command)
	args = append(args, config.Args...)

	cmd := exec.Command(nspawnPath, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	LogInfo("Running in systemd-nspawn sandbox (session %s): %s %s", sandboxID, config.Command, strings.Join(config.Args, " "))
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		LogError("systemd-nspawn execution failed: %v", err)
		return 1
	}
	return 0
}
