//go:build !windows && !linux && !darwin

package main

import (
	"os"
	"os/exec"
)

func platformLaunchNative(config SandboxConfig, guestHome, workDir, cmdPath string, cleanEnv []string, netCtx NetworkLaunchContext) int {
	cmd := exec.Command(cmdPath, config.Args...)
	cmd.Env = cleanEnv
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if workDir != "" {
		cmd.Dir = workDir
	}
	applySandboxIsolation(cmd, guestHome)
	defer closeTokenHandle(cmd)

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		LogError("Sandbox execution failed: %v", err)
		return 1
	}
	return 0
}
