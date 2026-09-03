//go:build !windows && !linux && !darwin

package main

import "runtime"

// Platforms with no OS-native containment refuse to run rather than running the
// command unprotected.
//
// This path used to execute the command with no isolation at all: it called an
// applySandboxIsolation that set a process group and logged "using environment
// isolation only", then ran it. So on FreeBSD or any other unlisted Unix, nvx
// printed "Running in native sandbox" and gave a contained install full access to
// the user's home, their SSH keys and the network -- the exact outcome the sandbox
// exists to prevent, under a message saying it was preventing it.
//
// A scrubbed environment and a redirected HOME are worth having, but they are
// conventions the child can ignore: nothing stops it reading $HOME's real path
// from /etc/passwd or opening a socket. Calling that a sandbox is the kind of
// claim SECURITY.md's design stance rules out -- "if a sandbox primitive is
// unavailable, nvx refuses to run the command rather than running it unprotected".
// Every other platform honours that. This one did not.
//
// No release binaries are published for these platforms, so this is reachable
// only by building from source -- which is precisely the person who would trust
// the word "sandbox" without checking. `--no-sandbox` remains the way to run
// uncontained deliberately; it is handled by shouldSandbox and never reaches here.
func platformLaunchNative(config SandboxConfig, guestHome, workDir, cmdPath string, cleanEnv []string, netCtx NetworkLaunchContext) (int, error) {
	LogError("No OS-native sandbox is available on %s; nvx contains commands on Windows, Linux and macOS only.", runtime.GOOS)
	LogInfo("Refusing to run rather than running this command unprotected.")
	LogInfo("To run it without containment, and accept that: nvx --no-sandbox %s", config.Command)
	return 1, errSandboxDidNotStart
}
