//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// nodeSandboxPreserveFlags let node run inside an AppContainer. Node's module
// loader realpath's the entry and every require, which lstats each path up to
// the drive root (C:\). An AppContainer cannot stat C:\ (that grant needs
// elevation), so without these flags any file/require fails with EPERM. The
// flags skip the realpath walk. Bypass with --no-sandbox if a project relies on
// symlink-resolved module identity (e.g. some pnpm layouts).
var nodeSandboxPreserveFlags = []string{"--preserve-symlinks-main", "--preserve-symlinks"}

// rewriteWindowsNodeCommand adapts a resolved command for AppContainer launch:
//   - npm.cmd / npx.cmd become a direct "node.exe <cli>.js" call, because batch
//     files can't be CreateProcess'd and the cmd.exe fallback is denied inside
//     the container.
//   - any node.exe invocation gains the preserve-symlinks flags (see above).
func rewriteWindowsNodeCommand(cmdPath string, args []string) (string, []string) {
	switch strings.ToLower(filepath.Base(cmdPath)) {
	case "npm.cmd", "npx.cmd":
		cli := "npm-cli.js"
		if strings.EqualFold(filepath.Base(cmdPath), "npx.cmd") {
			cli = "npx-cli.js"
		}
		dir := filepath.Dir(cmdPath)
		nodeExe := filepath.Join(dir, "node.exe")
		cliPath := filepath.Join(dir, "node_modules", "npm", "bin", cli)
		if !regularFileExists(nodeExe) || !regularFileExists(cliPath) {
			return cmdPath, args
		}
		rewritten := make([]string, 0, len(nodeSandboxPreserveFlags)+1+len(args))
		rewritten = append(rewritten, nodeSandboxPreserveFlags...)
		rewritten = append(rewritten, cliPath)
		rewritten = append(rewritten, args...)
		return nodeExe, rewritten
	case "node.exe":
		rewritten := make([]string, 0, len(nodeSandboxPreserveFlags)+len(args))
		rewritten = append(rewritten, nodeSandboxPreserveFlags...)
		rewritten = append(rewritten, args...)
		return cmdPath, rewritten
	default:
		return cmdPath, args
	}
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
	// Adapt node/npm/npx for AppContainer launch (direct node.exe, realpath-safe).
	cmdPath, launchArgs := rewriteWindowsNodeCommand(cmdPath, config.Args)

	cmdPath, err = ensureAppContainerCommand(sid, config.NvxHome, cmdPath)
	if err != nil {
		LogError("AppContainer executable access failed: %v", err)
		return 1
	}

	// Network: AppContainers cannot reach the loopback egress proxy, so by
	// default we grant internetClient and run direct (network works, egress not
	// OS-allowlisted). The admin loopback-allowlist opt-in flips this to a
	// proxied, allowlisted path. offline/loopback modes grant nothing.
	capabilitySIDs, useProxy := windowsSandboxNetwork(config.NvxHome, netCtx.Mode)
	if !useProxy {
		cleanEnv = stripProxyEnv(cleanEnv)
	}

	LogInfo("Windows AppContainer isolation active")
	exitCode, err := launchAppContainerProcess(
		cmdPath, launchArgs, cleanEnv, workDir, sid, 0, capabilitySIDs,
	)
	if err != nil {
		LogError("AppContainer launch failed: %v", err)
		return 1
	}
	return exitCode
}

// windowsSandboxNetwork decides AppContainer network capabilities and whether to
// route through the loopback egress proxy, based on network.mode and whether the
// admin loopback allowlist is enabled.
func windowsSandboxNetwork(nvxHome, mode string) (capabilitySIDs []string, useProxy bool) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "offline", "loopback":
		return nil, false // no capabilities: sandbox has no network
	}
	if windowsLoopbackAllowlistEnabled(nvxHome) {
		// Stable, loopback-exempted SID can reach the proxy; egress is allowlisted.
		return nil, true
	}
	// Default: internetClient so network works; direct (egress not allowlisted).
	return []string{capabilityInternetClientSID}, false
}

func stripProxyEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		switch strings.ToUpper(strings.SplitN(e, "=", 2)[0]) {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY":
			continue
		}
		out = append(out, e)
	}
	return out
}
