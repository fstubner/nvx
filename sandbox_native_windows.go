//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// rewriteWindowsNodeCliCommand turns npm.cmd / npx.cmd into a direct
// "node.exe <cli>.js" invocation. Two AppContainer-specific reasons:
//   - Batch files can't be CreateProcess'd, so nvx would otherwise fall back to
//     cmd.exe, which is denied inside the container; node.exe runs directly.
//   - Node's module loader realpath's the entry and its requires, which lstats
//     every ancestor up to the drive root (C:\) — the AppContainer cannot stat
//     C:\, giving EPERM. --preserve-symlinks[-main] skips that realpath walk.
func rewriteWindowsNodeCliCommand(cmdPath string, args []string) (string, []string) {
	var cli string
	switch strings.ToLower(filepath.Base(cmdPath)) {
	case "npm.cmd":
		cli = "npm-cli.js"
	case "npx.cmd":
		cli = "npx-cli.js"
	default:
		return cmdPath, args
	}
	dir := filepath.Dir(cmdPath)
	nodeExe := filepath.Join(dir, "node.exe")
	cliPath := filepath.Join(dir, "node_modules", "npm", "bin", cli)
	if !regularFileExists(nodeExe) || !regularFileExists(cliPath) {
		return cmdPath, args
	}
	rewritten := append([]string{"--preserve-symlinks-main", "--preserve-symlinks", cliPath}, args...)
	return nodeExe, rewritten
}

func regularFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

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
	// Prefer a direct node.exe invocation for npm/npx so the container never
	// needs cmd.exe to interpret a .cmd wrapper.
	cmdPath, launchArgs := rewriteWindowsNodeCliCommand(cmdPath, config.Args)

	cmdPath, err = ensureAppContainerCommand(sid, config.NvxHome, cmdPath)
	if err != nil {
		LogError("AppContainer executable access failed: %v", err)
		return 1
	}

	LogInfo("Windows AppContainer isolation active")
	exitCode, err := launchAppContainerProcess(
		cmdPath, launchArgs, cleanEnv, workDir, sid, 0,
	)
	if err != nil {
		LogError("AppContainer launch failed: %v", err)
		return 1
	}
	return exitCode
}
