//go:build darwin

package main

import (
	"bytes"
	"io"
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

	profile := buildSeatbeltProfile(netCtx, guestHome, workDir, config.NvxHome, filepath.Dir(cmdPath))
	profileFile, err := os.CreateTemp("", "nvx-*.sb")
	if err != nil {
		LogError("Failed to create Seatbelt profile file: %v", err)
		return 1
	}
	profilePath := profileFile.Name()
	defer os.Remove(profilePath)
	if _, err := profileFile.Write([]byte(profile)); err != nil {
		profileFile.Close()
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

	var errBuf bytes.Buffer
	cmd := exec.Command(sandboxExec, args...)
	cmd.Env = cleanEnv
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = io.MultiWriter(os.Stderr, &errBuf)
	if workDir != "" {
		cmd.Dir = workDir
	}

	LogInfo("macOS Seatbelt isolation active")
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			// A non-zero exit with no child output usually means sandbox-exec
			// itself rejected the launch (bad profile / unresolved command).
			// Surface the details so failures are diagnosable, not silent.
			if errBuf.Len() == 0 {
				LogError("Sandboxed command exited %d with no output (command=%q, profile=%s).", exitErr.ExitCode(), cmdPath, profilePath)
			}
			return exitErr.ExitCode()
		}
		LogError("Seatbelt execution failed: %v", err)
		return 1
	}
	return 0
}
