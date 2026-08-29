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
	// missingPosixShims lists wrapped commands with no extensionless shim on
	// Windows. bash does not consult PATHEXT, so without one a bare `npm` in Git
	// Bash resolves straight past nvx -- while every PATHEXT-based check in this
	// report happily calls interception healthy.
	missingPosixShims []string
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
			if dirWithin(ce, root) && dirHoldsAWrappedCommand(ce) {
				rep.shadowedBy = append(rep.shadowedBy, pathShadow{dir: ce, index: i})
				break
			}
		}
	}

	if runtime.GOOS == "windows" {
		for _, c := range shimCmds {
			if _, err := os.Stat(filepath.Join(shimDir, c)); err != nil {
				rep.missingPosixShims = append(rep.missingPosixShims, c)
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

// runDoctor diagnoses shim interception against the current PATH and regenerates
// shims. It repairs a shadowed persistent PATH only when fix is set. Returns 0
// when interception is healthy after any repair, 1 when the user must act.
//
// The repair is opt-in because it edits a persistent, machine-level setting. This
// used to happen on sight: running `nvx doctor` rewrote the user's real Windows
// PATH, and because it targets whatever NVX_HOME is currently set, pointing that
// at a throwaway directory silently fronted the real PATH with it. A command
// named after diagnosis should not mutate the machine to be useful.
//
// A flag rather than a prompt, deliberately. `PromptYesNo` honours NVX_YES, which
// agents and CI set as a matter of course, so a prompt here would auto-approve a
// persistent system change for exactly the callers least able to notice it.
func runDoctor(nvxHome string, fix bool) int {
	// Diagnose BEFORE writing anything.
	//
	// This used to regenerate the shims first, which made the missing-shim check
	// below unreachable: the shims it was looking for had just been recreated, so
	// it never reported one missing and never counted one against health. Deleting
	// every extensionless shim and running `nvx doctor` printed "nvx is
	// intercepting commands correctly" and exit 0, having silently put them back.
	//
	// It also meant a command named after diagnosis wrote files on every run, which
	// is the same objection that moved the PATH repair behind --fix. Regeneration
	// is now part of the repair, not part of the report.
	cmds := coreShimCommands()
	rep := diagnosePath(os.Getenv("PATH"), nvxHome, cmds)
	fmt.Print(formatDoctorReport(rep))

	// Machine state that weakens containment without breaking anything visible --
	// a loopback exemption an older `nvx setup` left behind, and grants an older
	// nvx left on this project that every sandbox on the machine still holds.
	weakened := reportSandboxWeakeners(nvxHome)
	if reportStaleProjectGrantsHere(fix) {
		weakened = true
	}

	// A policy nvx cannot read is not a weakening -- it is a refusal to run at
	// all, which is the loudest state doctor can be asked about.
	policyBroken := reportUnreadablePolicy(nvxHome)

	// One definition, read twice: once before any repair and once after, since a
	// --fix pass can change the answer. It was written out twice instead, and the
	// second copy was missed when policyBroken was added -- doctor named the
	// unreadable policy, then fell through to the second check and exited 0
	// anyway. Closing over rep is deliberate: --fix reassigns it.
	healthyNow := func() bool {
		return rep.shimDirOnPath && len(rep.shadowedBy) == 0 &&
			len(rep.missingPosixShims) == 0 && !weakened && !policyBroken
	}

	if healthyNow() {
		LogSuccess("nvx is intercepting commands correctly.")
		return 0
	}

	if fix {
		if err := generateShims(nvxHome); err != nil {
			LogWarn("Could not regenerate shims: %v", err)
		} else if len(rep.missingPosixShims) > 0 {
			LogSuccess("Wrote the missing shims.")
		}
		// Re-diagnose so the caller is told the state after the repair rather
		// than the state that prompted it.
		rep = diagnosePath(os.Getenv("PATH"), nvxHome, cmds)
	} else if len(rep.missingPosixShims) > 0 {
		LogInfo("Run 'nvx doctor --fix' (or 'nvx init-shims') to write them.")
	}

	// Persistent-PATH repair (Windows); POSIX is a no-op. Only applied with --fix.
	if available, err := repairPersistentPath(nvxHome, fix); err != nil {
		LogWarn("Could not repair the persistent PATH automatically: %v", err)
	} else if available && fix {
		LogSuccess("Repaired your persistent PATH. Open a new terminal for it to take effect.")
	} else if available {
		LogInfo("Your persistent PATH can be repaired automatically: run 'nvx doctor --fix'.")
		LogInfo("It edits your user PATH, so nvx does not do it unless you ask.")
	}

	LogInfo("To fix the current shell now, run:")
	if runtime.GOOS == "windows" {
		LogInfo(`  $env:PATH = "%s;$env:PATH"`, shimDirPath(nvxHome))
	} else {
		LogInfo(`  export PATH="%s:$PATH"`, shimDirPath(nvxHome))
	}
	LogInfo("Ensure your shell profile contains:  eval \"$(nvx env)\"  (or the PowerShell equivalent).")

	// After a --fix pass the shims may now be complete even though PATH still is
	// not, so report on what is left rather than on what was found first.
	if healthyNow() {
		LogSuccess("nvx is intercepting commands correctly.")
		return 0
	}
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
		// Not "first on PATH": nothing here checks that, and saying so while
		// printing "position 53" contradicted itself on the page. What the branch
		// above actually established is that no nvx raw-runtime directory sits
		// ahead of the shim dir -- position is irrelevant, and someone diagnosing
		// a PATH problem was being told the opposite.
		fmt.Fprintf(&b, "  [OK]   shim dir is on PATH at position %d, with no raw-runtime dir ahead of it\n", rep.shimDirIndex)
	}

	if len(rep.missingPosixShims) > 0 {
		b.WriteString("  [FAIL] no bash shim for: " + strings.Join(rep.missingPosixShims, ", ") + "\n")
		b.WriteString("         Git Bash ignores PATHEXT, so these run unwrapped there.\n")
		b.WriteString("         Fix: nvx init-shims\n")
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

// pathIsShadowed reports whether a raw-runtime dir precedes the shim dir on the
// given PATH (a partially-broken interception setup).
// dirHoldsAWrappedCommand reports whether a directory actually contains one of
// the commands nvx intercepts.
//
// Being inside a runtime tree is not enough. `npm run` puts
// `<runtime>/node_modules/npm/node_modules/@npmcli/run-script/lib/node-gyp-bin`
// on the child's PATH, ahead of the shim dir -- measured at index 7 against the
// shim dir's 61. It is inside the runtime, so it counted as shadowing, and every
// `npm run` warned that "some commands may bypass nvx" and pointed at `nvx
// doctor`, which then reported the PATH healthy because it reads the user's PATH
// where that entry does not exist. The user was told to run a diagnostic that
// contradicted the warning, about a PATH entry nvx's own child process had
// inherited from npm.
//
// That directory holds `node-gyp` and nothing else, so it cannot shadow
// anything. Asking what a directory contains is the question that was meant all
// along -- "is it under a runtime root" was a proxy for it, and the proxy was
// wrong. Only directories already inside a runtime root reach here, so this is
// at most a handful of stats on a path that usually finds none.
func dirHoldsAWrappedCommand(dir string) bool {
	for _, c := range allShimCommands() {
		if _, err := os.Stat(filepath.Join(dir, c)); err == nil {
			return true
		}
		if runtime.GOOS == "windows" {
			if _, err := os.Stat(filepath.Join(dir, c+".exe")); err == nil {
				return true
			}
			if _, err := os.Stat(filepath.Join(dir, c+".cmd")); err == nil {
				return true
			}
		}
	}
	return false
}

// reportUnreadablePolicy names any policy file nvx cannot parse, and reports
// whether it found one.
//
// When a policy will not load, nvx refuses to run, and the refusal an MCP client
// receives says "Check it with `nvx doctor`". Doctor did not look at policy files
// at all -- it reported PATH and shim interception and exited 0 with a broken
// `.nvx-policy.json` sitting in the working directory. The path and the parse
// error existed, but only on stderr, which is the stream an MCP client discards,
// so the one thing the person needed was in the one place they could not see.
// An acceptance pass followed nvx's own advice and arrived nowhere.
//
// Doctor is the right place for it rather than the message: it already runs when
// everything else refuses -- loading a policy is not on its path -- so it is the
// one command that can still answer the question.
//
// The errors from these two readers already name the file, so the message does
// not repeat it.
func reportUnreadablePolicy(nvxHome string) bool {
	found := false
	if _, err := loadGlobalPolicy(nvxHome); err != nil {
		LogError("A security policy could not be read: %v", err)
		found = true
	}
	if cwd, err := os.Getwd(); err == nil {
		for _, path := range collectProjectPolicyPaths(cwd, nvxHome) {
			if _, _, perr := readProjectPolicyFile(path); perr != nil {
				LogError("A security policy could not be read: %v", perr)
				found = true
			}
		}
	}
	if found {
		LogInfo("nvx refuses to run while a policy file cannot be read. Fix the file above, or remove it.")
	}
	return found
}

func pathIsShadowed(pathEnv, nvxHome string) bool {
	rep := diagnosePath(pathEnv, nvxHome, nil)
	return rep.shimDirOnPath && len(rep.shadowedBy) > 0
}

func shadowHintMarkerPath(nvxHome string) string {
	return filepath.Join(nvxHome, "shadow-hint-shown")
}

// hintIfShadowed warns once per ongoing occurrence of PATH shadowing the shim
// dir, so a wrapped command that happens to still route through nvx nudges the
// user to run `nvx doctor` before a future command bypasses it.
//
// The guard is a marker file, not an in-process sync.Once: a single user-facing
// command routinely spawns a whole tree of nvx shim processes (an npm lifecycle
// script alone can nest prepublishOnly -> build -> clean -> node, each its own
// process), so a per-process guard reprints the same warning once per process in
// that tree instead of once. The marker is removed as soon as the condition
// clears, so it re-arms if shadowing recurs later rather than going silent
// forever after the first sighting.
func hintIfShadowed(nvxHome string) {
	marker := shadowHintMarkerPath(nvxHome)
	if !pathIsShadowed(os.Getenv("PATH"), nvxHome) {
		_ = os.Remove(marker)
		return
	}
	if _, err := os.Stat(marker); err == nil {
		return // already shown for this ongoing occurrence
	}
	LogWarn("A runtime dir is ahead of nvx's shim dir on PATH; some commands may bypass nvx. Run: nvx doctor")
	_ = os.WriteFile(marker, nil, 0o600)
}

// repairPersistentPath is a variable so tests can stop it touching the real
// machine.
//
// It writes HKCU\Environment\Path on Windows. A test called runDoctor(home,
// true), which reaches it, so every `go test` on a developer's Windows box
// prepended a dead temp directory to their actual user PATH -- 38 of them had
// accumulated on the machine an acceptance pass measured, in a registry value
// with a hard practical size limit. The test's throwaway NVX_HOME did not make
// the write throwaway.
//
// The seam is deliberately at this boundary: everything above it, including the
// decision of whether a repair is available and the report the user reads, still
// runs for real in tests. Only the machine-wide write is replaceable.
var repairPersistentPath = repairPersistentPathImpl
