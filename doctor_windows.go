//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// repairPersistentPath rewrites the User PATH environment variable so the shim
// dir leads and raw-runtime dirs no longer shadow it. Returns true if it made a
// change. New shells pick up the updated value.
func repairPersistentPathImpl(nvxHome string, apply bool) (bool, error) {
	shimDir := shimDirPath(nvxHome)
	out, err := runWinCmd(15*time.Second, "reg", "query", `HKCU\Environment`, "/v", "Path")
	if err != nil {
		return false, err
	}
	existing := parseRegPath(string(out))
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
	if !apply {
		// A repair is available but was not asked for. Report that and write
		// nothing. `nvx doctor` used to edit the persistent PATH on sight, which
		// is a surprising thing for a command named after diagnosis to do -- and
		// it fired against whatever NVX_HOME happened to be set, so anyone
		// pointing that at a throwaway directory had their real PATH rewritten to
		// front it.
		return true, nil
	}
	// setx truncates at 1024 chars; use PowerShell's [Environment] setter which
	// does not, matching what install.ps1 uses. The new PATH is passed via an
	// environment variable so no quoting/injection issues arise.
	ps := "[Environment]::SetEnvironmentVariable('Path', $env:__NVX_NEWPATH, 'User')"
	cmd := exec.Command("powershell", "-NoProfile", "-Command", ps)
	cmd.Env = append(cmd.Environ(), "__NVX_NEWPATH="+fixed)
	if out, err := cmd.CombinedOutput(); err != nil {
		return false, fmt.Errorf("set User PATH: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return true, nil
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
