package main

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// runWslSandbox executes the command inside the user's default WSL
// distribution with a scrubbed environment and an ephemeral Linux-side guest
// home. This is weaker isolation than WSL Containers (wslc): the process
// shares the distro's kernel namespace and installed packages. Use wslc when
// Hyper-V container isolation is available.
func runWslSandbox(config SandboxConfig) int {
	if runtime.GOOS != "windows" {
		LogError("The 'wsl' isolation provider is only available on Windows.")
		return 1
	}
	wslPath, err := exec.LookPath("wsl.exe")
	if err != nil {
		LogError("wsl.exe not found. Install the Windows Subsystem for Linux to use the 'wsl' provider.")
		return 1
	}

	sandboxID, err := generateSandboxID()
	if err != nil {
		LogError("Sandbox initialization failed: %v", err)
		return 1
	}
	guestHome := "/tmp/nvx_sandbox_" + sandboxID

	cwd := config.WorkDir
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	// Start from an empty environment (env -i) and set only known-safe
	// variables, mirroring scrubEnvironment for the Linux guest side.
	envVars := []string{
		"HOME=" + guestHome,
		"TMPDIR=" + guestHome + "/tmp",
		"XDG_CONFIG_HOME=" + guestHome + "/.config",
		"XDG_CACHE_HOME=" + guestHome + "/.cache",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"TERM=xterm-256color",
		"NVX_SANDBOX=1",
	}

	// The wrapper creates the guest home, runs the requested command, and
	// removes the guest home afterwards while preserving the exit status.
	wrapper := `mkdir -p "$HOME/tmp" "$HOME/.config" "$HOME/.cache"; "$@"; status=$?; rm -rf "$HOME"; exit $status`

	// --exec launches the binary directly, preserving argv boundaries.
	// (Plain `wsl -- ...` space-joins the arguments and re-parses them
	// through the distribution's default shell, which mangles quoting.)
	args := []string{"--cd", cwd, "--exec", "/usr/bin/env", "-i"}
	args = append(args, envVars...)
	args = append(args, "/bin/sh", "-c", wrapper, "sh", config.Command)
	args = append(args, config.Args...)

	cmd := exec.Command(wslPath, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	LogInfo("Running in WSL sandbox (session %s): %s %s", sandboxID, config.Command, strings.Join(config.Args, " "))
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		LogError("WSL execution failed: %v. Make sure a WSL distribution is installed and running.", err)
		return 1
	}
	return 0
}
