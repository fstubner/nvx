package main

import (
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
