package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// pathResolveExts returns the executable extensions to try when resolving a
// command name against PATH, mirroring how a shell would. On Windows the shim
// dir holds "<cmd>.cmd"/".ps1" and runtimes hold "<cmd>.exe"; the order matters
// only within a single directory (first directory on PATH always wins).
func pathResolveExts() []string {
	if runtime.GOOS == "windows" {
		return []string{".com", ".exe", ".bat", ".cmd", ".ps1", ""}
	}
	return []string{""}
}

// resolveCommandOnPath returns the absolute path a shell would execute for name,
// scanning pathEnv left to right. It does not consult or mutate the process
// environment, so it is safe to call for diagnosis. Returns "" if unresolved.
func resolveCommandOnPath(name, pathEnv string) string {
	exts := pathResolveExts()
	for _, dir := range filepath.SplitList(pathEnv) {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		for _, ext := range exts {
			cand := filepath.Join(dir, name+ext)
			info, err := os.Stat(cand)
			if err != nil || info.IsDir() {
				continue
			}
			if runtime.GOOS == "windows" || info.Mode()&0111 != 0 {
				return cand
			}
		}
	}
	return ""
}

// commandResolution records where a wrapped command resolves on PATH.
type commandResolution struct {
	name     string
	resolved string // absolute path a shell would run; "" if not found
	viaShim  bool   // resolved path lives inside the nvx shim dir
}

// pathShadow is a raw-runtime PATH entry that precedes the shim dir.
type pathShadow struct {
	dir   string
	index int
}

// doctorReport is the diagnosis of nvx's shim interception for one PATH value.
type doctorReport struct {
	shimDir       string
	shimDirOnPath bool
	shimDirIndex  int // index of the shim dir in PATH; -1 if absent
	shadowedBy    []pathShadow
	commands      []commandResolution
}

// dirsEqual reports whether two directory paths are the same after cleaning
// (case-insensitive on Windows).
func dirsEqual(a, b string) bool {
	ca, cb := filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(ca, cb)
	}
	return ca == cb
}

// dirWithin reports whether path is at or below base after cleaning.
func dirWithin(path, base string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return rel == "." || !strings.HasPrefix(rel, "..")
}

// nvxRuntimeDirs lists the raw-runtime roots that must never precede the shim
// dir: the global-default junction and the per-version install tree.
func nvxRuntimeDirs(nvxHome string) []string {
	return []string{
		filepath.Join(nvxHome, "current"),
		filepath.Join(nvxHome, "versions"),
	}
}

// diagnosePath analyzes a PATH string for shim-interception health. shimCmds is
// the set of wrapped command names to resolve (pass nil to skip per-command
// resolution). It never touches the process environment.
func diagnosePath(pathEnv, nvxHome string, shimCmds []string) doctorReport {
	shimDir := filepath.Join(nvxHome, "bin")
	entries := filepath.SplitList(pathEnv)
	rep := doctorReport{shimDir: shimDir, shimDirIndex: -1}

	for i, e := range entries {
		if strings.TrimSpace(e) == "" {
			continue
		}
		if dirsEqual(e, shimDir) {
			rep.shimDirOnPath = true
			rep.shimDirIndex = i
			break
		}
	}

	roots := nvxRuntimeDirs(nvxHome)
	for i, e := range entries {
		if strings.TrimSpace(e) == "" {
			continue
		}
		if rep.shimDirIndex != -1 && i >= rep.shimDirIndex {
			break // only entries before the shim dir can shadow it
		}
		ce := filepath.Clean(e)
		for _, root := range roots {
			if dirWithin(ce, root) {
				rep.shadowedBy = append(rep.shadowedBy, pathShadow{dir: ce, index: i})
				break
			}
		}
	}

	for _, c := range shimCmds {
		resolved := resolveCommandOnPath(c, pathEnv)
		rep.commands = append(rep.commands, commandResolution{
			name:     c,
			resolved: resolved,
			viaShim:  resolved != "" && dirsEqual(filepath.Dir(resolved), shimDir),
		})
	}
	return rep
}

// shimPathPrependSnippet returns shell code that removes any existing shim-dir
// entry from PATH and prepends it, guaranteeing the shim dir wins command
// resolution in every new shell — independent of `nvx use`/`auto`. shimDir is
// the OS-native path; POSIX shells receive a Git-Bash-style path on Windows.
func shimPathPrependSnippet(shell, shimDir string) string {
	if shell == "bash" || shell == "zsh" {
		dir := shimDir
		if runtime.GOOS == "windows" {
			dir = ToBashPath(shimDir)
		}
		// ${PATH//:x:/:} works in both bash and zsh; the wrapping colons let it
		// match the first/last entry too.
		return "__nvx_bin=" + quotePOSIXShell(dir) + "\n" +
			`PATH=":$PATH:"; PATH="${PATH//:$__nvx_bin:/:}"; PATH="${PATH#:}"; PATH="${PATH%:}"` + "\n" +
			`export PATH="$__nvx_bin:$PATH"` + "\n"
	}
	// PowerShell default.
	dir := quotePowerShell(shimDir)
	return "$__nvx_bin = " + dir + "\n" +
		`$env:PATH = (($env:PATH -split ';') | Where-Object { $_ -and ($_.TrimEnd('\') -ne $__nvx_bin.TrimEnd('\')) }) -join ';'` + "\n" +
		`$env:PATH = "$__nvx_bin;$env:PATH"` + "\n"
}

// runDoctor diagnoses shim interception against the current PATH, regenerates
// shims, and repairs a shadowed persistent PATH where it can. Returns 0 when
// interception is healthy after any repair, 1 when the user must act manually.
func runDoctor(nvxHome string) int {
	if err := generateShims(nvxHome); err != nil {
		LogWarn("Could not regenerate shims: %v", err)
	}

	cmds := coreShimCommands()
	rep := diagnosePath(os.Getenv("PATH"), nvxHome, cmds)
	fmt.Print(formatDoctorReport(rep))

	healthy := rep.shimDirOnPath && len(rep.shadowedBy) == 0
	if healthy {
		LogSuccess("nvx is intercepting commands correctly.")
		return 0
	}

	// Attempt a persistent-PATH repair (Windows); POSIX is a no-op.
	if changed, err := repairPersistentPath(nvxHome); err != nil {
		LogWarn("Could not repair the persistent PATH automatically: %v", err)
	} else if changed {
		LogSuccess("Repaired your persistent PATH. Open a new terminal for it to take effect.")
	}

	LogInfo("To fix the current shell now, run:")
	if runtime.GOOS == "windows" {
		LogInfo(`  $env:PATH = "%s;$env:PATH"`, shimDirPath(nvxHome))
	} else {
		LogInfo(`  export PATH="%s:$PATH"`, shimDirPath(nvxHome))
	}
	LogInfo("Ensure your shell profile contains:  eval \"$(nvx env)\"  (or the PowerShell equivalent).")
	return 1
}

// coreShimCommands is the subset of wrapped commands worth resolving in the
// doctor report — the ones users invoke directly. Falls back to all shim
// commands if the registry names none of them.
func coreShimCommands() []string {
	want := map[string]bool{"node": true, "npm": true, "npx": true, "bun": true, "bunx": true}
	var out []string
	for _, c := range allShimCommands() {
		if want[strings.ToLower(c)] {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return allShimCommands()
	}
	return out
}

// shimDirPath returns the nvx shim directory (~/.nvx/bin).
func shimDirPath(nvxHome string) string {
	return filepath.Join(nvxHome, "bin")
}

// rebuildUserPath returns a PATH with every dropDir entry removed and shimDir
// moved to the front (deduplicated). Used to repair a persistent PATH where a
// raw-runtime dir shadows the shim dir. Comparison is case-insensitive on
// Windows via dirsEqual. Separator is the OS list separator.
func rebuildUserPath(existing, shimDir string, dropDirs []string) string {
	sep := string(os.PathListSeparator)
	var kept []string
	for _, e := range strings.Split(existing, sep) {
		if strings.TrimSpace(e) == "" {
			continue
		}
		if dirsEqual(e, shimDir) {
			continue // will be re-added at the front
		}
		drop := false
		for _, d := range dropDirs {
			if dirsEqual(e, d) || dirWithin(filepath.Clean(e), filepath.Clean(d)) {
				drop = true
				break
			}
		}
		if drop {
			continue
		}
		kept = append(kept, e)
	}
	return strings.Join(append([]string{shimDir}, kept...), sep)
}

// formatDoctorReport renders a doctorReport as a human-readable, plain-text
// summary (no ANSI, so it is stable to assert on and pipe-friendly).
func formatDoctorReport(rep doctorReport) string {
	var b strings.Builder
	b.WriteString("nvx doctor — shim interception\n")
	fmt.Fprintf(&b, "  shim dir: %s\n", rep.shimDir)

	switch {
	case !rep.shimDirOnPath:
		b.WriteString("  [FAIL] shim dir is not on PATH\n")
	case len(rep.shadowedBy) > 0:
		b.WriteString("  [FAIL] shim dir is shadowed by raw-runtime dirs ahead of it:\n")
		for _, s := range rep.shadowedBy {
			fmt.Fprintf(&b, "         - %s (PATH position %d)\n", s.dir, s.index)
		}
	default:
		fmt.Fprintf(&b, "  [OK]   shim dir is first on PATH (position %d)\n", rep.shimDirIndex)
	}

	if len(rep.commands) > 0 {
		b.WriteString("  commands:\n")
		for _, c := range rep.commands {
			switch {
			case c.resolved == "":
				fmt.Fprintf(&b, "    [--]  %s: not found on PATH\n", c.name)
			case c.viaShim:
				fmt.Fprintf(&b, "    [OK]  %s -> %s\n", c.name, c.resolved)
			default:
				fmt.Fprintf(&b, "    [FAIL] %s -> %s (bypasses nvx)\n", c.name, c.resolved)
			}
		}
	}
	return b.String()
}
