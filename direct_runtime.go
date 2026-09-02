package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// The uncontained ("your own code") launch on Windows, and the two process
// hops it no longer takes.
//
// Measured 2026-09-02 with Get-CimInstance Win32_Process, `npm run dev` where
// dev is `node -e "setTimeout(()=>{},8000)"`, from cmd.exe with the shim dir
// first on PATH, was this tree:
//
//	cmd.exe /c "npm run dev"
//	  nvx.exe shim npm run dev
//	    cmd.exe /c <version>\npm.cmd run dev        <- Windows runs a .cmd via cmd.exe
//	      node.exe npm-cli.js run dev
//	        cmd.exe /d /s /c node -e ...            <- npm's script runner, not ours
//	          nvx.exe shim node -e ...              <- `node` resolved to nvx's shim
//	            node.exe -e ...
//
// directLaunchCommand removes the first cmd.exe by starting node.exe with
// npm-cli.js the way the batch file would have. directRuntimeDir removes the
// second nvx.exe by giving the child a PATH entry in which `node` is the real
// node -- and ONLY node, so a nested `npm install` inside a script still
// resolves to the shim and stays intercepted.

// windowsNpmCliLaunch resolves npm.cmd or npx.cmd to the node.exe and CLI script
// the batch wrapper would run. nodeExeFallback is used when no node.exe sits
// beside the .cmd, which is the layout of a self-updated npm in a version's
// npm_global prefix: the CLI is kept beside the .cmd so that npm is the one that
// runs, and only the interpreter falls back. ok is false when either file is
// missing, in which case the caller launches the .cmd as it is.
func windowsNpmCliLaunch(cmdPath, nodeExeFallback string) (nodeExe, cliPath string, ok bool) {
	var cli string
	switch strings.ToLower(filepath.Base(cmdPath)) {
	case "npm.cmd":
		cli = "npm-cli.js"
	case "npx.cmd":
		cli = "npx-cli.js"
	default:
		return "", "", false
	}
	dir := filepath.Dir(cmdPath)
	nodeExe = filepath.Join(dir, "node.exe")
	cliPath = filepath.Join(dir, "node_modules", "npm", "bin", cli)
	if !regularFileExists(nodeExe) && regularFileExists(nodeExeFallback) {
		nodeExe = nodeExeFallback
	}
	if !regularFileExists(nodeExe) || !regularFileExists(cliPath) {
		return "", "", false
	}
	return nodeExe, cliPath, true
}

func regularFileExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// directLaunchCommand is what the uncontained path executes for a resolved
// binary: npm.cmd/npx.cmd become `node.exe <cli>.js args`, anything else is
// launched as resolved. Unlike the sandbox's rewriteWindowsNodeCommand it adds
// no --preserve-symlinks flags -- those exist because an AppContainer cannot
// stat the drive root, and they change module identity for pnpm layouts, which
// a command running as the user has no reason to accept.
func directLaunchCommand(binaryPath, nodeExeFallback string, args []string) (string, []string) {
	nodeExe, cliPath, ok := windowsNpmCliLaunch(binaryPath, nodeExeFallback)
	if !ok {
		return binaryPath, args
	}
	rewritten := make([]string, 0, 1+len(args))
	rewritten = append(rewritten, cliPath)
	return nodeExe, append(rewritten, args...)
}

// directRuntimeDir returns a directory holding only the runtime executable
// (node.exe or bun.exe) for the version in use, creating it under
// <nvxHome>/direct/<runtime>/<version> when needed, or "" when there is no
// installed version to offer. It is meant to be prepended to a child's PATH.
//
// Not the version directory itself: that also holds npm.cmd and npx.cmd, and
// putting it ahead of the shim dir would unshim every nested npm call, so an
// `npm install` run from a script would no longer be contained.
//
// Outside <nvxHome>/versions and <nvxHome>/current on purpose. Those are the
// roots doctor treats as raw runtime dirs that must never precede the shim dir
// (nvxRuntimeDirs); a nested shim sees this directory ahead of the shim dir on
// the PATH nvx gave it and must not warn about nvx's own arrangement.
func directRuntimeDir(nvxHome string, rt RuntimeProvider, version string) string {
	if runtime.GOOS != "windows" || rt == nil || version == "" {
		return ""
	}
	exe := resolvePinnedCommandPath(rt.Name(), nvxHome, version, rt)
	if exe == "" {
		return ""
	}
	dir := filepath.Join(nvxHome, "direct", rt.Name(), filepath.Base(filepath.Dir(exe)))
	if err := ensureDirectRuntimeExe(dir, exe); err != nil {
		LogWarn("Could not prepare %s for nested %s calls (%v); they will route through nvx's shim.", dir, rt.Name(), err)
		return ""
	}
	return dir
}

// ensureDirectRuntimeExe makes dir/<base of exe> a hard link to exe (a copy
// where linking fails), replacing a link that no longer points at exe -- a
// reinstalled version has a new node.exe, and a link to the old file would keep
// running the old one.
func ensureDirectRuntimeExe(dir, exe string) error {
	dst := filepath.Join(dir, filepath.Base(exe))
	if sameExistingFile(exe, dst) || sameSizeAndTime(exe, dst) {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Link(exe, dst); err == nil {
		return nil
	}
	if err := installNvxCopy(exe, dst); err != nil {
		return err
	}
	if info, err := os.Stat(exe); err == nil {
		_ = os.Chtimes(dst, info.ModTime(), info.ModTime())
	}
	return nil
}

// pruneDirectRuntimeDirs removes direct dirs whose version is no longer
// installed. A hard link keeps the file alive after `nvx uninstall` deletes the
// version tree, so without this every uninstalled runtime left its node.exe (or
// bun.exe) behind under <nvxHome>/direct.
func pruneDirectRuntimeDirs(nvxHome string) {
	root := filepath.Join(nvxHome, "direct")
	runtimes, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, rt := range runtimes {
		if !rt.IsDir() {
			continue
		}
		versions, err := os.ReadDir(filepath.Join(root, rt.Name()))
		if err != nil {
			continue
		}
		for _, v := range versions {
			if _, err := os.Stat(filepath.Join(nvxHome, "versions", rt.Name(), v.Name())); os.IsNotExist(err) {
				_ = os.RemoveAll(filepath.Join(root, rt.Name(), v.Name()))
			}
		}
	}
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
