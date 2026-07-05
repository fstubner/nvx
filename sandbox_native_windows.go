//go:build windows

package main

import (
	"path/filepath"
	"syscall"
)

// platformLaunchNative applies AppContainer isolation on Windows.
// Isolation setup is fail-closed: if AppContainer cannot be applied, the command
// is not executed.
func platformLaunchNative(config SandboxConfig, guestHome, workDir, cmdPath string, cleanEnv []string, netCtx NetworkLaunchContext) int {
	profileName := appContainerNamePrefix + "." + filepath.Base(guestHome)
	sid, err := ensureAppContainerSID(profileName)
	if err != nil {
		LogError("AppContainer profile unavailable: %v", err)
		return 1
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	defer deleteAppContainerProfile(profileName)

	if err := prepareAppContainerFilesystem(sid, guestHome, workDir); err != nil {
		LogError("AppContainer filesystem setup failed: %v", err)
		return 1
	}
	cmdPath, err = ensureAppContainerCommand(sid, config.NvxHome, cmdPath)
	if err != nil {
		LogError("AppContainer executable access failed: %v", err)
		return 1
	}

	LogInfo("Windows AppContainer isolation active")
	exitCode, err := launchAppContainerProcess(
		cmdPath, config.Args, cleanEnv, workDir, sid, 0,
	)
	if err != nil {
		LogError("AppContainer launch failed: %v", err)
		return 1
	}
	return exitCode
}
