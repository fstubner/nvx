//go:build windows

package nvx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A package manager's own script, started by node, that the sandbox cannot read.
//
// A pnpm or yarn installed with `npm install -g` outside the folders nvx manages
// is refused when `pnpm` reaches nvx's shim, with a message naming the fix (see
// notARuntimeInstallError). It reaches nvx another way when its folder is ahead
// of nvx's shims on PATH. npm's own launcher, the `pnpm` script Git Bash finds or
// pnpm.cmd, runs `node <folder>\node_modules\pnpm\bin\pnpm.cjs`, and `node` is
// nvx's shim. nvx reads that as a pnpm install, which it contains, and node stops
// with "Cannot find module" on a script the sandbox cannot read. Measured
// 2026-10-07 with pnpm 10.34.6 in an npm prefix under %TEMP%, exit 1. The docs
// say the run is refused with a message that says so.
//
// Only a script that is there and whose permissions name nobody the launch
// carries counts, so a script that does not exist is still node's to report, and
// so is one whose permissions could not be read.

// sandboxReaderSIDs are the identities whose read permission lets this launch
// read a file: its package, the project capabilities and the runtime's, and
// ALL APPLICATION PACKAGES, which is how Program Files and Windows are readable
// to every AppContainer.
func sandboxReaderSIDs(packageSID string, scopeCaps []string) []string {
	readers := append(launchCapabilitySIDs(scopeCaps, nil), "S-1-15-2-1")
	if packageSID != "" {
		readers = append(readers, packageSID)
	}
	return readers
}

// unreadablePackageManagerScript returns the entry script of a package manager
// that command starts through node, when the sandbox cannot read it, or "".
func unreadablePackageManagerScript(command string, args, readers []string) string {
	if strings.TrimSuffix(strings.ToLower(filepath.Base(command)), ".exe") != "node" {
		return ""
	}
	i := nodeScriptIndex(args)
	// A relative path is the project's, which the sandbox can read.
	if i < 0 || !filepath.IsAbs(args[i]) {
		return ""
	}
	script := filepath.Clean(args[i])
	if info, err := os.Stat(script); err != nil || info.IsDir() {
		return ""
	}
	entries, err := readDACL(script)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.grantsAtLeast(aclMaskReadExec) {
			continue
		}
		for _, r := range readers {
			if sidsEqual(e.SID, r) {
				return ""
			}
		}
	}
	return script
}

// scriptPackage names the package a script belongs to and the folder holding it,
// the node_modules\<name> folder when the path has one and the script's own
// folder when it does not.
func scriptPackage(script string) (name, dir string) {
	parts := strings.Split(filepath.Clean(script), string(filepath.Separator))
	for i := len(parts) - 2; i >= 0; i-- {
		if !strings.EqualFold(parts[i], "node_modules") {
			continue
		}
		end := i + 2
		if strings.HasPrefix(parts[i+1], "@") {
			end++ // @scope\name
		}
		if end <= len(parts)-1 {
			return strings.Join(parts[i+1:end], "/"), strings.Join(parts[:end], string(filepath.Separator))
		}
	}
	return strings.TrimSuffix(filepath.Base(script), filepath.Ext(script)), filepath.Dir(script)
}

// notReadableScriptError is the refusal for such a script, with the ways to run
// it that do work.
func notReadableScriptError(script string) error {
	name, dir := scriptPackage(script)
	return fmt.Errorf("%s is not somewhere this project's sandbox can read, so node would stop with \"Cannot find module\". "+
		"To run it contained, use a runtime nvx manages ('nvx install lts', and for a package 'nvx --no-sandbox npm install -g %s'), "+
		"or add %s to isolation.filesystem.allow_read_exec in your nvx policy so this project's sandbox reads it where it is. "+
		"To run it uncontained, use 'nvx --no-sandbox'", script, name, dir)
}
