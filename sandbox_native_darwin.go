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

	profilePath := filepath.Join(guestHome, "nvx.sb")
	profile := buildSeatbeltProfile(netCtx, guestHome, workDir, config.NvxHome, filepath.Dir(cmdPath))
	if err := os.WriteFile(profilePath, []byte(profile), 0600); err != nil {
		LogError("Failed to write Seatbelt profile: %v", err)
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
