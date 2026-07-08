# Containment v2 Parts 2–4 (Classification, Levels, Profile Wiring) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace "sandbox every wrapped command" with "sandbox code you didn't write" — classify every invocation as `your-code` / `install` / `ad-hoc-tool`, add a `standard`/`strict` isolation level, and wire the decision into `shouldSandbox` so `npm run build` runs uncontained by default while `npm install` and `npx <tool>` stay contained.

**Architecture:** A pure classifier `classifyInvocation(cmd, args) → invocationClass` (new `classify.go`) reuses the install-subcommand detection already in `env.go`. A pure decision function `shouldContain(class, level, opts) bool` (new `containment.go`) replaces the unconditional command-name check inside `shouldSandbox` (shim_options.go). `isolation.level` is a new `Policy` field with the existing loosen/tighten trust machinery covering it for free. `nvx --strict`/`nvx --standard` are new leading flags mirroring the existing `--no-sandbox` anti-smuggling pattern.

**Tech Stack:** Go 1.23 (stdlib only), `go test`.

**Scope note:** This is **Parts 2–4** of `docs/superpowers/specs/2026-07-07-containment-model-v2-design.md`. Part 1 (shim interception, `nvx doctor`) is done — see `docs/superpowers/plans/2026-07-07-shim-interception-part1.md`. Part 5 (trusted-tool + strict-mode grants) is a separate follow-on plan; this plan does not touch `policy_persist.go`'s grant schema.

---

## Why this is next

Today `shouldSandbox` (shim_options.go:41) contains everything: if the command name is in the runtime's `ShimCommands()` list (or is a project-bin command), it is sandboxed — full stop. That means `node app.js`, `npm run build`, and `npm test` all run contained today, which is the mis-framing the owner rejected: *"what is the point of running nvx at all if the sandbox is ephemeral... it's also about installing packages and those packages have various requirements."* The fix is not "less sandboxing" — it's "sandbox the right things": code whose provenance you don't control (installs, ad-hoc `npx`/`bunx` tools) stays contained; code you wrote and are actively running (`npm run`, `node app.js`) does not, unless you opt into `strict`.

---

## File Structure

- **Create `classify.go`** — `invocationClass` type + constants, `classifyInvocation(cmd, args []string) invocationClass`. Pure, no I/O.
- **Create `classify_test.go`** — table test covering every runtime × class combination from the spec's Part 2 table.
- **Create `containment.go`** — `isolationLevel` type + constants, `parseIsolationLevel(s string) (isolationLevel, bool)`, `shouldContain(class invocationClass, level isolationLevel, opts shimOptions) bool`. Pure.
- **Create `containment_test.go`** — table test: class × level × `--no-sandbox`/`--strict`/`--standard` flag matrix.
- **Modify `policy.go`** — add `Isolation.Level` field (string, default `"standard"`), a `Policy.IsolationLevel() isolationLevel` accessor, extend `normalizePolicy`/`MergePolicies`/`policyLoosens` for the new field.
- **Modify `shim_options.go`** — `shimOptions` gains `strictFlag`/`standardFlag` (payload-smuggling guards, like `payloadNoSandbox`); `parseShimOptions` recognizes `--strict`/`--standard` in wrapped-command args and flags them as smuggled (ignored, same anti-bypass posture as `--no-sandbox`); `shouldSandbox` is rewritten to call `classifyInvocation` + `shouldContain` instead of the raw command-name check.
- **Modify `main.go`** — top-level `nvx --strict <cmd>`/`nvx --standard <cmd>` leading-flag parsing (mirrors the existing `noSandboxFlag` global), help text update.
- **Modify `policy_test.go`** — extend existing loosen/tighten and merge tests for `isolation.level`.

Each task builds and `go test ./...` stays green.

---

### Task 1: `invocationClass` type and `classifyInvocation` — the your-code/install/ad-hoc-tool table

**Files:**
- Create: `classify.go`
- Test: `classify_test.go`

- [ ] **Step 1: Write the failing test**

```go
package main

import "testing"

func TestClassifyInvocation(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		args []string
		want invocationClass
	}{
		// your-code
		{"node run file", "node", []string{"app.js"}, classYourCode},
		{"npm run script", "npm", []string{"run", "build"}, classYourCode},
		{"npm test", "npm", []string{"test"}, classYourCode},
		{"npm start", "npm", []string{"start"}, classYourCode},
		{"bun run script", "bun", []string{"run", "dev"}, classYourCode},
		{"bun run file directly", "bun", []string{"app.ts"}, classYourCode},
		{"python script", "python", []string{"x.py"}, classYourCode},

		// install
		{"npm install", "npm", []string{"install"}, classInstall},
		{"npm i shorthand", "npm", []string{"i"}, classInstall},
		{"npm ci", "npm", []string{"ci"}, classInstall},
		{"yarn add", "yarn", []string{"add", "lodash"}, classInstall},
		{"pnpm install", "pnpm", []string{"install"}, classInstall},
		{"bun install", "bun", []string{"install"}, classInstall},
		{"bun add", "bun", []string{"add", "lodash"}, classInstall},
		{"bun add short alias", "bun", []string{"a", "lodash"}, classInstall},
		{"uv add", "uv", []string{"add", "requests"}, classInstall},
		{"uv pip install", "uv", []string{"pip", "install", "requests"}, classInstall},
		{"deno add npm pkg", "deno", []string{"add", "npm:lodash"}, classInstall},

		// ad-hoc-tool
		{"npx tool", "npx", []string{"cowsay", "hi"}, classAdHocTool},
		{"bunx tool", "bunx", []string{"cowsay", "hi"}, classAdHocTool},
		{"uvx tool", "uvx", []string{"ruff", "check"}, classAdHocTool},
		{"pyx tool", "pyx", []string{"ruff", "check"}, classAdHocTool},

		// leading flags must not defeat subcommand detection
		{"npm with global flag then install", "npm", []string{"--loglevel=error", "install", "pkg"}, classInstall},
		{"npm with global flag then run", "npm", []string{"--silent", "run", "build"}, classYourCode},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyInvocation(tc.cmd, tc.args)
			if got != tc.want {
				t.Errorf("classifyInvocation(%q, %v) = %v, want %v", tc.cmd, tc.args, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestClassifyInvocation`
Expected: FAIL — `undefined: invocationClass` (or `classifyInvocation`)

- [ ] **Step 3: Write minimal implementation**

First, read `env.go`'s existing `installAliases` map and `firstNonFlagArg` function (already defined at env.go:292-296 and env.go:370-381) — reuse them, do not redefine. `packagesAfterSubcommand` (env.go:353-368) is also reusable if needed, but classification only needs the subcommand name, not the package list.

Create `classify.go`:

```go
package main

import "strings"

// invocationClass is the three-way split the containment model bases its
// contain/don't-contain decision on: code you wrote and are running, a
// package-manager install (untrusted code arriving on disk), or an ad-hoc
// third-party tool invocation (untrusted code you didn't install yourself).
type invocationClass int

const (
	classYourCode invocationClass = iota
	classInstall
	classAdHocTool
)

// executorCommands are ad-hoc tool runners: they fetch and execute a package
// that was not explicitly installed into the project, so every invocation is
// untrusted-code-by-default regardless of subcommand.
var executorCommands = map[string]bool{
	"npx": true, "bunx": true, "uvx": true, "pyx": true,
}

// runScriptSubcommands are the your-code entry points package managers expose
// for running project-owned scripts (as opposed to installing dependencies).
var runScriptSubcommands = map[string]bool{
	"run": true, "test": true, "start": true,
}

// classifyInvocation determines which containment class a wrapped command
// invocation falls into. It is subcommand-aware: the same command name (npm,
// bun, uv) can be your-code, install, or (for npx/bunx/uvx/pyx) an ad-hoc tool
// runner, depending on its first non-flag argument.
func classifyInvocation(cmd string, args []string) invocationClass {
	lower := strings.ToLower(cmd)

	if executorCommands[lower] {
		return classAdHocTool
	}

	sub := firstNonFlagArg(args)

	switch lower {
	case "npm", "yarn", "pnpm":
		if sub == "ci" || installAliases[sub] {
			return classInstall
		}
		return classYourCode
	case "bun":
		if sub == "install" || sub == "add" || sub == "a" || installAliases[sub] {
			return classInstall
		}
		return classYourCode
	case "uv":
		if sub == "add" || sub == "pip" || installAliases[sub] {
			return classInstall
		}
		return classYourCode
	case "deno":
		if sub == "add" || sub == "install" {
			return classInstall
		}
		return classYourCode
	default:
		// node, python, and any other direct runtime invocation runs the
		// script you asked it to run — that is your code by definition.
		return classYourCode
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run TestClassifyInvocation -v`
Expected: PASS for every subtest

- [ ] **Step 5: Commit**

```bash
git add classify.go classify_test.go
git commit -m "classify: subcommand-aware your-code/install/ad-hoc-tool classification"
```

---

### Task 2: `isolationLevel` type and `shouldContain` — the pure containment decision

**Files:**
- Create: `containment.go`
- Test: `containment_test.go`

- [ ] **Step 1: Write the failing test**

```go
package main

import "testing"

func TestParseIsolationLevel(t *testing.T) {
	tests := []struct {
		in      string
		want    isolationLevel
		wantOK  bool
	}{
		{"standard", levelStandard, true},
		{"Standard", levelStandard, true},
		{"", levelStandard, true},
		{"strict", levelStrict, true},
		{"STRICT", levelStrict, true},
		{"bogus", levelStandard, false},
	}
	for _, tc := range tests {
		got, ok := parseIsolationLevel(tc.in)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("parseIsolationLevel(%q) = (%v, %v), want (%v, %v)", tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestShouldContain(t *testing.T) {
	tests := []struct {
		name  string
		class invocationClass
		level isolationLevel
		opts  shimOptions
		want  bool
	}{
		{"standard your-code not contained", classYourCode, levelStandard, shimOptions{}, false},
		{"standard install contained", classInstall, levelStandard, shimOptions{}, true},
		{"standard ad-hoc-tool contained", classAdHocTool, levelStandard, shimOptions{}, true},
		{"strict your-code contained", classYourCode, levelStrict, shimOptions{}, true},
		{"strict install contained", classInstall, levelStrict, shimOptions{}, true},
		{"strict ad-hoc-tool contained", classAdHocTool, levelStrict, shimOptions{}, true},
		{"per-command --strict overrides standard level", classYourCode, levelStandard, shimOptions{strictFlag: true}, true},
		// --standard downgrades the effective level from strict to standard for
		// this call, but standard still contains installs — it must never act
		// as a blanket bypass for code you did not write.
		{"per-command --standard downgrades level but still contains installs", classInstall, levelStrict, shimOptions{standardFlag: true}, true},
		{"per-command --standard leaves your own code uncontained", classYourCode, levelStrict, shimOptions{standardFlag: true}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldContain(tc.class, tc.level, tc.opts)
			if got != tc.want {
				t.Errorf("shouldContain(%v, %v, %+v) = %v, want %v", tc.class, tc.level, tc.opts, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run 'TestParseIsolationLevel|TestShouldContain'`
Expected: FAIL — undefined symbols (`isolationLevel` etc.); also `shimOptions{strictFlag: ...}` will fail to compile until Task 3 adds those fields — for now, comment out or skip the two `--strict`/`--standard` override subtests (`t.Skip("added in Task 3")`) so this task's test compiles and the level×class matrix is verified first. Re-enable them in Task 3's step.

Adjust the test file to skip the two flag-override cases for this task:

```go
		{"per-command --strict overrides standard level", classYourCode, levelStandard, shimOptions{strictFlag: true}, true},
```
→ becomes, for this task only, guarded:
```go
	// Flag-override cases are added by Task 3 once shimOptions grows
	// strictFlag/standardFlag; classify_test.go's TestShouldContain in the
	// final file includes them directly (see Task 3, Step 1).
```

To keep this step self-contained, write `containment_test.go` in Task 2 WITHOUT the two flag-override subtests (just the six level×class cases), and Task 3 will extend it with the flag-override subtests once `shimOptions` has the new fields. This avoids a compile error mid-task.

- [ ] **Step 3: Write minimal implementation**

Create `containment.go`:

```go
package main

import "strings"

// isolationLevel selects how invocation classes map to "contained?". Standard
// (default) contains only code you did not write (installs, ad-hoc tools);
// strict extends containment to your own code too.
type isolationLevel int

const (
	levelStandard isolationLevel = iota
	levelStrict
)

// parseIsolationLevel parses a policy/flag value into an isolationLevel. An
// empty string is treated as the default (standard, ok=true). An unrecognized
// non-empty value returns ok=false so callers can warn rather than silently
// falling back.
func parseIsolationLevel(s string) (isolationLevel, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "standard":
		return levelStandard, true
	case "strict":
		return levelStrict, true
	default:
		return levelStandard, false
	}
}

func (l isolationLevel) String() string {
	if l == levelStrict {
		return "strict"
	}
	return "standard"
}

// shouldContain is the pure containment decision: given what kind of code is
// being invoked, the effective isolation level, and any per-command flag
// override, should this invocation run inside the sandbox?
func shouldContain(class invocationClass, level isolationLevel, opts shimOptions) bool {
	effectiveLevel := level
	if opts.strictFlag {
		effectiveLevel = levelStrict
	} else if opts.standardFlag {
		effectiveLevel = levelStandard
	}

	if effectiveLevel == levelStrict {
		return true
	}
	// standard: only code you did not write is contained.
	return class != classYourCode
}
```

Note: `containment.go` references `shimOptions.strictFlag`/`standardFlag`, which don't exist until Task 3. To keep Task 2 compiling standalone, temporarily use only the six level×class test cases (as instructed in Step 2) and have `shouldContain`'s signature take `opts shimOptions` from the start — Task 3 adds the two fields to `shimOptions` (in shim_options.go) so this file does not need to change again. Confirm `go build ./...` succeeds after Task 2 only if Task 3's `shimOptions` fields are added in the same pass; **if doing these as strictly separate commits, add the two `shimOptions` fields as part of Task 2** (a one-line struct change) even though `parseShimOptions` doesn't populate them until Task 3. This keeps every commit buildable. Add to `shimOptions` in shim_options.go now:

```go
	// strictFlag / standardFlag record a leading `nvx --strict`/`nvx --standard`
	// override for this invocation. Populated in Task 3; declared here so
	// shouldContain has a stable signature from its first commit.
	strictFlag   bool
	standardFlag bool
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go build ./... && go test ./... -run 'TestParseIsolationLevel|TestShouldContain' -v`
Expected: PASS for all six enabled subtests; build succeeds.

- [ ] **Step 5: Commit**

```bash
git add containment.go containment_test.go shim_options.go
git commit -m "containment: isolation levels and the pure contain/don't-contain decision"
```

---

### Task 3: Leading `--strict`/`--standard` flags with anti-smuggling, wired into `shouldSandbox`

**Files:**
- Modify: `shim_options.go`
- Modify: `main.go`
- Modify: `containment_test.go` (re-enable the two flag-override subtests)
- Modify: `remediation_test.go` (update `parseStartupFlags` call sites to the new 5-value return form)

- [ ] **Step 1: Extend `containment_test.go`**

Add the two previously-skipped subtests back into `TestShouldContain`'s table (they already appear in Task 2's Step 1 listing above — uncomment/add them now):

```go
		{"per-command --strict overrides standard level", classYourCode, levelStandard, shimOptions{strictFlag: true}, true},
		// --standard downgrades the effective level from strict to standard for
		// this call, but standard still contains installs — it must never act
		// as a blanket bypass for code you did not write.
		{"per-command --standard downgrades level but still contains installs", classInstall, levelStrict, shimOptions{standardFlag: true}, true},
		{"per-command --standard leaves your own code uncontained", classYourCode, levelStrict, shimOptions{standardFlag: true}, false},
```

Run: `go test ./... -run TestShouldContain -v`
Expected: PASS (the struct fields already exist from Task 2; this is just exercising them).

- [ ] **Step 2: Add payload-smuggling detection to `parseShimOptions`**

Read the current `shim_options.go` (already shown above) before editing. Add a `payloadStrict`/`payloadStandard` pair mirroring `payloadNoSandbox`, and parse `--strict`/`--standard` out of the wrapped command's own args (so `npm --strict install` doesn't silently do anything unexpected — it must be stripped and flagged, exactly like `--no-sandbox`):

```go
type shimOptions struct {
	filesystemProvider string
	// payloadNoSandbox records a --no-sandbox smuggled through the wrapped
	// command (e.g. `npx --no-sandbox`). It is stripped but NOT honored: only an
	// explicit `nvx --no-sandbox <cmd>` disables isolation, so nothing can bypass
	// the sandbox by tacking a flag onto a package manager.
	payloadNoSandbox bool
	// payloadStrict / payloadStandard record --strict/--standard smuggled
	// through the wrapped command's own args. Stripped but NOT honored, for the
	// same anti-bypass reason as payloadNoSandbox: only a leading
	// `nvx --strict`/`nvx --standard` (strictFlag/standardFlag) changes the
	// containment level.
	payloadStrict   bool
	payloadStandard bool
	// strictFlag / standardFlag record a leading `nvx --strict`/`nvx --standard`
	// override for this invocation.
	strictFlag   bool
	standardFlag bool
	args         []string
}

func parseShimOptions(args []string) shimOptions {
	opts := shimOptions{args: args}
	var filtered []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--no-sandbox":
			opts.payloadNoSandbox = true
		case arg == "--strict":
			opts.payloadStrict = true
		case arg == "--standard":
			opts.payloadStandard = true
		case strings.HasPrefix(arg, "--filesystem-provider="):
			opts.filesystemProvider = strings.TrimPrefix(arg, "--filesystem-provider=")
		case arg == "--filesystem-provider" && i+1 < len(args):
			opts.filesystemProvider = args[i+1]
			i++
		default:
			filtered = append(filtered, arg)
		}
	}
	opts.args = filtered
	return opts
}
```

Note `strictFlag`/`standardFlag` (the ones `shouldContain` reads) are set by the caller (`runShim` in env.go, from the leading `nvx --strict` global, added in Step 4 below) — `parseShimOptions` only ever populates the `payload*` pair, never `strictFlag`/`standardFlag` directly, so a smuggled flag can never take effect. This mirrors exactly how `noSandboxFlag` (a package-level global set by `main.go`'s argument parsing) is distinct from `payloadNoSandbox`.

- [ ] **Step 3: Rewrite `shouldSandbox` to use classification**

Replace `shouldSandbox` in shim_options.go:

```go
func shouldSandbox(cmdName string, args []string, policy Policy, opts shimOptions) bool {
	// Only a leading `nvx --no-sandbox ...` (noSandboxFlag) disables isolation;
	// a --no-sandbox smuggled into the wrapped command's args does not.
	if noSandboxFlag {
		return false
	}
	if inSandboxSession() {
		return false
	}
	if os.Getenv("NVX_SANDBOX") == "1" || os.Getenv("NVX_SANDBOX") == "true" {
		return false
	}
	if !policy.Isolation.Enabled {
		return false
	}
	provider := runtimeForShim(cmdName)
	isWrapped := isProjectBinCommand(cmdName)
	for _, c := range provider.ShimCommands() {
		if strings.EqualFold(c, cmdName) {
			isWrapped = true
			break
		}
	}
	if !isWrapped {
		return false
	}

	class := classifyInvocation(cmdName, args)
	level := policy.IsolationLevel()
	return shouldContain(class, level, opts)
}
```

This changes `shouldSandbox`'s signature (adds `args []string`) — find its one call site in env.go's `runShim` (`if shouldSandbox(cmdName, policy, opts) {`) and update it to `if shouldSandbox(cmdName, args, policy, opts) {`. `args` is already in scope there (it's `opts.args`, reassigned to the local `args` variable at env.go:529).

- [ ] **Step 4: Add the leading `nvx --strict`/`nvx --standard` flags in main.go**

main.go:13-42 is the exact mechanism to extend:

```go
var yesFlag = false

func init() {
	var yes, noSandbox bool
	os.Args, yes, noSandbox = parseStartupFlags(os.Args)
	yesFlag = yes
	noSandboxFlag = noSandbox
}

func parseStartupFlags(args []string) ([]string, bool, bool) {
	if len(args) <= 1 {
		return args, false, false
	}
	filtered := []string{args[0]}
	yes := false
	noSandbox := false
	i := 1
	for ; i < len(args); i++ {
		switch args[i] {
		case "-y", "--yes":
			yes = true
		case "--no-sandbox":
			noSandbox = true
		default:
			filtered = append(filtered, args[i:]...)
			return filtered, yes, noSandbox
		}
	}
	return filtered, yes, noSandbox
}
```

`parseStartupFlags` only recognizes flags *before* the first non-flag token (the command name itself, e.g. `npx` in `nvx --no-sandbox npx wrangler login`) — anything after that is passed through untouched via `filtered = append(filtered, args[i:]...)`. This is exactly the leading-flag boundary the spec requires, and it's why a `--strict` smuggled into the wrapped command's own args (handled separately by `parseShimOptions` in Task 3 Step 2) can never reach this function.

Replace main.go:13-42 with:

```go
var yesFlag = false

func init() {
	var yes, noSandbox, strict, standard bool
	os.Args, yes, noSandbox, strict, standard = parseStartupFlags(os.Args)
	yesFlag = yes
	noSandboxFlag = noSandbox
	// If both are passed, fail toward more containment, not less.
	strictFlag = strict
	standardFlag = standard && !strict
}

func parseStartupFlags(args []string) ([]string, bool, bool, bool, bool) {
	if len(args) <= 1 {
		return args, false, false, false, false
	}
	filtered := []string{args[0]}
	yes := false
	noSandbox := false
	strict := false
	standard := false
	i := 1
	for ; i < len(args); i++ {
		switch args[i] {
		case "-y", "--yes":
			yes = true
		case "--no-sandbox":
			noSandbox = true
		case "--strict":
			strict = true
		case "--standard":
			standard = true
		default:
			filtered = append(filtered, args[i:]...)
			return filtered, yes, noSandbox, strict, standard
		}
	}
	return filtered, yes, noSandbox, strict, standard
}
```

`parseStartupFlags` is also called directly by two existing tests in `remediation_test.go` (lines 319-347) using the old 3-return-value form — these must be updated to the new 5-value form or the package will fail to compile. Replace both:

```go
func TestParseStartupFlagsDoesNotConsumeShimPayloadFlags(t *testing.T) {
	args, yes, noSandbox, strict, standard := parseStartupFlags([]string{"nvx", "shim", "npx", "-y", "create-vite", "--no-sandbox"})

	if yes {
		t.Fatal("shim payload -y must not enable nvx --yes")
	}
	if noSandbox {
		t.Fatal("shim payload --no-sandbox must not disable nvx sandboxing")
	}
	if strict || standard {
		t.Fatal("shim payload --strict/--standard must not set the leading nvx flags")
	}
	want := []string{"nvx", "shim", "npx", "-y", "create-vite", "--no-sandbox"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args changed unexpectedly: got %v want %v", args, want)
	}
}

func TestParseStartupFlagsOnlyConsumesLeadingGlobalFlags(t *testing.T) {
	args, yes, noSandbox, strict, standard := parseStartupFlags([]string{"nvx", "--yes", "--no-sandbox", "--strict", "shim", "node", "-e", "1"})

	if !yes {
		t.Fatal("leading --yes should enable nvx yes mode")
	}
	if !noSandbox {
		t.Fatal("leading --no-sandbox should enable nvx no-sandbox mode")
	}
	if !strict {
		t.Fatal("leading --strict should enable nvx strict mode")
	}
	if standard {
		t.Fatal("standard should not be set when only --strict was passed")
	}
	want := []string{"nvx", "shim", "node", "-e", "1"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args mismatch: got %v want %v", args, want)
	}
}
```

Add `strictFlag`/`standardFlag` as package-level `bool` vars next to `noSandboxFlag` in shim_options.go:

```go
var noSandboxFlag bool
var strictFlag bool
var standardFlag bool
```

Then wire them into `runShim` (env.go:527) so `shouldSandbox` sees the leading-flag override via `opts`:

```go
func runShim(cmdName string, args []string, nvxHome string) int {
	opts := parseShimOptions(args)
	args = opts.args
	opts.strictFlag = strictFlag
	opts.standardFlag = standardFlag
```

- [ ] **Step 5: Update help text**

In `printHelp` (main.go, `Options:` section, near `--no-sandbox`), add:

```
  --strict               Contain your own code too for this invocation (not just installs/ad-hoc tools)
  --standard             Force standard containment for this invocation, overriding a project's strict policy
```

- [ ] **Step 6: Run full suite**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all green. Pay attention to any existing test that calls `shouldSandbox` directly with the old 3-arg signature — update call sites to the new 4-arg form (`cmdName, args, policy, opts`).

- [ ] **Step 7: Commit**

```bash
git add shim_options.go main.go containment_test.go remediation_test.go
git commit -m "containment: wire classification + isolation level into shouldSandbox, add --strict/--standard"
```

---

### Task 4: `isolation.level` policy field with loosen/tighten trust coverage

**Files:**
- Modify: `policy.go`
- Modify: `policy_test.go`

- [ ] **Step 1: Write the failing test**

Add to `policy_test.go` (read the file first to match its existing style/imports):

```go
func TestIsolationLevelDefaultsToStandard(t *testing.T) {
	p := DefaultPolicy()
	if p.IsolationLevel() != levelStandard {
		t.Errorf("DefaultPolicy().IsolationLevel() = %v, want standard", p.IsolationLevel())
	}
}

func TestIsolationLevelFromJSON(t *testing.T) {
	p := DefaultPolicy()
	local := Policy{Isolation: IsolationPolicy{Level: "strict"}}
	merged := MergePolicies(p, local)
	if merged.IsolationLevel() != levelStrict {
		t.Errorf("merged.IsolationLevel() = %v, want strict", merged.IsolationLevel())
	}
}

func TestPolicyLoosensOnStrictToStandard(t *testing.T) {
	before := DefaultPolicy()
	before.Isolation.Level = "strict"
	after := before
	after.Isolation.Level = "standard"
	if !policyLoosens(before, after) {
		t.Error("dropping isolation.level from strict to standard should count as loosening")
	}
}

func TestPolicyTightensOnStandardToStrict(t *testing.T) {
	before := DefaultPolicy() // level defaults to standard
	after := before
	after.Isolation.Level = "strict"
	if policyLoosens(before, after) {
		t.Error("raising isolation.level from standard to strict should not count as loosening")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run 'TestIsolationLevel|TestPolicyLoosensOnStrict|TestPolicyTightensOnStandard'`
Expected: FAIL — `IsolationPolicy` has no field `Level` / `Policy` has no method `IsolationLevel`.

- [ ] **Step 3: Write minimal implementation**

In `policy.go`, add `Level` to `IsolationPolicy` (policy.go:50-59):

```go
type IsolationPolicy struct {
	Enabled    bool             `json:"enabled"`
	Filesystem FilesystemPolicy `json:"filesystem"`
	Network    NetworkPolicy    `json:"network"`
	// Level selects standard vs strict containment (see isolationLevel in
	// containment.go). Empty/unrecognized values normalize to "standard".
	Level string `json:"level,omitempty"`
	// Legacy top-level provider from older policy files.
	Provider string        `json:"provider,omitempty"`
	Runtime  RuntimePolicy `json:"runtime,omitempty"`

	EnabledSet bool `json:"-"`
}
```

Add an accessor near `Policy.FilesystemProvider()` (policy.go:233-238):

```go
// IsolationLevel returns the effective containment level, normalizing an
// empty or unrecognized isolation.level value to standard.
func (p Policy) IsolationLevel() isolationLevel {
	level, ok := parseIsolationLevel(p.Isolation.Level)
	if !ok {
		LogWarn("Unrecognized isolation.level %q in policy; using standard.", p.Isolation.Level)
	}
	return level
}
```

Extend `MergePolicies` (policy.go:516-632) — add near the other `local.Isolation.*` merges:

```go
	if local.Isolation.Level != "" {
		merged.Isolation.Level = local.Isolation.Level
	}
```

Extend `policyLoosens` (policy.go:481-514) — add a rank-based check analogous to `networkModeRank`:

```go
func isolationLevelRank(level string) int {
	l, _ := parseIsolationLevel(level)
	if l == levelStrict {
		return 2
	}
	return 1
}
```

and inside `policyLoosens`, add:

```go
	if isolationLevelRank(after.Isolation.Level) < isolationLevelRank(before.Isolation.Level) {
		return true
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run 'TestIsolationLevel|TestPolicyLoosensOnStrict|TestPolicyTightensOnStandard' -v`
Expected: PASS

- [ ] **Step 5: Run the full suite**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all green, including pre-existing `policy_test.go` tests (confirm nothing about the new `Level` field broke existing merge/loosen assertions — those tests construct `Policy{}` values that leave `Level` as the zero value `""`, which `isolationLevelRank`/`parseIsolationLevel` treat as standard, so no adjustment should be needed).

- [ ] **Step 6: Commit**

```bash
git add policy.go policy_test.go
git commit -m "policy: add isolation.level (standard/strict) with loosen/tighten trust coverage"
```

---

### Task 5: `nvx policy init` scaffolds `isolation.level` and README/help mention the containment model

**Files:**
- Modify: `policy_init.go`
- Modify: `main.go` (help text, `install`/`shim` command doc if applicable)

- [ ] **Step 1: Read `policy_init.go`** to see the scaffolded JSON template(s) it writes for `nvx policy init --global`/`--project`.

- [ ] **Step 2: Add `"level": "standard"` to the scaffolded `isolation` object** (both global and project templates, wherever `isolation.enabled`/`isolation.network` etc. are already written), with a comment in the surrounding Go string/struct (not in the JSON itself — JSON has no comments) explaining that `strict` also contains your own code. If `policy_init.go` builds the JSON via `json.Marshal` of a `Policy`/`IsolationPolicy` struct literal rather than a raw string template, just set `Level: "standard"` in that literal — do not hand-write JSON strings if the file doesn't already do so.

- [ ] **Step 3: Update `printHelp`'s isolation-related help text** (main.go `policy init` help line, main.go:229/291-ish — read current text first) to mention `isolation.level`.

- [ ] **Step 4: Build and run any existing `policy_init` tests**

Run: `go build ./... && go test ./... -run PolicyInit -v` (adjust the `-run` pattern to match whatever test names actually exist in the codebase — grep `policy_init_test.go` or the existing test file if one exists; if none exists, skip writing one for this docs/scaffolding task, per the plan's YAGNI guidance — this task is deliberately light).

- [ ] **Step 5: Commit**

```bash
git add policy_init.go main.go
git commit -m "policy init: scaffold isolation.level; mention it in help text"
```

---

## Acceptance (from the spec, Parts 2–4)

- `classifyInvocation` correctly classifies every runtime × install/run/tool/file combination in the spec's Part 2 table.
- `shouldContain` matrix (class × level × flags) matches the spec's Part 3 table exactly.
- In a project with default (`standard`) policy: `node app.js` and `npm run build` are NOT contained; `npm install` and `npx <tool>` ARE contained.
- `nvx --strict node app.js` IS contained; `nvx --standard` on a project pinned to `strict` policy forces standard for that one invocation (still subject to existing policy-trust prompts for the *policy file itself* — this plan does not add new prompts for the flag).
- A project `.nvx-policy.json` that lowers `isolation.level` from `strict` to `standard` triggers the existing one-time trust prompt (`ensureProjectPolicyTrust`); raising it to `strict` applies silently.
- Nested invocations remain governed only by the outermost call: `inSandboxSession()`/`NVX_SANDBOX=1` short-circuit in `shouldSandbox` is unchanged (still checked before classification).

Final gate: `go build ./... && go vet ./... && go test ./...` green. Manual check: in a scratch npm project with default policy, `nvx shim npm run build` (or through the real shim once regenerated) shows no sandbox banner, while `nvx shim npx cowsay hi` does.

---

## Self-Review

**Spec coverage (Parts 2–4):**
- Part 2 classification table → Task 1, fully covered by `classify_test.go` including bun's `a` alias, uv's `pip install`, deno's `add npm:`.
- Part 3 levels table + per-command override + policy-trust interaction → Tasks 2–4.
- Part 4 containment profile ("identical profile regardless of class... the change is *when* it applies") → intentionally **no new profile code** in this plan; `runSandbox`/`SandboxConfig` are unchanged, only the decision of *whether* to call them changes. This matches the spec's explicit statement that Part 4 requires no filesystem/network profile changes.
- Nested-invocation short-circuit preserved → Task 3 Step 3 keeps the `inSandboxSession()`/`NVX_SANDBOX` checks ahead of classification in `shouldSandbox`.

**Placeholder scan:** none — every step has complete code or an exact, locatable existing-code reference (line numbers from the current codebase state) rather than "similar to X."

**Type consistency:** `invocationClass` (Task 1) is consumed unchanged by `shouldContain` (Task 2) and `shouldSandbox` (Task 3). `isolationLevel` (Task 2) is consumed unchanged by `Policy.IsolationLevel()` (Task 4). `shimOptions.strictFlag`/`standardFlag` declared in Task 2, populated in Task 3 — no rename drift. `shouldSandbox`'s signature change (adds `args`) is applied consistently at its one call site in env.go.

**Buildability per commit:** Task 2 deliberately adds the (unused-until-Task-3) `strictFlag`/`standardFlag` fields to `shimOptions` in its own commit so `containment.go` compiles standalone — flagged explicitly in Task 2 Step 3 to avoid a broken intermediate commit.
