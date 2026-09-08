//go:build windows

package main

import (
	"fmt"
	"strings"
	"time"
)

// repairPersistentPath rewrites the User PATH environment variable so the shim
// dir leads and raw-runtime dirs no longer shadow it. Returns true if it made a
// change. New shells pick up the updated value.
func repairPersistentPathImpl(nvxHome string, apply bool) (bool, error) {
	shimDir := shimDirPath(nvxHome)
	// A throwaway NVX_HOME must not be written into the User PATH.
	//
	// The persistent PATH outlives the directory. Tests, agents and CI steps all
	// point NVX_HOME at a temp directory routinely, and `nvx doctor --fix` run
	// under one used to put that temp path into the User PATH for ever: this
	// machine still carries
	// `C:\Users\<user>\AppData\Local\Temp\nvxa\bin` from exactly that. Two
	// consequences, and the second is the serious one -- the entry is dead once
	// the directory is cleaned up, and until then it is a directory anything that
	// can write the temp tree can drop an executable into, on the search path of
	// every process the user starts.
	//
	// Refused rather than warned, because the caller cannot undo it afterwards.
	if underTempDir(shimDir) {
		return false, fmt.Errorf("NVX_HOME is inside the temporary directory (%s); "+
			"refusing to put it in your persistent PATH, since that outlives the directory", shimDir)
	}
	existing, expandable, err := readUserPath()
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(existing) == "" {
		// A genuinely empty User PATH is indistinguishable here from a parse
		// failure (unexpected `reg query` output shape, localized Windows,
		// etc.). Treating either as "safe to overwrite" would let us replace
		// the user's entire persistent PATH with just the shim dir, silently
		// destroying every other PATH entry they have. Refuse and let the
		// caller fall back to the per-shell fix hint instead.
		return false, fmt.Errorf("could not read the current User PATH (empty or unrecognized `reg query` output); leaving it unchanged")
	}
	fixed := rebuildUserPath(existing, shimDir, nvxRuntimeDirs(nvxHome))
	if existing == fixed {
		return false, nil
	}
	// Say what goes. The repair removes raw-runtime directories that shadow the
	// shims; a PATH that shrinks with no word about what left is the defect the
	// report exists to prevent.
	for _, e := range droppedPathEntries(existing, fixed, shimDir) {
		LogInfo("PATH repair removes %s (a raw runtime directory that shadows nvx's shims).", e)
	}
	if !apply {
		// A repair is available but was not asked for. Report that and write
		// nothing. `nvx doctor` used to edit the persistent PATH on sight, which
		// is a surprising thing for a command named after diagnosis to do -- and
		// it fired against whatever NVX_HOME happened to be set, so anyone
		// pointing that at a throwaway directory had their real PATH rewritten to
		// front it.
		return true, nil
	}
	// Written through the registry, with the type it already had.
	//
	// This used to go through PowerShell's [Environment]::SetEnvironmentVariable,
	// which always writes REG_SZ. Windows ships the User PATH as REG_EXPAND_SZ,
	// so the repair converted the type on every machine it ran on, and any entry
	// spelled %USERPROFILE%\bin or %JAVA_HOME%\bin stopped being expanded and
	// quietly stopped resolving. setx was rejected before that for truncating at
	// 1024 characters; writing the value directly has neither limit nor the
	// conversion, and keeps the broadcast the .NET setter was doing for us.
	if err := setUserPath(fixed, expandable); err != nil {
		return false, fmt.Errorf("set User PATH: %w", err)
	}
	return true, nil
}

// readUserPath returns the persistent User PATH as the registry holds it. A
// variable so a test can hand the repair a PATH of its own and check what the
// repair says about it, without touching the registry.
var readUserPath = func() (value string, expand bool, err error) {
	out, err := runWinCmd(15*time.Second, "reg", "query", `HKCU\Environment`, "/v", "Path")
	if err != nil {
		return "", false, err
	}
	return parseRegPath(string(out)), parseRegExpandable(string(out)), nil
}

// setUserPath writes the User PATH back, preserving the type it was stored as.
// A variable so a test can watch what the repair would write without a machine
// carrying the result afterwards.
var setUserPath = func(value string, expand bool) error {
	if err := setRegistryStringValue("Environment", "Path", value, expand); err != nil {
		return err
	}
	broadcastEnvironmentChange()
	return nil
}

// parseRegExpandable reports whether `reg query` said the value is
// REG_EXPAND_SZ -- the type Windows ships the User PATH as, and the one that
// makes %USERPROFILE%\bin resolve.
func parseRegExpandable(regOut string) bool {
	return strings.Contains(regOut, "REG_EXPAND_SZ")
}

// parseRegPath extracts the value from `reg query ... /v Path` output.
func parseRegPath(regOut string) string {
	for _, line := range strings.Split(regOut, "\n") {
		if i := strings.Index(line, "REG_"); i != -1 {
			rest := line[i:]
			fields := strings.SplitN(rest, "    ", 2)
			if len(fields) == 2 {
				return strings.TrimSpace(fields[1])
			}
			// Fallback: value after the type token.
			toks := strings.Fields(rest)
			if len(toks) >= 2 {
				return strings.Join(toks[1:], " ")
			}
		}
	}
	return ""
}
