package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var shimToRuntime = map[string]string{}

func initRuntimeRegistry() {
	shimToRuntime = map[string]string{}
	for name, p := range Providers {
		for _, cmd := range p.ShimCommands() {
			shimToRuntime[strings.ToLower(cmd)] = name
		}
	}
}

func runtimeForShim(cmdName string) RuntimeProvider {
	if len(shimToRuntime) == 0 {
		initRuntimeRegistry()
	}
	if rt, ok := shimToRuntime[strings.ToLower(cmdName)]; ok {
		return Providers[rt]
	}
	return Providers["node"]
}

func resolvePinnedCommandPath(command string, nvxHome string, pinnedVer string, provider RuntimeProvider) string {
	if provider == nil {
		provider = runtimeForShim(command)
	}
	if pinnedVer == "" {
		return ""
	}
	return provider.ResolveBinary(command, nvxHome, pinnedVer)
}

func (n NodeProvider) ShimCommands() []string {
	return []string{"node", "npm", "npx", "yarn", "pnpm"}
}

func (n NodeProvider) SandboxImage(version string) string {
	return runtimeDockerImage("node", version)
}

func (n NodeProvider) SessionEnv(versionDir string) map[string]string { return nil }

func (n NodeProvider) DefaultNetworkAllow() []string {
	return []string{
		"registry.npmjs.org:443",
		"api.osv.dev:443",
	}
}

func (n NodeProvider) ResolveBinary(cmd string, nvxHome string, pinnedVer string) string {
	resolvedVer, err := resolveLocalVersion(n, pinnedVer, nvxHome)
	if err != nil {
		return ""
	}
	versionDir := filepath.Join(nvxHome, "versions", "node", resolvedVer)

	cmd = strings.ToLower(cmd)

	// npm/npx can be self-updated via `npm install -g npm@x`, which (when
	// NPM_CONFIG_PREFIX is set, as it is in every real session — see
	// runUse/runAuto) lands in the version's npm_global prefix rather than
	// the bundled node_modules/npm. Check there first so a self-update
	// actually takes effect; node itself is never installed this way, so it
	// always resolves to the bundled binary only.
	if cmd == "npm" || cmd == "npx" {
		if p := npmGlobalOverridePath(nvxHome, versionDir, cmd); p != "" {
			return p
		}
	}

	var binaryPath string
	if runtime.GOOS == "windows" {
		switch cmd {
		case "node":
			binaryPath = filepath.Join(versionDir, "node.exe")
		case "npm":
			binaryPath = filepath.Join(versionDir, "npm.cmd")
		case "npx":
			binaryPath = filepath.Join(versionDir, "npx.cmd")
		}
	} else {
		switch cmd {
		case "node", "npm", "npx":
			binaryPath = filepath.Join(versionDir, "bin", cmd)
		}
	}

	if binaryPath != "" {
		if _, err := os.Stat(binaryPath); err == nil {
			return binaryPath
		}
	}
	return ""
}

// npmGlobalOverridePath returns the path to cmd inside versionDir's npm_global
// prefix — where a self-updated npm/npx lands — if it exists there, else "".
//
// This override is trusted with no further integrity check once it exists,
// and every command resolved through it (isGlobalInstall blocks contained
// code from ever creating or modifying it, but nothing re-verifies it on
// each use) persists across every future invocation on this version, in
// every project, sandboxed or not. Each use is audit-logged so that trail is
// at least inspectable (`~/.nvx/audit.log`) rather than fully silent.
func npmGlobalOverridePath(nvxHome, versionDir, cmd string) string {
	binDir := GetNpmPrefixBinDir(filepath.Join(versionDir, "npm_global"))
	name := cmd
	if runtime.GOOS == "windows" {
		name += ".cmd"
	}
	p := filepath.Join(binDir, name)
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return ""
	}
	auditLog(nvxHome, "npm_global_override_used", map[string]string{
		"path": p,
		"cmd":  cmd,
	})
	return p
}
