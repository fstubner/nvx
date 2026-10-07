package nvx

import (
	"path/filepath"
	"strings"
)

// `corepack enable` and `corepack disable` through nvx's corepack shim.
//
// Both act on the directory the `corepack` binary is found in, which they ask
// `which corepack`. With nvx's shim directory first on PATH that answer is the
// shim directory, so corepack's yarn and pnpm links replaced nvx's yarn and pnpm
// shims there. See shim_dir.go for what followed.
//
// The shim now hands corepack the directory it would have chosen had nvx not
// been first on PATH, the one holding the corepack nvx is about to run. For an
// nvx-managed Node that is its own bin directory (beside node.exe on Windows),
// which is also where nvm and fnm end up. nvx's yarn and pnpm shims find the
// links there (see NodeProvider.ResolveBinary) and contain what they run.
//
// A directory the user names with --install-directory is theirs to choose and is
// left alone.

// corepackInstallDirArgs returns args with --install-directory added when they
// are `corepack enable` or `corepack disable` without one.
func corepackInstallDirArgs(args []string, nvxHome string) []string {
	if len(args) == 0 || (args[0] != "enable" && args[0] != "disable") {
		return args
	}
	for _, a := range args[1:] {
		if a == "--" {
			break
		}
		if a == "--install-directory" || strings.HasPrefix(a, "--install-directory=") {
			return args
		}
	}
	dir := corepackBinaryDir(nvxHome)
	if dir == "" {
		return args
	}
	if args[0] == "enable" {
		LogInfo("Corepack's links go in %s, beside corepack itself, and not in nvx's shim directory.", dir)
	} else {
		LogInfo("Corepack's links come out of %s, beside corepack itself, and not out of nvx's shim directory.", dir)
	}
	withDir := make([]string, 0, len(args)+2)
	withDir = append(withDir, args[0], "--install-directory", dir)
	return append(withDir, args[1:]...)
}

// corepackLauncherScript returns the corepack script that a yarn.cmd or pnpm.cmd
// launcher starts, or "" when the file is not one corepack wrote. The launcher
// names its script, so that is what is checked. Corepack puts it in
// node_modules\corepack\dist beside the launcher.
func corepackLauncherScript(cmdPath string) string {
	name := strings.TrimSuffix(strings.ToLower(filepath.Base(cmdPath)), ".cmd")
	if !strings.Contains(strings.ToLower(readSmallFile(cmdPath)), `corepack\dist\`+name+`.js`) {
		return ""
	}
	return filepath.Join(filepath.Dir(cmdPath), "node_modules", "corepack", "dist", name+".js")
}

// corepackBinaryDir is the directory holding the corepack the shim runs, found
// in the order directCommand finds it. That is the active version's own, then the
// project's node_modules/.bin, then one on PATH outside nvx's shim directory. ""
// when there is none. A different order here would hand corepack a directory
// beside some other corepack than the one that runs, and its own `which` would
// still find the shim directory.
func corepackBinaryDir(nvxHome string) string {
	rt := runtimeForShim("corepack")
	activeVer := sessionRuntimeVersion(nvxHome, rt, projectPinFor(rt))
	path := resolvePinnedCommandPath("corepack", nvxHome, activeVer, rt)
	if path == "" {
		path = resolveProjectBinCommand("corepack")
	}
	if path == "" {
		found, err := lookPathSkippingNvxShims("corepack", nvxHome)
		if err != nil {
			return ""
		}
		path = found
	}
	return filepath.Dir(path)
}
