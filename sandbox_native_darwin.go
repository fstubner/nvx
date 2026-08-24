//go:build darwin

package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
)

// seatbeltExecPath is where macOS keeps sandbox-exec. A variable rather than a
// constant so a test can point it at a path that does not exist and check that
// nvx refuses to run instead of running uncontained -- the one macOS
// fail-closed claim that could not be verified while this was inlined, since
// the real file cannot be removed from a running system.
var seatbeltExecPath = "/usr/bin/sandbox-exec"

// platformLaunchNative runs the command under sandbox-exec (Seatbelt) with
// filesystem write restrictions — this is the default native path on macOS.
func platformLaunchNative(config SandboxConfig, guestHome, workDir, cmdPath string, cleanEnv []string, netCtx NetworkLaunchContext) int {
	if _, err := os.Stat(seatbeltExecPath); err != nil {
		LogError("native sandbox requires sandbox-exec at %s.", seatbeltExecPath)
		return 1
	}
	sandboxExec := seatbeltExecPath

	// Only the guest home and the working directory are writable. This used to also
	// pass config.NvxHome and the runtime binary's directory, which let any
	// sandboxed process rewrite policy.json, self-approve grants, poison
	// npm_global, read and rewrite tool_home credentials, or trojan the node
	// binary itself -- a persistent sandbox defeat on the DEFAULT macOS path. The
	// legacy caller in sandbox_seatbelt.go was fixed in July; this one was missed,
	// so the comment there described a guarantee the shipped path did not provide.
	profile := buildSeatbeltProfile(netCtx, guestHome, workDir)
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
