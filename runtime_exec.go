package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

var (
	shimToRuntime = map[string]string{}
	shimRuntimeMu sync.Mutex
)

func initRuntimeRegistry() {
	shimToRuntime = map[string]string{}
	for name, p := range Providers {
		for _, cmd := range p.ShimCommands() {
			shimToRuntime[strings.ToLower(cmd)] = name
		}
	}
}

func runtimeForShim(cmdName string) RuntimeProvider {
	shimRuntimeMu.Lock()
	if len(shimToRuntime) == 0 {
		initRuntimeRegistry()
	}
	rt, ok := shimToRuntime[strings.ToLower(cmdName)]
	shimRuntimeMu.Unlock()
	if ok {
		return Providers[rt]
	}
	return Providers[defaultRuntime]
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
	// yarn/pnpm are Node-ecosystem tools (resolved via corepack/PATH fallback);
	// bun/bunx belong to the Bun runtime provider (see runtime_bun.go).
	return []string{"node", "npm", "npx", "yarn", "pnpm"}
}

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

	cmd = strings.ToLower(cmd)
	var binaryPath string
	if runtime.GOOS == "windows" {
		switch cmd {
		case "node":
			binaryPath = filepath.Join(nvxHome, "versions", "node", resolvedVer, "node.exe")
		case "npm":
			binaryPath = filepath.Join(nvxHome, "versions", "node", resolvedVer, "npm.cmd")
		case "npx":
			binaryPath = filepath.Join(nvxHome, "versions", "node", resolvedVer, "npx.cmd")
		}
	} else {
		switch cmd {
		case "node", "npm", "npx":
			binaryPath = filepath.Join(nvxHome, "versions", "node", resolvedVer, "bin", cmd)
		}
	}

	if binaryPath != "" {
		if _, err := os.Stat(binaryPath); err == nil {
			return binaryPath
		}
	}
	return ""
}
