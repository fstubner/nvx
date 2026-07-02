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
	if err := grantAppContainerExecutable(sid, cmdPath); err != nil {
		LogError("AppContainer executable access failed: %v", err)
		return 1
	}

	lowToken, err := createLowIntegrityPrimaryToken()
	if err != nil {
		LogError("Low Integrity token for AppContainer launch: %v", err)
		return 1
	}
	defer syscall.CloseHandle(syscall.Handle(lowToken))

	LogInfo("Windows AppContainer + Low Integrity isolation active")
	exitCode, err := launchAppContainerProcess(
		cmdPath, config.Args, cleanEnv, workDir, sid, lowToken,
	)
	if err != nil {
		LogError("AppContainer launch failed: %v", err)
		return 1
	}
	return exitCode
}
