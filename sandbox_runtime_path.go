package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// withRuntimeBinOnPath puts the nvx-managed runtime's bin directory at the
// front of the contained process's PATH, when the command being run is one of
// nvx's own runtime binaries.
//
// npm on Unix is a script starting `#!/usr/bin/env node`, so it looks node up
// on PATH. Inside the sandbox that PATH is the user's, which after `nvx env`
// leads with nvx's shim directory -- so npm found the node SHIM, and the shim
// went looking for nvx's home to resolve a version. NVX_HOME is scrubbed and
// HOME points at the throwaway guest profile, so it found neither: measured on
// Linux, a contained `npm install` failed with "Could not find real executable
// for node" whenever the session had not also run `nvx use`. Windows escaped it
// because npm.cmd resolves node.exe next to itself instead of through PATH.
//
// Ahead of the user's entries, deliberately. Outside a sandbox nvx puts its
// shims first so a nested `npm` is intercepted and contained; inside one the
// containment is already running, and a nested call that re-entered nvx would be
// a second sandbox starting inside the first. The pinned runtime, directly, is
// what a contained process should get.
//
// Windows is left alone: it does not have the problem, its PATH order inside an
// AppContainer is what the probe suite measures, and there is no reason to move
// it for a fix it does not need.
func withRuntimeBinOnPath(env []string, cmdPath, nvxHome string) []string {
	if runtime.GOOS == "windows" || cmdPath == "" || nvxHome == "" {
		return env
	}
	binDir := filepath.Dir(cmdPath)
	// Only nvx's own runtimes. This exists to make the pinned runtime findable,
	// not to put whatever directory a command happens to live in on the PATH of
	// a contained process.
	if !dirWithin(binDir, filepath.Join(nvxHome, "versions")) {
		return env
	}
	for i, e := range env {
		if k, v, ok := strings.Cut(e, "="); ok && strings.EqualFold(k, "PATH") {
			env[i] = "PATH=" + binDir + string(os.PathListSeparator) + v
			return env
		}
	}
	return append(env, "PATH="+binDir)
}
