# Shim Interception (Containment v2, Part 1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Guarantee `~/.nvx/bin` is unconditionally first on `PATH` so nvx actually intercepts `node`/`npm`/`npx`/`bun`/etc., and add `nvx doctor` to diagnose and repair interception.

**Architecture:** Three cooperating pieces. (1) A set of *pure, table-tested* diagnosis helpers (`resolveCommandOnPath`, `diagnosePath`) that reason about a PATH string without touching the process environment. (2) `nvx env` gains a shell-init preamble that dedups and fronts the shim dir on every new shell — independent of `nvx use`/`auto`. (3) A new `nvx doctor` command that reports per-command resolution, regenerates shims, and (on Windows) repairs the persistent User PATH. The installers stop putting the raw runtime dir (`~/.nvx/current`) on PATH, since the shims resolve the active runtime themselves.

**Tech Stack:** Go 1.23 (stdlib only), `go test`, PowerShell/POSIX shell snippets emitted as strings.

**Scope note:** This plan covers **Part 1 only** of the [containment model v2 spec](../specs/2026-07-07-containment-model-v2-design.md). Part 1 is ship-blocking and produces working, testable software on its own. Parts 2–5 (operation classification, containment levels, containment profile, unified grants) get their own follow-on plans once this lands.

---

## Why this is the first fix

`shouldSandbox` (shim_options.go:41) and the whole security layer only run when a wrapped command routes through `nvx shim <cmd>`. That routing exists *only* if `~/.nvx/bin` wins command resolution. Today two things undermine that:

1. **`nvx env` never touches PATH.** `runEnv` (main.go:602) defines the `nvx` shell function and directory-change hooks, but the shim dir only reaches PATH when `CleanAndBuildPath` runs — which happens on `nvx use`/`auto`, i.e. only after a version switch. A fresh shell in a directory with no `.nvmrc` never gets the shim dir fronted by nvx itself; it relies entirely on the installer's persistent PATH edit.
2. **The Windows installer also prepends `~/.nvx/current`** (install.ps1:40-51, :57) — the raw runtime junction that contains `node.exe`, `npm.cmd`, `npx.cmd`. Depending on how PATH is later rebuilt, that raw dir can precede `~/.nvx/bin`, so `npm`/`npx` run raw and nvx silently does nothing.

The fix: front the shim dir at every shell init, remove the raw-runtime dir from persistent PATH, and give users `nvx doctor` to detect and repair a broken setup.

---

## File Structure

- **Create `doctor.go`** — cross-platform: the pure diagnosis helpers (`resolveCommandOnPath`, `pathResolveExts`, `dirsEqual`, `dirWithin`, `nvxRuntimeDirs`, `diagnosePath`, the `doctorReport`/`commandResolution`/`pathShadow` types, `formatDoctorReport`), the shell-init snippet generator (`shimPathPrependSnippet`), and `runDoctor`.
- **Create `doctor_test.go`** — table tests for every pure helper above.
- **Create `doctor_windows.go`** — Windows-only persistent User PATH repair (`repairPersistentPath`, `rebuildUserPath`).
- **Create `doctor_other.go`** — non-Windows stub for `repairPersistentPath`.
- **Modify `main.go`** — refactor `runEnv` to build its script via a new pure `envScript(shell, exePath, shimDir)`; add `case "doctor"`; add `doctor` to `printHelp` and `commandHelpText`.
- **Modify `install.ps1`** — stop adding `~/.nvx/current` to persistent + session PATH.
- **Modify `env.go`** — optional one-time precedence hint in `runShim` (last task).

Each task builds and `go test ./...` stays green.

---

### Task 1: `resolveCommandOnPath` — resolve a command against a given PATH string

**Files:**
- Create: `doctor.go`
- Test: `doctor_test.go`

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveCommandOnPath(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()

	// Command name differs by platform: on Windows shims are "<cmd>.cmd".
	shimName := "npm"
	if runtime.GOOS == "windows" {
		shimName = "npm.cmd"
	} else {
		shimName = "npm"
	}
	writeExec(t, filepath.Join(dirA, shimName))
	writeExec(t, filepath.Join(dirB, shimName))

	pathEnv := dirA + string(os.PathListSeparator) + dirB
	got := resolveCommandOnPath("npm", pathEnv)
	want := filepath.Join(dirA, shimName)
	if got != want {
		t.Fatalf("resolveCommandOnPath = %q, want %q (first dir wins)", got, want)
	}

	if resolveCommandOnPath("does-not-exist", pathEnv) != "" {
		t.Fatalf("expected empty for missing command")
	}
}

// writeExec creates an executable file (0755) so Unix resolution accepts it.
func writeExec(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0755); err != nil { // #nosec G306 -- test fixture
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestResolveCommandOnPath`
Expected: FAIL — `undefined: resolveCommandOnPath`

- [ ] **Step 3: Write minimal implementation**

Create `doctor.go`:

```go
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
		return []string{".COM", ".EXE", ".BAT", ".CMD", ".PS1", ""}
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run TestResolveCommandOnPath`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add doctor.go doctor_test.go
git commit -m "doctor: resolve a command against a PATH string (pure helper)"
```

---

### Task 2: `diagnosePath` — report shim-dir presence, precedence, and shadowing

**Files:**
- Modify: `doctor.go`
- Test: `doctor_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestDiagnosePath(t *testing.T) {
	nvxHome := t.TempDir()
	shimDir := filepath.Join(nvxHome, "bin")
	current := filepath.Join(nvxHome, "current")
	if err := os.MkdirAll(shimDir, 0755); err != nil { // #nosec G301 -- test fixture
		t.Fatal(err)
	}
	if err := os.MkdirAll(current, 0755); err != nil { // #nosec G301 -- test fixture
		t.Fatal(err)
	}

	// Healthy: shim dir first, current after.
	healthy := shimDir + string(os.PathListSeparator) + current
	rep := diagnosePath(healthy, nvxHome, nil)
	if !rep.shimDirOnPath || rep.shimDirIndex != 0 {
		t.Fatalf("healthy: shimDirOnPath=%v index=%d, want true/0", rep.shimDirOnPath, rep.shimDirIndex)
	}
	if len(rep.shadowedBy) != 0 {
		t.Fatalf("healthy: want no shadowing, got %+v", rep.shadowedBy)
	}

	// Broken: current before shim dir -> shadowing reported.
	broken := current + string(os.PathListSeparator) + shimDir
	rep = diagnosePath(broken, nvxHome, nil)
	if !rep.shimDirOnPath || rep.shimDirIndex != 1 {
		t.Fatalf("broken: index=%d, want 1", rep.shimDirIndex)
	}
	if len(rep.shadowedBy) != 1 || rep.shadowedBy[0].index != 0 {
		t.Fatalf("broken: want current shadowing at index 0, got %+v", rep.shadowedBy)
	}

	// Absent: shim dir not on PATH at all.
	rep = diagnosePath(current, nvxHome, nil)
	if rep.shimDirOnPath {
		t.Fatalf("absent: shimDirOnPath should be false")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestDiagnosePath`
Expected: FAIL — `undefined: diagnosePath`

- [ ] **Step 3: Write minimal implementation**

Append to `doctor.go`:

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run TestDiagnosePath`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add doctor.go doctor_test.go
git commit -m "doctor: diagnose shim-dir precedence and runtime-dir shadowing"
```

---

### Task 3: `shimPathPrependSnippet` — shell code that dedups and fronts the shim dir

**Files:**
- Modify: `doctor.go`
- Test: `doctor_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestShimPathPrependSnippet(t *testing.T) {
	// POSIX: must reference the bash-form dir and export PATH with it in front.
	bash := shimPathPrependSnippet("bash", "/home/u/.nvx/bin")
	if !strings.Contains(bash, "/home/u/.nvx/bin") {
		t.Fatalf("bash snippet missing shim dir: %s", bash)
	}
	if !strings.Contains(bash, "export PATH=") {
		t.Fatalf("bash snippet must export PATH: %s", bash)
	}

	// PowerShell: must filter the existing entry and reassign $env:PATH.
	ps := shimPathPrependSnippet("powershell", `C:\Users\u\.nvx\bin`)
	if !strings.Contains(ps, `.nvx\bin`) {
		t.Fatalf("powershell snippet missing shim dir: %s", ps)
	}
	if !strings.Contains(ps, "$env:PATH") {
		t.Fatalf("powershell snippet must set $env:PATH: %s", ps)
	}
}
```

Add `"strings"` to the `doctor_test.go` import block if not already present.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestShimPathPrependSnippet`
Expected: FAIL — `undefined: shimPathPrependSnippet`

- [ ] **Step 3: Write minimal implementation**

Append to `doctor.go`:

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run TestShimPathPrependSnippet`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add doctor.go doctor_test.go
git commit -m "doctor: shell snippet that dedups and fronts the shim dir"
```

---

### Task 4: Wire the snippet into `nvx env` via a pure `envScript`

**Files:**
- Modify: `main.go:602-705` (`runEnv`)
- Test: `nvx_test.go`

- [ ] **Step 1: Write the failing test**

Add to `nvx_test.go`:

```go
func TestEnvScriptFrontsShimDir(t *testing.T) {
	bash := envScript("bash", "/opt/nvx", "/home/u/.nvx/bin")
	if !strings.Contains(bash, "export PATH=") || !strings.Contains(bash, "/home/u/.nvx/bin") {
		t.Fatalf("bash env script must front the shim dir:\n%s", bash)
	}
	// The nvx shell function must still be emitted.
	if !strings.Contains(bash, "nvx() {") {
		t.Fatalf("bash env script must still define the nvx function")
	}

	ps := envScript("powershell", `C:\opt\nvx.exe`, `C:\Users\u\.nvx\bin`)
	if !strings.Contains(ps, "$env:PATH") || !strings.Contains(ps, `.nvx\bin`) {
		t.Fatalf("powershell env script must front the shim dir:\n%s", ps)
	}
	if !strings.Contains(ps, "function nvx {") {
		t.Fatalf("powershell env script must still define the nvx function")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestEnvScriptFrontsShimDir`
Expected: FAIL — `undefined: envScript`

- [ ] **Step 3: Refactor `runEnv` to delegate to `envScript`**

Replace the body of `runEnv` (main.go:602-705) with:

```go
func runEnv(shell string, nvxHome string) {
	if err := generateShims(nvxHome); err != nil {
		LogWarn("Failed to generate PATH shims: %v", err)
	}

	exePath, err := os.Executable()
	if err != nil {
		exePath = "nvx"
	}
	fmt.Print(envScript(shell, exePath, filepath.Join(nvxHome, "bin")))
}

// envScript builds the shell integration script printed by `nvx env`. It fronts
// the shim dir on PATH (so nvx intercepts commands in every new shell) and then
// defines the `nvx` function plus directory-change auto-switch hooks. exePath is
// the nvx binary; shimDir is ~/.nvx/bin.
func envScript(shell, exePath, shimDir string) string {
	exe := strings.ReplaceAll(exePath, "\\", "/")
	prepend := shimPathPrependSnippet(shell, shimDir)

	if shell == "bash" || shell == "zsh" {
		return prepend + fmt.Sprintf(`__nvx_shell_type() {
    if [ -n "$ZSH_VERSION" ]; then
        echo "zsh"
    else
        echo "bash"
    fi
}

nvx() {
    local cmd="$1"
    if [ "$cmd" = "use" ] || [ "$cmd" = "auto" ]; then
        local stdout
        stdout=$(%q "$@" --shell="$(__nvx_shell_type)")
        if [ -n "$stdout" ]; then
            eval "$stdout"
        fi
    else
        %q "$@"
    fi
}

nvx_prompt_hook() {
    local exit_code=$?
    if [ "$PWD" != "$__nvx_last_pwd" ]; then
        export __nvx_last_pwd="$PWD"
        local stdout
        stdout=$(%q auto --shell="$(__nvx_shell_type)")
        if [ -n "$stdout" ]; then
            eval "$stdout"
        fi
    fi
    return $exit_code
}

if [[ -n "$ZSH_VERSION" ]]; then
    # Optimize using native zsh chpwd hook instead of prompt command
    nvx_chpwd_hook() {
        local stdout
        stdout=$(%q auto --shell=zsh)
        if [ -n "$stdout" ]; then
            eval "$stdout"
        fi
    }
    autoload -U add-zsh-hook
    add-zsh-hook chpwd nvx_chpwd_hook
elif [[ -n "$BASH_VERSION" ]]; then
    if [[ ! "$PROMPT_COMMAND" =~ nvx_prompt_hook ]]; then
        PROMPT_COMMAND="nvx_prompt_hook; $PROMPT_COMMAND"
    fi
fi
`, exe, exe, exe, exe)
	}

	// PowerShell default
	return prepend + fmt.Sprintf(`$global:__nvx_last_pwd = ""

function nvx {
    $cmd = $args[0]
    if ($cmd -eq "use" -or $cmd -eq "auto") {
        $stdout = & %q @args --shell=powershell
        if ($stdout) {
            $stdout | Out-String | Invoke-Expression
        }
    } else {
        & %q @args
    }
}

function nvx_prompt_hook {
    if ($global:__nvx_last_pwd -ne $pwd) {
        $global:__nvx_last_pwd = $pwd
        $stdout = & %q auto --shell=powershell
        if ($stdout) {
            $stdout | Out-String | Invoke-Expression
        }
    }
}

if (Test-Path Function:\prompt) {
    $old_prompt = $function:prompt
    $function:prompt = {
        nvx_prompt_hook
        . $old_prompt
    }
} else {
    $function:prompt = {
        nvx_prompt_hook
        "PS $pwd> "
    }
}
`, exe, exe, exe)
}
```

Confirm `main.go` already imports `path/filepath` (it does — used elsewhere). If not, add it.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run TestEnvScriptFrontsShimDir`
Expected: PASS

- [ ] **Step 5: Run the full suite and build**

Run: `go build ./... && go test ./...`
Expected: build OK, all tests PASS

- [ ] **Step 6: Commit**

```bash
git add main.go nvx_test.go
git commit -m "env: front the shim dir on PATH at every shell init"
```

---

### Task 5: `formatDoctorReport` — human-readable diagnosis output

**Files:**
- Modify: `doctor.go`
- Test: `doctor_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestFormatDoctorReport(t *testing.T) {
	shimDir := filepath.FromSlash("/home/u/.nvx/bin")
	healthy := doctorReport{
		shimDir: shimDir, shimDirOnPath: true, shimDirIndex: 0,
		commands: []commandResolution{
			{name: "npm", resolved: filepath.Join(shimDir, "npm"), viaShim: true},
		},
	}
	out := formatDoctorReport(healthy)
	if !strings.Contains(out, "npm") || !strings.Contains(strings.ToLower(out), "ok") {
		t.Fatalf("healthy report should mark npm OK:\n%s", out)
	}

	broken := doctorReport{
		shimDir: shimDir, shimDirOnPath: true, shimDirIndex: 2,
		shadowedBy: []pathShadow{{dir: filepath.FromSlash("/home/u/.nvx/current"), index: 0}},
		commands: []commandResolution{
			{name: "npm", resolved: filepath.FromSlash("/home/u/.nvx/current/npm"), viaShim: false},
		},
	}
	out = formatDoctorReport(broken)
	if !strings.Contains(out, "current") {
		t.Fatalf("broken report should name the shadowing dir:\n%s", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestFormatDoctorReport`
Expected: FAIL — `undefined: formatDoctorReport`

- [ ] **Step 3: Write minimal implementation**

Append to `doctor.go`:

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run TestFormatDoctorReport`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add doctor.go doctor_test.go
git commit -m "doctor: format a human-readable interception report"
```

---

### Task 6: Windows persistent-PATH repair — `rebuildUserPath` (pure) + `repairPersistentPath`

**Files:**
- Create: `doctor_windows.go`
- Create: `doctor_other.go`
- Test: `doctor_test.go`

- [ ] **Step 1: Write the failing test**

The repair logic is a pure string transform, testable on any OS:

```go
func TestRebuildUserPath(t *testing.T) {
	shimDir := `C:\Users\u\.nvx\bin`
	current := `C:\Users\u\.nvx\current`
	other := `C:\Windows\System32`

	// current is ahead of the shim dir and must be dropped; shim dir must lead.
	existing := current + ";" + other + ";" + shimDir
	got := rebuildUserPath(existing, shimDir, []string{current})
	want := shimDir + ";" + other
	if got != want {
		t.Fatalf("rebuildUserPath = %q, want %q", got, want)
	}

	// Idempotent: a healthy PATH is unchanged.
	if again := rebuildUserPath(got, shimDir, []string{current}); again != want {
		t.Fatalf("rebuildUserPath not idempotent: %q", again)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestRebuildUserPath`
Expected: FAIL — `undefined: rebuildUserPath`

- [ ] **Step 3: Write the pure helper in `doctor.go`**

`rebuildUserPath` is cross-platform (pure string work), so it lives in `doctor.go`. Append:

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run TestRebuildUserPath`
Expected: PASS

- [ ] **Step 5: Add the platform repair entry points**

Create `doctor_windows.go`:

```go
//go:build windows

package main

import (
	"os/exec"
	"strings"
	"time"
)

// repairPersistentPath rewrites the User PATH environment variable so the shim
// dir leads and raw-runtime dirs no longer shadow it. Returns true if it made a
// change. Uses setx via the current (non-elevated) user; new shells pick it up.
func repairPersistentPath(nvxHome string) (bool, error) {
	shimDir := shimDirPath(nvxHome)
	out, err := runWinCmd(15*time.Second, "reg", "query", `HKCU\Environment`, "/v", "Path")
	if err != nil {
		return false, err
	}
	existing := parseRegPath(string(out))
	fixed := rebuildUserPath(existing, shimDir, nvxRuntimeDirs(nvxHome))
	if dirsEqual(existing, fixed) || existing == fixed {
		return false, nil
	}
	// setx truncates at 1024 chars; use PowerShell's [Environment] setter which
	// does not, matching what install.ps1 uses.
	ps := "[Environment]::SetEnvironmentVariable('Path', $env:__NVX_NEWPATH, 'User')"
	cmd := exec.Command("powershell", "-NoProfile", "-Command", ps)
	cmd.Env = append(cmd.Environ(), "__NVX_NEWPATH="+fixed)
	if _, err := cmd.CombinedOutput(); err != nil {
		return false, err
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
```

Create `doctor_other.go`:

```go
//go:build !windows

package main

// repairPersistentPath is a no-op on non-Windows: POSIX shells get the shim dir
// fronted by the `nvx env` snippet in the user's profile, so there is no
// separate persistent PATH store to repair.
func repairPersistentPath(nvxHome string) (bool, error) {
	return false, nil
}
```

Add the small shared helper to `doctor.go`:

```go
// shimDirPath returns the nvx shim directory (~/.nvx/bin).
func shimDirPath(nvxHome string) string {
	return filepath.Join(nvxHome, "bin")
}
```

- [ ] **Step 6: Run build + full suite**

Run: `go build ./... && go test ./...`
Expected: build OK (both platform files compile via build tags), tests PASS

- [ ] **Step 7: Commit**

```bash
git add doctor.go doctor_windows.go doctor_other.go doctor_test.go
git commit -m "doctor: repair a shadowed persistent PATH (Windows) via pure rebuildUserPath"
```

---

### Task 7: `runDoctor` + `nvx doctor` command registration

**Files:**
- Modify: `doctor.go` (add `runDoctor`)
- Modify: `main.go` (dispatch, `printHelp`, `commandHelpText`)
- Test: manual (integration)

- [ ] **Step 1: Add `runDoctor` to `doctor.go`**

```go
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
```

- [ ] **Step 2: Register the command in `main.go`**

After the `case "cleanup":` block (main.go:163-166), add:

```go
	case "doctor":
		os.Exit(runDoctor(nvxHome))
```

- [ ] **Step 3: Add `doctor` to `printHelp`**

In the `Commands:` list (main.go, near :293), add after the `cleanup` line:

```
  doctor                   Check and repair that nvx intercepts node/npm/npx on PATH
```

- [ ] **Step 4: Add `doctor` help text**

In `commandHelpText` (main.go:213-239), add a case before the closing `}`:

```go
	case "doctor":
		return "nvx doctor\n\nCheck that ~/.nvx/bin is first on PATH so nvx intercepts node/npm/npx/bun.\nRegenerates shims and, on Windows, repairs a shadowed persistent PATH.\n"
```

- [ ] **Step 5: Build and smoke-test**

Run: `go build -o nvx.exe . && ./nvx.exe doctor`
Expected: prints the interception report and either "intercepting commands correctly" (exit 0) or a fix hint (exit 1).

- [ ] **Step 6: Commit**

```bash
git add doctor.go main.go
git commit -m "doctor: add 'nvx doctor' command to diagnose and repair interception"
```

---

### Task 8: Installer — stop putting the raw runtime dir on PATH (Windows)

**Files:**
- Modify: `install.ps1:40-58`

- [ ] **Step 1: Remove the `current` persistent prepend**

Delete the "Prepend current link" block (install.ps1:40-51) entirely. The shims resolve the active runtime; the raw `current` junction must not be on PATH because it shadows the shims.

- [ ] **Step 2: Fix the session PATH line**

Change install.ps1:57 from:

```powershell
    $env:PATH = "$binDir;$currentLink;$env:PATH"
```

to:

```powershell
    $env:PATH = "$binDir;$env:PATH"
```

- [ ] **Step 3: Remove the now-unused `$currentLink`**

If `$currentLink` (install.ps1:13) is no longer referenced after Steps 1–2, delete its assignment. Verify with a search for `currentLink` in `install.ps1` first; keep it only if still used elsewhere.

- [ ] **Step 4: Manual verification (no Go test — installer is PowerShell)**

In a scratch Windows shell after re-running `install.ps1`:
```
Get-Command npx | Select-Object Source
```
Expected: resolves to `~/.nvx/bin/npx.cmd` (or `.ps1`), not `~/.nvx/current`.

- [ ] **Step 5: Commit**

```bash
git add install.ps1
git commit -m "install: keep only the shim dir on PATH; drop the raw runtime dir"
```

---

### Task 9 (optional, minimal): one-time interception hint from `runShim`

**Files:**
- Modify: `env.go:527` (`runShim`)
- Modify: `doctor.go` (add a `sync.Once` guard)
- Test: `doctor_test.go`

This is the spec's "self-check on shim run — keep minimal." When a shim *does* run, warn once if the current PATH still has a raw-runtime dir ahead of the shim dir (a partially-broken setup), pointing at `nvx doctor`.

- [ ] **Step 1: Write the failing test**

```go
func TestPathIsShadowed(t *testing.T) {
	nvxHome := t.TempDir()
	shimDir := filepath.Join(nvxHome, "bin")
	current := filepath.Join(nvxHome, "current")
	sep := string(os.PathListSeparator)

	if pathIsShadowed(shimDir+sep+current, nvxHome) {
		t.Fatalf("healthy PATH should not be shadowed")
	}
	if !pathIsShadowed(current+sep+shimDir, nvxHome) {
		t.Fatalf("current ahead of shim dir should be shadowed")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestPathIsShadowed`
Expected: FAIL — `undefined: pathIsShadowed`

- [ ] **Step 3: Implement in `doctor.go`**

```go
import "sync" // add to the existing import block in doctor.go

// pathIsShadowed reports whether a raw-runtime dir precedes the shim dir on the
// given PATH (a partially-broken interception setup).
func pathIsShadowed(pathEnv, nvxHome string) bool {
	rep := diagnosePath(pathEnv, nvxHome, nil)
	return rep.shimDirOnPath && len(rep.shadowedBy) > 0
}

var shadowHintOnce sync.Once

// hintIfShadowed warns at most once per process when the current PATH shadows
// the shim dir, so a wrapped command that happens to still route through nvx
// nudges the user to run `nvx doctor` before a future command bypasses it.
func hintIfShadowed(nvxHome string) {
	if pathIsShadowed(os.Getenv("PATH"), nvxHome) {
		shadowHintOnce.Do(func() {
			LogWarn("A runtime dir is ahead of nvx's shim dir on PATH; some commands may bypass nvx. Run: nvx doctor")
		})
	}
}
```

- [ ] **Step 4: Call it from `runShim`**

In `runShim` (env.go:527), after `args = opts.args` (env.go:529), add:

```go
	hintIfShadowed(nvxHome)
```

- [ ] **Step 5: Run test + full suite**

Run: `go test ./... -run TestPathIsShadowed && go build ./... && go test ./...`
Expected: all PASS

- [ ] **Step 6: Commit**

```bash
git add doctor.go doctor_test.go env.go
git commit -m "shim: hint once when a runtime dir shadows the shim dir on PATH"
```

---

## Acceptance (from the spec)

- In a fresh shell with no `nvx use`/`auto`, `Get-Command npx` / `command -v npx` resolves to `~/.nvx/bin`.
- `nvx doctor` reports each of `node`/`npm`/`npx`/`bun` resolving via the shim, or names exactly what shadows them and how to fix it.
- Re-running the Windows installer no longer places `~/.nvx/current` on PATH.

Final gate: `go build ./... && go test ./...` green on Windows and Linux; manual `nvx doctor` on Windows shows all core commands `[OK]`.

---

## Self-Review

**Spec coverage (Part 1):**
- "shim dir unconditionally first at shell init" → Task 3 (snippet) + Task 4 (wired into `nvx env`).
- "raw runtime dir never precedes it" → Task 8 (installer) + Task 6 (persistent-PATH repair).
- "`nvx doctor` verifies resolution + precedence, repairs, else prints what to fix" → Tasks 2, 5, 6, 7.
- "self-check on shim run (optional, minimal)" → Task 9.
- Acceptance test (fresh shell resolves `npx` to shim) → covered by Tasks 4 + 8; verified manually.

**Placeholder scan:** none — every code step shows complete code; installer steps show exact before/after.

**Type consistency:** `doctorReport`/`commandResolution`/`pathShadow` defined in Task 2 and used unchanged in Tasks 5, 7. Helpers `dirsEqual`/`dirWithin`/`nvxRuntimeDirs`/`shimDirPath`/`resolveCommandOnPath` defined once and reused. `rebuildUserPath` (Task 6) and `diagnosePath` (Task 2) share `dirsEqual`/`dirWithin`. `envScript` (Task 4) consumes `shimPathPrependSnippet` (Task 3). No signature drift.

**Cross-platform:** `doctor.go` is tag-free; Windows-only repair is isolated in `doctor_windows.go` with a `doctor_other.go` stub — pure logic (`rebuildUserPath`, `diagnosePath`) is tested on any OS.
