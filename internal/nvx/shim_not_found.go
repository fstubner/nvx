package nvx

import (
	"fmt"
	"path/filepath"
	"strings"
)

// What a shim says when it has nothing to run.
//
// It said "Could not find real executable for node" and nothing more, and exited
// 1. Doctor knew the answer, which was "Fix: nvx install lts", and the shim, which
// is what people actually run, did not. docs/exit-codes.md lists 127 for "the
// command to run was not found", the code a shell gives for the same situation,
// and the shim now exits with it.

// reportNoRealExecutable prints that cmdName has nothing to run, and what to do.
func reportNoRealExecutable(cmdName, nvxHome string) {
	if !isShimCommand(cmdName) {
		// A project's own command, say. Nothing about a runtime to suggest.
		LogError("Command not found: %s", cmdName)
		return
	}
	LogError("Could not find real executable for %s", cmdName)
	for _, hint := range noRealExecutableHints(cmdName, nvxHome) {
		// Part of the error, so -q does not hide it.
		LogRefusalDetail("%s", hint)
	}
}

// noRealExecutableHints says why a shim can find nothing and how to fix it. The
// causes differ. There may be no runtime at all, no default among the ones
// installed, an install with a file missing, or yarn or pnpm not set up.
func noRealExecutableHints(cmdName, nvxHome string) []string {
	cmd := strings.ToLower(cmdName)
	rt := runtimeForShim(cmd)
	display := runtimeDisplayName(rt.Name())
	spec := func(version string) string {
		if rt.Name() == "node" {
			return version
		}
		return rt.Name() + "@" + version
	}

	versions, _ := rt.ListLocal(nvxHome)
	if len(versions) == 0 {
		install := "nvx install lts"
		if rt.Name() != "node" {
			install = "nvx install " + rt.Name()
		}
		return []string{fmt.Sprintf("nvx has no %s installed. Run '%s'. The first install becomes the default.", display, install)}
	}

	active := sessionRuntimeVersion(nvxHome, rt, projectPinFor(rt))
	switch {
	case active == "":
		return []string{fmt.Sprintf("No default %s version is set. Run 'nvx default <version>' to choose one. 'nvx list' shows what is installed.", display)}
	case cmd == "yarn" || cmd == "pnpm":
		return []string{fmt.Sprintf("nvx runs %s from the Node.js that has it set up. Run 'corepack enable' once to set up yarn and pnpm there.", cmd)}
	case cmd == "corepack":
		return []string{fmt.Sprintf("%s %s has no corepack. Some releases leave it out. To add it, run 'nvx --no-sandbox npm install -g corepack'.", display, active)}
	}
	// Deleting the folder is what repairs it. `nvx uninstall` refuses the default
	// version, and `nvx install` calls a version installed while its node is there.
	return []string{fmt.Sprintf("%s %s is installed but has no %s, so the install is incomplete. Delete %s and run 'nvx install %s' to put it back.",
		display, active, cmd, filepath.Join(nvxHome, "versions", rt.Name(), active), spec(active))}
}
