//go:build darwin

package main

import (
	"os"
	"os/exec"
	"path/filepath"
)

// platformLaunchNative runs the command under sandbox-exec (Seatbelt) with
// filesystem write restrictions — this is the default native path on macOS.
func platformLaunchNative(config SandboxConfig, guestHome, workDir, cmdPath string, cleanEnv []string, netCtx NetworkLaunchContext) int {
	sandboxExec := "/usr/bin/sandbox-exec"
	if _, err := os.Stat(sandboxExec); err != nil {
		LogError("native sandbox requires sandbox-exec at %s.", sandboxExec)
		return 1
	}

	profilePath := filepath.Join(guestHome, "nvx.sb")
	profile := buildSeatbeltProfile(netCtx, guestHome, workDir)
	if err := os.WriteFile(profilePath, []byte(profile), 0644); err != nil {
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
	if workDir != "" {
		cmd.Dir = workDir
	}

	LogInfo("macOS Seatbelt isolation active")
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		LogError("Seatbelt execution failed: %v", err)
		return 1
	}
	return 0
}
