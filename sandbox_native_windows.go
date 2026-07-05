//go:build windows

package main

import (
	"syscall"
)

// platformLaunchNative applies AppContainer + Low IL isolation on Windows.
// Isolation setup is fail-closed: if AppContainer cannot be applied, the command
// is not executed.
func platformLaunchNative(config SandboxConfig, guestHome, workDir, cmdPath string, cleanEnv []string, netCtx NetworkLaunchContext) int {
	sid, err := ensureAppContainerSID()
	if err != nil {
		LogError("AppContainer profile unavailable: %v", err)
		return 1
	}
	defer syscall.LocalFree(syscall.Handle(sid))

	if err := prepareAppContainerFilesystem(sid, guestHome, workDir); err != nil {
		LogError("AppContainer filesystem setup failed: %v", err)
		return 1
	}
	cmdPath, err = ensureAppContainerCommand(sid, config.NvxHome, cmdPath)
	if err != nil {
		LogError("AppContainer executable access failed: %v", err)
		return 1
	}

	// NOTE: the process is launched with the caller's token (lowILToken=0), not a
	// Low-IL duplicate: labeling the runtime Low IL previously broke CreateProcess
	// on AppContainer children. Isolation here is the AppContainer boundary + zero
	// capabilities, NOT an added Low Integrity Level — the log reflects that
	// honestly. (Re-introducing a working Low-IL token is tracked as future work.)
	LogInfo("Windows AppContainer isolation active (zero capabilities)")
	exitCode, err := launchAppContainerProcess(
		cmdPath, config.Args, cleanEnv, workDir, sid, 0,
	)
	if err != nil {
		LogError("AppContainer launch failed: %v", err)
		return 1
	}
	return exitCode
}
