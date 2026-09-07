package main

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// containedRuntimeBinDirs returns the runtime directories a contained process
// should find on its PATH, most specific first: the runtime of the command
// being launched, then every other runtime nvx has installed.
//
// The first entry is what npm needs. npm on Unix is a script starting
// `#!/usr/bin/env node`, so it resolves node through PATH; inside the sandbox
// that PATH leads with nvx's shim directory, so npm found the node SHIM, and
// the shim could not resolve a version from in there -- NVX_HOME is scrubbed
// and HOME is the throwaway guest profile. Measured on Linux: the install died
// with "Could not find real executable for node" where no other node was
// reachable, and on macOS it silently ran the machine's own node instead, so a
// contained process was on the pinned v22.23.2 while a nested lookup got
// v24.20.0.
//
// The rest are for the other runtime. Measured on Windows, where nvx already
// put the launched runtime's directory on the contained PATH: inside a
// contained bun, a script calling `node` fell through to
// C:\Program Files\nodejs\node.exe, which the container then denied; inside a
// contained node, `bun` could not be resolved at all. A postinstall that calls
// the other runtime is ordinary, and either outcome is wrong.
//
// Ahead of the user's own entries, deliberately. Outside a sandbox nvx puts its
// shims first so a nested call is intercepted and contained; inside one the
// containment is already running, and a nested call that re-entered nvx would
// be a second sandbox starting inside the first.
func containedRuntimeBinDirs(cmdPath, nvxHome string) []string {
	if nvxHome == "" {
		return nil
	}
	versions := filepath.Join(nvxHome, "versions")

	var dirs []string
	seen := map[string]bool{}
	add := func(dir string) {
		if dir == "" || seen[dir] {
			return
		}
		if !dirWithin(dir, versions) {
			return
		}
		if _, err := os.Stat(dir); err != nil {
			return
		}
		seen[dir] = true
		dirs = append(dirs, dir)
	}

	// The launched runtime first, taken from the command itself so it is exactly
	// the one this run resolved -- a pinned version, a project override, or the
	// global default, without re-deriving which.
	add(filepath.Dir(cmdPath))

	// Then the others, at whatever version this session would use for them.
	names := make([]string, 0, len(Providers))
	for name := range Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		rt := Providers[name]
		ver := getActiveShellVersionFor(nvxHome, rt.Name())
		if ver == "" {
			ver = getGlobalDefaultVersionFor(nvxHome, rt.Name())
		}
		if ver == "" {
			continue
		}
		for _, cmd := range rt.ShimCommands() {
			if p := resolvePinnedCommandPath(cmd, nvxHome, ver, rt); p != "" {
				add(filepath.Dir(p))
				break
			}
		}
	}
	return dirs
}

// withRuntimeBinOnPath puts those directories at the front of a contained
// process's PATH. Windows gets the same list through containedEnv, which
// already placed the launched runtime's directory there before this existed.
func withRuntimeBinOnPath(env []string, cmdPath, nvxHome string) []string {
	if runtime.GOOS == "windows" {
		return env
	}
	dirs := containedRuntimeBinDirs(cmdPath, nvxHome)
	if len(dirs) == 0 {
		return env
	}
	prefix := strings.Join(dirs, string(os.PathListSeparator))
	for i, e := range env {
		if k, v, ok := strings.Cut(e, "="); ok && strings.EqualFold(k, "PATH") {
			env[i] = "PATH=" + prefix + string(os.PathListSeparator) + v
			return env
		}
	}
	return append(env, "PATH="+prefix)
}
