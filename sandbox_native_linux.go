//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
)

// platformLaunchNative re-execs nvx as a Landlock child so restrictions are
// applied in the process that runs the target command.
func platformLaunchNative(config SandboxConfig, guestHome, workDir, cmdPath string, cleanEnv []string, netCtx NetworkLaunchContext) int {
	exe, err := os.Executable()
	if err != nil {
		LogError("Failed to resolve nvx executable: %v", err)
		return 1
	}

	args := []string{
		"__landlock-exec",
		"--guest-home=" + guestHome,
		"--work-dir=" + workDir,
		"--nvx-home=" + config.NvxHome,
		"--network-mode=" + netCtx.Mode,
		fmt.Sprintf("--proxy-port=%d", netCtx.HTTPProxyPort),
		"--",
		cmdPath,
	}
	args = append(args, config.Args...)

	cmd := exec.Command(exe, args...)
	cmd.Env = cleanEnv
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if workDir != "" {
		cmd.Dir = workDir
	}

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		LogError("Landlock sandbox execution failed: %v", err)
		return 1
	}
	return 0
}
