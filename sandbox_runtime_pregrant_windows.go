//go:build windows

package main

import (
	"os"
	"path/filepath"
)

// pregrantRuntimeForSandbox gives the sandbox read/execute on a freshly
// installed runtime, at install time rather than on the first contained
// command.
//
// The container has to read node.exe before it can start, so this grant is
// required and cannot be backgrounded the way the advisory ancestor walks
// were. It costs a Windows ACL propagation over the whole version tree --
// measured 2026-09-03 on a fresh NVX_HOME, 0.88s of a first contained
// `npm install` that took 11.4s in total.
//
// Nothing about it needs to happen at that moment, though. The tree is known
// as soon as the runtime is on disk, and `nvx install` has just spent seconds
// downloading and extracting, so a second more there is invisible where the
// same second in front of someone's first `npm install` is not.
//
// Best-effort and silent on failure, deliberately: ensureAppContainerCommand
// still makes the same grant and its has-grant check finds this one when it
// landed. So this is an optimisation that can fail without costing anything
// beyond the second it was meant to save, and never a thing the sandbox
// depends on having run.
func pregrantRuntimeForSandbox(nvxHome, runtimeName, version string) {
	if nvxHome == "" || runtimeName == "" || version == "" {
		return
	}
	dir := filepath.Join(nvxHome, "versions", runtimeName, version)
	if !regularDirExists(dir) {
		return
	}
	if err := grantRuntimeReadExecTree(dir); err != nil {
		LogDetail("Could not pre-grant the sandbox access to %s (%v); the first contained command will do it.", dir, err)
	}
}

func regularDirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
