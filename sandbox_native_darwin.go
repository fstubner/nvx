//go:build darwin

package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
)

// platformLaunchNative runs the command under sandbox-exec (Seatbelt) with
// filesystem write restrictions — this is the default native path on macOS.
func platformLaunchNative(config SandboxConfig, guestHome, workDir, cmdPath string, cleanEnv []string, netCtx NetworkLaunchContext) (int, error) {
	if _, err := os.Stat(seatbeltExecPath); err != nil {
		LogError("native sandbox requires sandbox-exec at %s.", seatbeltExecPath)
		return 1, refusedToStart("sandbox-exec is not present on this machine")
	}
	sandboxExec := seatbeltExecPath

	// Before the profile is rendered: the relays resolve the in-sandbox ports the
	// profile has to name. runSeatbeltSandbox does the same, and both are written
	// out rather than shared, because the shared thing they would call is three
	// lines and the two launch paths differ in everything around them. The
	// comment below is what happens when one of them is changed and the other is
	// not, so a future change here belongs there too.
	connectEnv, stopConnect, err := startSeatbeltConnectRelays(&netCtx)
	if err != nil {
		LogError("Could not open a path to a host service for the sandbox: %v", err)
		return 1, refusedToStart("a path to a host service could not be opened")
	}
	defer stopConnect()
	cleanEnv = append(cleanEnv, connectEnv...)

	// Only the guest home and the working directory are writable. This used to also
	// pass config.NvxHome and the runtime binary's directory, which let any
	// sandboxed process rewrite policy.json, self-approve grants, poison
	// npm_global, read and rewrite tool_home credentials, or trojan the node
	// binary itself -- a persistent sandbox defeat on the DEFAULT macOS path. The
	// legacy caller in sandbox_seatbelt.go was fixed in July; this one was missed,
	// so the comment there described a guarantee the shipped path did not provide.
	profile := buildSeatbeltProfile(netCtx, guestHome, workDir)
	// Under ~/.nvx, which the profile does not grant writes to; see
	// writeSeatbeltProfile for what $TMPDIR allowed.
	profilePath, removeProfile, err := writeSeatbeltProfile(config.NvxHome, profile)
	if err != nil {
		LogError("Failed to write the Seatbelt profile: %v", err)
		return 1, refusedToStart("the seatbelt profile could not be written")
	}
	defer removeProfile()

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
	// Not cmd.Run: a signalled nvx has to take the sandboxed process with it.
	// See runChildForwardingSignals for what that covers and what it cannot.
	if err := runChildForwardingSignals(cmd); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			// A non-zero exit with no child output usually means sandbox-exec
			// itself rejected the launch (bad profile / unresolved command).
			// Surface the details so failures are diagnosable, not silent.
			if errBuf.Len() == 0 {
				LogError("Sandboxed command exited %d with no output (command=%q, profile=%s).", exitErr.ExitCode(), cmdPath, profilePath)
			}
			return exitErr.ExitCode(), nil
		}
		LogError("Seatbelt execution failed: %v", err)
		return 1, refusedToStart("the seatbelt sandbox could not be launched")
	}
	return 0, nil
}
