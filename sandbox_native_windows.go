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
//
// nodeExeFallback is used when no node.exe sits beside the .cmd, which is the
// normal layout for a self-updated npm living in a version's npm_global prefix.
// Without it the rewrite would bail and launch the batch wrapper, whose own
// `IF EXIST "%dp0%\node.exe"` check then fails and degrades to a bare `node`
// that is not resolvable inside the container.
func rewriteWindowsNodeCommand(cmdPath string, args []string, nodeExeFallback string) (string, []string) {
	switch strings.ToLower(filepath.Base(cmdPath)) {
	case "npm.cmd", "npx.cmd":
		cli := "npm-cli.js"
		if strings.EqualFold(filepath.Base(cmdPath), "npx.cmd") {
			cli = "npx-cli.js"
		}
		dir := filepath.Dir(cmdPath)
		nodeExe := filepath.Join(dir, "node.exe")
		// Keep the CLI next to the .cmd we resolved, so a self-updated npm stays
		// the one that runs; only the interpreter falls back.
		cliPath := filepath.Join(dir, "node_modules", "npm", "bin", cli)
		if !regularFileExists(nodeExe) && regularFileExists(nodeExeFallback) {
			nodeExe = nodeExeFallback
		}
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
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// resolveSandboxNodeExe locates the node.exe of the runtime version this session
// is using, for use as the interpreter when a resolved npm.cmd/npx.cmd has no
// node.exe of its own. Returns "" if no version is active, in which case the
// rewrite simply declines rather than guessing.
func resolveSandboxNodeExe(nvxHome string) string {
	rt := runtimeForShim("node")
	ver := getActiveShellVersionFor(nvxHome, rt.Name())
	if ver == "" {
		ver = getGlobalDefaultVersionFor(nvxHome, rt.Name())
	}
	if ver == "" {
		return ""
	}
	return resolvePinnedCommandPath("node", nvxHome, ver, rt)
}

// platformLaunchNative applies AppContainer isolation on Windows.
// Isolation setup is fail-closed: if AppContainer cannot be applied, the command
// is not executed.
func platformLaunchNative(config SandboxConfig, guestHome, workDir, cmdPath string, cleanEnv []string, netCtx NetworkLaunchContext) int {
	// Use the stable profile so its SID is a durable target for `nvx setup`
	// grants (ancestor stat + loopback exemption). It is intentionally not
	// deleted after the run; isolation comes from the ephemeral guest home,
	// capability restrictions, and filesystem ACLs, not SID uniqueness.
	sid, err := ensureAppContainerSID(stableSandboxProfile)
	if err != nil {
		LogError("AppContainer profile unavailable: %v", err)
		return 1
	}
	defer syscall.LocalFree(syscall.Handle(sid))

	// Real package-manager workflows stat ancestor directories (up to C:\), which
	// an AppContainer cannot do until `nvx setup` grants it. Rather than launch a
	// run we know will fail with a cryptic EPERM, refuse with a clear choice.
	if isPackageManagerCommand(config.Command) && !windowsSandboxSetupDone(config.NvxHome) {
		LogError("%s can't run under the Windows sandbox until one-time setup is done.", config.Command)
		LogInfo("Choose one:")
		LogInfo("  * Run 'nvx setup' from an Administrator terminal to enable the sandbox (recommended).")
		LogInfo("  * Run it without OS isolation:  nvx --no-sandbox %s ...", config.Command)
		LogInfo("    (typosquat / vulnerability / install-script checks still apply).")
		return 1
	}

	if err := prepareAppContainerFilesystem(sid, guestHome, workDir); err != nil {
		LogError("AppContainer filesystem setup failed: %v", err)
		return 1
	}
	// Adapt node/npm/npx for AppContainer launch (direct node.exe, realpath-safe).
	cmdPath, launchArgs := rewriteWindowsNodeCommand(cmdPath, config.Args, resolveSandboxNodeExe(config.NvxHome))

	cmdPath, err = ensureAppContainerCommand(sid, config.NvxHome, cmdPath)
	if err != nil {
		LogError("AppContainer executable access failed: %v", err)
		return 1
	}

	// Put the resolved runtime's directory on PATH so tools spawned inside the
	// sandbox (e.g. an npx-installed CLI whose launcher calls `node`) can find it.
	// The host's own node dir is on PATH but is not accessible to the container;
	// this directory is granted RX above.
	cleanEnv = prependPath(cleanEnv, filepath.Dir(cmdPath))

	// The preserve-symlinks flags above only cover the process nvx launches.
	// npm scripts spawn further node processes, whose own entry-point resolution
	// realpaths up to the drive root — a path an AppContainer cannot stat unless
	// that volume's root was granted by `nvx setup` (only the system drive is,
	// so any project on another drive fails). NODE_OPTIONS carries the flags into
	// every child so the realpath walk is skipped there too.
	cleanEnv = setNodeOptionsPreserveSymlinks(cleanEnv)

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

// prependPath puts dir at the front of the PATH entry in env (case-insensitive
// key match), adding a PATH entry if none exists.
func prependPath(env []string, dir string) []string {
	if dir == "" {
		return env
	}
	for i, e := range env {
		kv := strings.SplitN(e, "=", 2)
		if len(kv) == 2 && strings.EqualFold(kv[0], "PATH") {
			env[i] = "PATH=" + dir + string(os.PathListSeparator) + kv[1]
			return env
		}
	}
	return append(env, "PATH="+dir)
}

// setNodeOptionsPreserveSymlinks ensures NODE_OPTIONS carries the
// preserve-symlinks flags, so node processes spawned *inside* the sandbox (npm
// scripts, tool launchers) skip the realpath walk that the flags on nvx's own
// launch command line only cover for the top-level process.
func setNodeOptionsPreserveSymlinks(env []string) []string {
	flags := strings.Join(nodeSandboxPreserveFlags, " ")
	for i, e := range env {
		kv := strings.SplitN(e, "=", 2)
		if len(kv) == 2 && strings.EqualFold(kv[0], "NODE_OPTIONS") {
			if strings.Contains(kv[1], "--preserve-symlinks") {
				return env // already covered; don't duplicate
			}
			env[i] = "NODE_OPTIONS=" + strings.TrimSpace(kv[1]+" "+flags)
			return env
		}
	}
	return append(env, "NODE_OPTIONS="+flags)
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
