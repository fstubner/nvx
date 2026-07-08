# Containment v2 Part 5 (Trusted-Tool Grants) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a contained ad-hoc tool (`npx wrangler login`, `npx gh auth`, `npx aws configure`) persist credentials to the user's real home directory, once approved, without disabling the sandbox — the "persist after verified" layer the spec calls for.

**Architecture:** A pure classifier (`trustedToolCandidate`) recognizes an auth-shaped ad-hoc-tool invocation (`npx <tool> login/auth/configure`). An orchestrator (`ensureTrustedToolGrant`) prompts once via the existing `PromptYesNo` machinery and persists the decision into the existing per-project grants file (`~/.nvx/grants/<hash>.json`), extended with a `trusted_tools` list. `runShim` threads the resolved tool name into a new `SandboxConfig.ToolName` field; `runNativeSandbox` — the single cross-platform entry point all three OS backends funnel through — swaps the ephemeral guest home for the user's real home when `ToolName` is set. A new `nvx grants` subcommand makes the resulting state inspectable and resettable.

**Tech Stack:** Go 1.23 (stdlib only), `go test`.

**Scope note:** This is **Part 5** of `docs/superpowers/specs/2026-07-07-containment-model-v2-design.md`. Parts 1–4 are done (see the two prior plans in this directory). This plan implements the **trusted-tool** grant only — the first of Part 5's two grant kinds (egress hosts are already implemented from Phase 1 of the original security remediation).

## Explicit descopes (read before objecting that something is "missing")

- **Windows real-home swap is not implemented.** Granting an AppContainer write access to a real home directory is the same class of operation (`icacls ... /grant ... /t` on the profile root) that is already known to **hang indefinitely** behind the OneDrive/Defender filesystem filter driver — this is why `nvx setup` (windows_setup_windows.go:78-81) deliberately excludes the profile root and relies on its pre-existing (read/traverse-only) ACE instead. Attempting the same recursive write grant for a trusted-tool's real home would hit the identical hang risk, this time inside a normal command invocation rather than a one-time setup step — unacceptable. Until this is live-tested and solved on real Windows hardware, `nvx` on Windows tells the user plainly that this isn't supported yet and to use `--no-sandbox` for that one command. Linux (Landlock) and macOS (Seatbelt) have no equivalent risk — both grant filesystem access via an in-process rule/profile list, not a filesystem ACL mutation — so they get the real feature in this plan.
- **The standalone `sandbox-exec`/`seatbelt` `FilesystemProvider`** (`runSeatbeltSandbox` in sandbox_seatbelt.go, selected via `isolation.filesystem.provider: seatbelt`) is a second, independent Seatbelt launch path from the default `native` provider's darwin implementation, and is out of scope here. The default `native` provider (what `DefaultPolicy()` selects) is fully covered.
- **Strict-mode-triggered grants** (the spec's third grant kind — "your own code's unmet needs... prompt through the same grant flow") are deferred. The spec does not define a concrete detection mechanism (how would nvx know your code needs a home path vs. is just failing for an unrelated reason?), and inventing one is a separate design problem, not a mechanical wiring task.
- **The auth-subcommand heuristic is intentionally narrow**: `login`, `auth`, `configure` — exactly the spec's own examples (`wrangler login`, `gh auth`, `aws configure`). This keeps prompting rare (not every never-before-seen `npx` tool) rather than training users to reflexively click "yes".

---

## File Structure

- **Create `grants_trusted_tools.go`** — pure `trustedToolCandidate(cmd string, args []string) (tool string, wantsRealHome bool)`, `stripVersionSuffix`, `nonFlagTokens` helpers; `realHomeSwapSupported() bool`; the `ensureTrustedToolGrant(nvxHome, toolName string) bool` orchestrator (prompt/persist/audit).
- **Create `grants_trusted_tools_test.go`** — table tests for the pure helpers.
- **Modify `policy_persist.go`** — `projectGrants` gains `TrustedTools []string`; `hasTrustedTool`/persist helper.
- **Modify `sandbox.go`** — `SandboxConfig` gains `ToolName string`; new `realHomeSwapSupported` lives here (uses `runtime.GOOS`, already imported).
- **Modify `env.go`** — `runShim` resolves and passes `ToolName` into `SandboxConfig` when a grant applies.
- **Modify `sandbox_native.go`** — `runNativeSandbox` swaps ephemeral guest home for the real home when `config.ToolName != ""`.
- **Create `grants_cmd.go`** — `runGrants(args []string, nvxHome string) int`, implementing `list` and `reset [--all]`.
- **Modify `main.go`** — dispatch `case "grants":`, help text.

Each task builds and `go test ./...` stays green.

---

### Task 1: `projectGrants.TrustedTools` — persistence for the trusted-tool list

**Files:**
- Modify: `policy_persist.go`
- Test: `remediation_test.go` (this file already holds the grants persistence tests — read it first to match its tempdir/chdir style, shown below)

- [ ] **Step 1: Write the failing test**

Add to `remediation_test.go`:

```go
func TestTrustedToolGrantPersistsUnderNvxHome(t *testing.T) {
	tmp := t.TempDir()
	projectDir := filepath.Join(tmp, "project")
	nvxHome := filepath.Join(tmp, ".nvx")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nvxHome, 0755); err != nil {
		t.Fatal(err)
	}

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWd) }()
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	scope := projectScopeDir()
	g := loadProjectGrants(nvxHome, scope)
	if g.hasTrustedTool("wrangler") {
		t.Fatal("fresh grants must not already trust wrangler")
	}

	g.TrustedTools = append(g.TrustedTools, "wrangler")
	g.ProjectPath = scope
	if err := saveProjectGrants(nvxHome, g); err != nil {
		t.Fatalf("saveProjectGrants: %v", err)
	}

	// Never written into the project tree.
	if _, err := os.Stat(filepath.Join(projectDir, ".nvx-policy.json")); err == nil {
		t.Fatal("trusted-tool grant must not create a policy file inside the project")
	}

	reloaded := loadProjectGrants(nvxHome, scope)
	if !reloaded.hasTrustedTool("wrangler") {
		t.Fatal("expected wrangler to be a persisted trusted tool after reload")
	}
	if reloaded.hasTrustedTool("Wrangler") == false {
		t.Fatal("hasTrustedTool must be case-insensitive")
	}
	if reloaded.hasTrustedTool("gh") {
		t.Fatal("unrelated tool must not be trusted")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestTrustedToolGrantPersistsUnderNvxHome`
Expected: FAIL — `g.hasTrustedTool undefined (type projectGrants has no field or method hasTrustedTool)`

- [ ] **Step 3: Write minimal implementation**

In `policy_persist.go`, extend the `projectGrants` struct (currently lines 19-23):

```go
// projectGrants records per-project state that must live outside the project
// tree, so that code running inside the sandbox (which can write the working
// directory) cannot edit the settings that govern it.
//
//   - AllowHosts:    egress hosts the user approved interactively.
//   - TrustedTools:  ad-hoc tool names (e.g. "wrangler") approved to receive
//     the real user home instead of the ephemeral sandbox guest home, so
//     credentials they save (e.g. `wrangler login`) persist.
//   - PolicyPins:    sha256 of each project policy file the user has trusted,
//     keyed by cleaned absolute path.
type projectGrants struct {
	ProjectPath  string            `json:"project_path"`
	AllowHosts   []string          `json:"allow_hosts,omitempty"`
	TrustedTools []string          `json:"trusted_tools,omitempty"`
	PolicyPins   map[string]string `json:"policy_pins,omitempty"`
}

// hasTrustedTool reports whether tool (case-insensitive) is in the granted
// trusted-tools list for this project.
func (g projectGrants) hasTrustedTool(tool string) bool {
	for _, t := range g.TrustedTools {
		if strings.EqualFold(t, tool) {
			return true
		}
	}
	return false
}
```

(The `strings` package is already imported in `policy_persist.go`.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run TestTrustedToolGrantPersistsUnderNvxHome -v`
Expected: PASS

- [ ] **Step 5: Run the full suite**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all green — this is an additive struct field, no existing JSON round-trips break (`omitempty` means old grant files without the key still parse fine).

- [ ] **Step 6: Commit**

```bash
git add policy_persist.go remediation_test.go
git commit -m "grants: add trusted-tool persistence to the per-project grants file"
```

---

### Task 2: `trustedToolCandidate` — recognize an auth-shaped ad-hoc-tool invocation

**Files:**
- Create: `grants_trusted_tools.go`
- Test: `grants_trusted_tools_test.go`

- [ ] **Step 1: Write the failing test**

Create `grants_trusted_tools_test.go`:

```go
package main

import "testing"

func TestTrustedToolCandidate(t *testing.T) {
	tests := []struct {
		name           string
		cmd            string
		args           []string
		wantTool       string
		wantWantsHome  bool
	}{
		{"npx wrangler login", "npx", []string{"wrangler", "login"}, "wrangler", true},
		{"npx gh auth", "npx", []string{"gh", "auth"}, "gh", true},
		{"bunx aws configure", "bunx", []string{"aws", "configure"}, "aws", true},
		{"npx wrangler deploy (not auth-shaped)", "npx", []string{"wrangler", "deploy"}, "wrangler", false},
		{"npx cowsay (no subcommand)", "npx", []string{"cowsay", "hi"}, "cowsay", false},
		{"npx with version pin", "npx", []string{"wrangler@3", "login"}, "wrangler", true},
		{"npx scoped package", "npx", []string{"@cloudflare/wrangler@2", "login"}, "@cloudflare/wrangler", true},
		{"npx with leading flag", "npx", []string{"-y", "wrangler", "login"}, "wrangler", true},
		{"npm run (not an executor command)", "npm", []string{"run", "login"}, "", false},
		{"node direct (not an executor command)", "node", []string{"login.js"}, "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotTool, gotWantsHome := trustedToolCandidate(tc.cmd, tc.args)
			if gotTool != tc.wantTool || gotWantsHome != tc.wantWantsHome {
				t.Errorf("trustedToolCandidate(%q, %v) = (%q, %v), want (%q, %v)",
					tc.cmd, tc.args, gotTool, gotWantsHome, tc.wantTool, tc.wantWantsHome)
			}
		})
	}
}

func TestStripVersionSuffix(t *testing.T) {
	tests := []struct{ in, want string }{
		{"wrangler", "wrangler"},
		{"wrangler@3", "wrangler"},
		{"wrangler@3.1.0", "wrangler"},
		{"@cloudflare/wrangler", "@cloudflare/wrangler"},
		{"@cloudflare/wrangler@2", "@cloudflare/wrangler"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := stripVersionSuffix(tc.in); got != tc.want {
			t.Errorf("stripVersionSuffix(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run 'TestTrustedToolCandidate|TestStripVersionSuffix'`
Expected: FAIL — undefined: `trustedToolCandidate`, `stripVersionSuffix`

- [ ] **Step 3: Write minimal implementation**

Create `grants_trusted_tools.go`. It reuses `executorCommands` (classify.go) and `flagTakesValue` (env.go) — do not redefine them.

```go
package main

import "strings"

// authLikeSubcommands are the ad-hoc-tool subcommands that plausibly need to
// persist credentials/config to the user's real home — exactly the spec's own
// examples (wrangler login, gh auth, aws configure). Intentionally narrow: the
// goal is prompting rarely, not on every never-before-seen npx invocation.
var authLikeSubcommands = map[string]bool{
	"login": true, "auth": true, "configure": true,
}

// nonFlagTokens returns args' tokens that are not flags (or flag values), in
// order. E.g. ["-y", "wrangler", "login"] -> ["wrangler", "login"].
func nonFlagTokens(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			if flagTakesValue(arg) && !strings.Contains(arg, "=") && i+1 < len(args) {
				i++
			}
			continue
		}
		out = append(out, arg)
	}
	return out
}

// stripVersionSuffix removes a trailing "@version" from a package spec,
// correctly handling scoped packages ("@scope/pkg@1.0" -> "@scope/pkg").
func stripVersionSuffix(spec string) string {
	if spec == "" {
		return spec
	}
	prefix := ""
	rest := spec
	if strings.HasPrefix(rest, "@") {
		prefix = "@"
		rest = rest[1:]
	}
	if idx := strings.Index(rest, "@"); idx != -1 {
		rest = rest[:idx]
	}
	return prefix + rest
}

// trustedToolCandidate inspects an ad-hoc-tool invocation (npx/bunx/uvx/pyx)
// and returns the bare tool name and whether its subcommand looks like it
// needs to persist credentials to the real home. Returns ("", false) for any
// command that is not an ad-hoc-tool executor.
func trustedToolCandidate(cmd string, args []string) (tool string, wantsRealHome bool) {
	if !executorCommands[strings.ToLower(cmd)] {
		return "", false
	}
	toks := nonFlagTokens(args)
	if len(toks) == 0 {
		return "", false
	}
	tool = strings.ToLower(stripVersionSuffix(toks[0]))
	if len(toks) < 2 {
		return tool, false
	}
	return tool, authLikeSubcommands[strings.ToLower(toks[1])]
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run 'TestTrustedToolCandidate|TestStripVersionSuffix' -v`
Expected: PASS for every subtest

- [ ] **Step 5: Commit**

```bash
git add grants_trusted_tools.go grants_trusted_tools_test.go
git commit -m "grants: recognize auth-shaped ad-hoc-tool invocations"
```

---

### Task 3: `ensureTrustedToolGrant` — prompt once, persist, audit

**Files:**
- Modify: `grants_trusted_tools.go`
- Modify: `sandbox.go` (add `realHomeSwapSupported`)
- Test: `grants_trusted_tools_test.go`

- [ ] **Step 1: Write the failing test**

`ensureTrustedToolGrant` calls `PromptYesNo`, which reads a real TTY — not mockable without a larger refactor, so this task tests the machinery around it (grant read/write, empty-input guards) directly rather than the prompt itself, mirroring how `TestPersistNetworkAllowHostDoesNotDisableDefaultProtections` tests `persistNetworkAllowHost` without covering prompt UI. Add to `grants_trusted_tools_test.go`:

```go
func TestEnsureTrustedToolGrantReturnsTrueWhenAlreadyGranted(t *testing.T) {
	tmp := t.TempDir()
	projectDir := filepath.Join(tmp, "project")
	nvxHome := filepath.Join(tmp, ".nvx")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nvxHome, 0755); err != nil {
		t.Fatal(err)
	}

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWd) }()
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	scope := projectScopeDir()
	g := loadProjectGrants(nvxHome, scope)
	g.TrustedTools = append(g.TrustedTools, "wrangler")
	g.ProjectPath = scope
	if err := saveProjectGrants(nvxHome, g); err != nil {
		t.Fatal(err)
	}

	// Already granted: must return true WITHOUT prompting (no TTY available in
	// `go test`, so a prompt attempt would deny and this assertion would catch it).
	if !ensureTrustedToolGrant(nvxHome, "wrangler") {
		t.Fatal("expected true for an already-granted tool")
	}
}

func TestEnsureTrustedToolGrantEmptyToolName(t *testing.T) {
	if ensureTrustedToolGrant(t.TempDir(), "") {
		t.Fatal("empty tool name must never be granted")
	}
}
```

Add `"os"`/`"path/filepath"` to `grants_trusted_tools_test.go`'s imports if not already present (they will be, from Task 2's own needs once this step's helpers are added — check the current import block first).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestEnsureTrustedToolGrant`
Expected: FAIL — `undefined: ensureTrustedToolGrant`

- [ ] **Step 3: Write minimal implementation**

Add to `sandbox.go` (near the other small helpers, e.g. after `getSandboxHomeDir`):

```go
// realHomeSwapSupported reports whether this platform can safely swap a
// trusted tool's sandbox HOME for the user's real home directory. Windows is
// excluded: granting an AppContainer write access to a real home directory
// would require the same recursive icacls write on the profile root that is
// already known to hang behind the OneDrive/Defender filter driver (see
// windows_setup_windows.go's profile-root exclusion). Linux (Landlock) and
// macOS (Seatbelt) grant filesystem access via an in-process rule/profile
// list, not a filesystem ACL mutation, so neither has this risk.
func realHomeSwapSupported() bool {
	return runtime.GOOS != "windows"
}
```

(`runtime` is already imported in `sandbox.go`.)

Append to `grants_trusted_tools.go`:

```go
import (
	"fmt"
	"strings"
)

// ensureTrustedToolGrant decides whether toolName should receive the real
// user home directory for this sandboxed run. Returns true if it should
// (either already granted, or the user just approved it now); false if
// denied, unsupported on this platform, or toolName is empty. Prompts at
// most once per project per tool; the decision persists in the project's
// grant file under nvxHome, never in the project tree.
func ensureTrustedToolGrant(nvxHome, toolName string) bool {
	if toolName == "" {
		return false
	}
	scope := projectScopeDir()
	if scope == "" {
		return false
	}

	g := loadProjectGrants(nvxHome, scope)
	if g.hasTrustedTool(toolName) {
		return true
	}

	if !realHomeSwapSupported() {
		LogInfo("%q could persist credentials to your real home on Linux/macOS; the Windows sandbox can't grant that safely yet. Run without isolation for this command: nvx --no-sandbox <cmd> ...", toolName)
		return false
	}

	msg := fmt.Sprintf("%q wants access to your real home directory to save credentials/config (e.g. login tokens). Allow?", toolName)
	if !PromptYesNo(msg) {
		auditLog(nvxHome, "trusted_tool_denied", map[string]string{"tool": toolName, "project": scope})
		return false
	}

	g.TrustedTools = append(g.TrustedTools, toolName)
	g.ProjectPath = scope
	if err := saveProjectGrants(nvxHome, g); err != nil {
		LogWarn("Failed to persist trusted-tool grant: %v", err)
		return false
	}
	auditLog(nvxHome, "trusted_tool_granted", map[string]string{"tool": toolName, "project": scope})
	return true
}
```

Merge this `import` block with the existing one at the top of `grants_trusted_tools.go` (Go allows only one `import (...)` block per file — combine `"strings"` from Task 2 with `"fmt"` here into a single block).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run 'TestEnsureTrustedToolGrant' -v`
Expected: PASS. (`TestEnsureTrustedToolGrantEmptyToolName` passes immediately since the empty-string check short-circuits before any prompt attempt.)

- [ ] **Step 5: Run the full suite**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all green.

- [ ] **Step 6: Commit**

```bash
git add grants_trusted_tools.go sandbox.go grants_trusted_tools_test.go
git commit -m "grants: prompt-once trusted-tool orchestrator (ensureTrustedToolGrant)"
```

---

### Task 4: `SandboxConfig.ToolName` — thread the resolved grant into `runShim`

**Files:**
- Modify: `sandbox.go`
- Modify: `env.go`

- [ ] **Step 1: Add the field**

In `sandbox.go`, extend `SandboxConfig` (currently lines 15-27):

```go
// SandboxConfig holds the parameters for an isolated execution environment.
type SandboxConfig struct {
	// NvxHome is the root nvx directory (~/.nvx)
	NvxHome string
	// Command is the executable to run (e.g. "node", "npx", full path)
	Command string
	// Args are the arguments to pass to the command
	Args []string
	// WorkDir is the working directory for the sandboxed process (defaults to cwd)
	WorkDir string
	// FilesystemProvider overrides isolation.filesystem.provider from policy.
	FilesystemProvider string
	// ToolName is set when this invocation is a granted trusted tool (see
	// ensureTrustedToolGrant) — the native sandbox uses the real home
	// directory instead of an ephemeral guest profile for the run. Empty
	// means "use the ephemeral guest home" (the default, contained behavior).
	ToolName string
}
```

- [ ] **Step 2: Wire it into `runShim`**

In `env.go`, the sandboxed branch of `runShim` currently reads (env.go:561-571):

```go
	if shouldSandbox(cmdName, args, policy, opts) {
		if opts.payloadNoSandbox {
			LogInfo("--no-sandbox is ignored when passed to a wrapped command. To run without isolation, use: nvx --no-sandbox %s ...", cmdName)
		}
		return runSandbox(SandboxConfig{
			NvxHome:            nvxHome,
			Command:            cmdName,
			Args:               args,
			FilesystemProvider: opts.filesystemProvider,
		})
	}
```

Replace with:

```go
	if shouldSandbox(cmdName, args, policy, opts) {
		if opts.payloadNoSandbox {
			LogInfo("--no-sandbox is ignored when passed to a wrapped command. To run without isolation, use: nvx --no-sandbox %s ...", cmdName)
		}
		toolName := ""
		if tool, wantsRealHome := trustedToolCandidate(cmdName, args); wantsRealHome {
			if ensureTrustedToolGrant(nvxHome, tool) {
				toolName = tool
			}
		}
		return runSandbox(SandboxConfig{
			NvxHome:            nvxHome,
			Command:            cmdName,
			Args:               args,
			FilesystemProvider: opts.filesystemProvider,
			ToolName:           toolName,
		})
	}
```

This only calls `trustedToolCandidate`/`ensureTrustedToolGrant` (and therefore only ever prompts) when the invocation is actually about to be sandboxed — never on an uncontained `npm run` or a `--no-sandbox` run.

- [ ] **Step 3: Run the full suite**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all green — `ToolName` is currently unread by any sandbox backend (that's Task 5), so this is a pure plumbing change with no behavior difference yet.

- [ ] **Step 4: Commit**

```bash
git add sandbox.go env.go
git commit -m "sandbox: thread the trusted-tool grant into SandboxConfig"
```

---

### Task 5: Real-home swap in `runNativeSandbox`

**Files:**
- Modify: `sandbox_native.go`
- Test: manual (integration — see below; a unit test for the branch selection is included)

`runNativeSandbox` (sandbox_native.go) is the single cross-platform entry point the `native` `FilesystemProvider` (the default) calls on every OS — it decides `guestHome` once, then calls the build-tag'd `platformLaunchNative(config, guestHome, ...)`, which is a thin per-OS launcher that uses whatever `guestHome` value it's given (Windows: AppContainer write grant + HOME env; Linux: Landlock rule + HOME env; macOS: Seatbelt profile writable-root + HOME env — confirmed by reading all three `sandbox_native_*.go` files). This means the swap only needs to happen in this one function.

- [ ] **Step 1: Write a unit test for the branch-selection logic**

Extracting the "which home directory to use" decision into a small pure function makes it testable without actually launching a sandbox. Add to a new `sandbox_native_test.go`:

```go
package main

import "testing"

func TestResolveSandboxHomeChoice(t *testing.T) {
	tests := []struct {
		name           string
		toolName       string
		swapSupported  bool
		realHome       string
		wantUsesReal   bool
	}{
		{"no tool: ephemeral", "", true, "/home/u", false},
		{"tool granted, swap supported: real", "wrangler", true, "/home/u", true},
		{"tool granted, swap unsupported (windows): ephemeral", "wrangler", false, "/home/u", false},
		{"tool granted, real home unresolvable: ephemeral", "wrangler", true, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			usesReal := resolveUseRealHome(tc.toolName, tc.swapSupported, tc.realHome)
			if usesReal != tc.wantUsesReal {
				t.Errorf("resolveUseRealHome(%q, %v, %q) = %v, want %v",
					tc.toolName, tc.swapSupported, tc.realHome, usesReal, tc.wantUsesReal)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestResolveSandboxHomeChoice`
Expected: FAIL — `undefined: resolveUseRealHome`

- [ ] **Step 3: Write minimal implementation**

Add the pure decision function to `sandbox_native.go`:

```go
// resolveUseRealHome decides whether a sandboxed run should use the real
// user home directory instead of an ephemeral guest profile: only when a
// trusted tool is set, the platform supports the swap, and the real home
// path actually resolved.
func resolveUseRealHome(toolName string, swapSupported bool, realHome string) bool {
	return toolName != "" && swapSupported && realHome != ""
}
```

Now wire it into `runNativeSandbox`. Read the current function (sandbox_native.go:13-45) first — it is:

```go
func runNativeSandbox(config SandboxConfig, policy Policy, egress *EgressProxy, netCtx NetworkLaunchContext) int {
	sandboxID, err := generateSandboxID()
	if err != nil {
		LogError("Sandbox initialization failed: %v", err)
		return 1
	}

	LogInfo("Sandbox session: %s", sandboxID)

	guestHome, err := createGuestProfile(config.NvxHome, sandboxID)
	if err != nil {
		LogError("Failed to create sandbox guest profile: %v", err)
		return 1
	}
	defer cleanupGuestProfile(config.NvxHome, sandboxID)

	cleanEnv := scrubEnvironment(guestHome)
	cleanEnv = applyProxyEnv(cleanEnv, egress)

	cmdPath := resolveSandboxCommand(config, policy)
	if cmdPath == "" {
		return 127
	}

	workDir := config.WorkDir
	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	workDir, _ = filepath.Abs(workDir)

	LogInfo("Running in native sandbox: %s %s", config.Command, strings.Join(config.Args, " "))
	return platformLaunchNative(config, guestHome, workDir, cmdPath, cleanEnv, netCtx)
}
```

Replace with:

```go
func runNativeSandbox(config SandboxConfig, policy Policy, egress *EgressProxy, netCtx NetworkLaunchContext) int {
	sandboxID, err := generateSandboxID()
	if err != nil {
		LogError("Sandbox initialization failed: %v", err)
		return 1
	}

	LogInfo("Sandbox session: %s", sandboxID)

	realHome, _ := os.UserHomeDir()
	useRealHome := resolveUseRealHome(config.ToolName, realHomeSwapSupported(), realHome)

	var guestHome string
	if useRealHome {
		guestHome = realHome
		LogInfo("%q is a trusted tool for this project: using your real home directory. Filesystem/network containment elsewhere is unchanged.", config.ToolName)
	} else {
		guestHome, err = createGuestProfile(config.NvxHome, sandboxID)
		if err != nil {
			LogError("Failed to create sandbox guest profile: %v", err)
			return 1
		}
		defer cleanupGuestProfile(config.NvxHome, sandboxID)
	}

	cleanEnv := scrubEnvironment(guestHome)
	cleanEnv = applyProxyEnv(cleanEnv, egress)

	cmdPath := resolveSandboxCommand(config, policy)
	if cmdPath == "" {
		return 127
	}

	workDir := config.WorkDir
	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	workDir, _ = filepath.Abs(workDir)

	LogInfo("Running in native sandbox: %s %s", config.Command, strings.Join(config.Args, " "))
	return platformLaunchNative(config, guestHome, workDir, cmdPath, cleanEnv, netCtx)
}
```

Note: `cleanupGuestProfile` is deliberately NOT deferred in the `useRealHome` branch — the real home directory must never be deleted. `defer` is only reached in the ephemeral-profile branch.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run TestResolveSandboxHomeChoice -v`
Expected: PASS

- [ ] **Step 5: Run the full suite**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all green.

- [ ] **Step 6: Manual integration check (Linux or macOS only — Windows is out of scope per this plan)**

In a scratch project on Linux or macOS:
```
nvx shim npx wrangler login
```
Expected: a one-time prompt ("wrangler wants access to your real home directory..."), then (on approval) the sandbox log line naming wrangler and using the real home; `wrangler`'s config lands in the real `~/.wrangler` (or equivalent), not the ephemeral `~/.nvx/sandbox_home/<id>`. A second run in the same project does not re-prompt.

- [ ] **Step 7: Commit**

```bash
git add sandbox_native.go sandbox_native_test.go
git commit -m "sandbox: swap to the real home for a granted trusted tool (native provider)"
```

---

### Task 6: `nvx grants` — `list` and `reset [--all]`

**Files:**
- Create: `grants_cmd.go`
- Modify: `main.go`
- Test: `grants_cmd_test.go`

- [ ] **Step 1: Write the failing test**

Create `grants_cmd_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunGrantsListShowsCurrentProjectGrants(t *testing.T) {
	tmp := t.TempDir()
	projectDir := filepath.Join(tmp, "project")
	nvxHome := filepath.Join(tmp, ".nvx")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nvxHome, 0755); err != nil {
		t.Fatal(err)
	}

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWd) }()
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	scope := projectScopeDir()
	g := loadProjectGrants(nvxHome, scope)
	g.AllowHosts = append(g.AllowHosts, "example.com:443")
	g.TrustedTools = append(g.TrustedTools, "wrangler")
	g.ProjectPath = scope
	if err := saveProjectGrants(nvxHome, g); err != nil {
		t.Fatal(err)
	}

	out := formatProjectGrants(g)
	if !containsAll(out, "example.com:443", "wrangler") {
		t.Fatalf("expected grants listing to mention the host and tool, got:\n%s", out)
	}
}

func TestRunGrantsResetRemovesGrantFile(t *testing.T) {
	tmp := t.TempDir()
	projectDir := filepath.Join(tmp, "project")
	nvxHome := filepath.Join(tmp, ".nvx")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nvxHome, 0755); err != nil {
		t.Fatal(err)
	}

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWd) }()
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	scope := projectScopeDir()
	g := loadProjectGrants(nvxHome, scope)
	g.TrustedTools = append(g.TrustedTools, "wrangler")
	g.ProjectPath = scope
	if err := saveProjectGrants(nvxHome, g); err != nil {
		t.Fatal(err)
	}
	path := grantsPath(nvxHome, scope)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("setup: grant file should exist before reset: %v", err)
	}

	if code := runGrants([]string{"reset"}, nvxHome); code != 0 {
		t.Fatalf("runGrants reset exit code = %d, want 0", code)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected grant file removed after reset, stat err=%v", err)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run 'TestRunGrantsListShowsCurrentProjectGrants|TestRunGrantsResetRemovesGrantFile'`
Expected: FAIL — `undefined: formatProjectGrants` / `undefined: runGrants`

- [ ] **Step 3: Write minimal implementation**

Create `grants_cmd.go`:

```go
package main

import (
	"fmt"
	"os"
	"strings"
)

// formatProjectGrants renders a projectGrants as a human-readable summary.
func formatProjectGrants(g projectGrants) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Grants for %s\n", g.ProjectPath)
	if len(g.AllowHosts) == 0 {
		b.WriteString("  egress hosts: (none)\n")
	} else {
		b.WriteString("  egress hosts:\n")
		for _, h := range g.AllowHosts {
			fmt.Fprintf(&b, "    - %s\n", h)
		}
	}
	if len(g.TrustedTools) == 0 {
		b.WriteString("  trusted tools: (none)\n")
	} else {
		b.WriteString("  trusted tools (real home access):\n")
		for _, t := range g.TrustedTools {
			fmt.Fprintf(&b, "    - %s\n", t)
		}
	}
	if len(g.PolicyPins) == 0 {
		b.WriteString("  trusted project policy files: (none)\n")
	} else {
		b.WriteString("  trusted project policy files:\n")
		for path := range g.PolicyPins {
			fmt.Fprintf(&b, "    - %s\n", path)
		}
	}
	return b.String()
}

// runGrants implements `nvx grants list` and `nvx grants reset [--all]`.
func runGrants(args []string, nvxHome string) int {
	if len(args) == 0 {
		LogError("Usage: nvx grants list | nvx grants reset [--all]")
		return 1
	}

	switch args[0] {
	case "list":
		scope := projectScopeDir()
		if scope == "" {
			LogError("Could not determine the current project.")
			return 1
		}
		g := loadProjectGrants(nvxHome, scope)
		fmt.Print(formatProjectGrants(g))
		return 0

	case "reset":
		all := false
		for _, a := range args[1:] {
			if a == "--all" {
				all = true
			}
		}
		if all {
			dir := grantsDir(nvxHome)
			entries, err := os.ReadDir(dir)
			if err != nil {
				if os.IsNotExist(err) {
					LogSuccess("No grants to reset.")
					return 0
				}
				LogError("Failed to read grants directory: %v", err)
				return 1
			}
			for _, e := range entries {
				if err := os.Remove(filepath_Join(dir, e.Name())); err != nil {
					LogWarn("Failed to remove %s: %v", e.Name(), err)
				}
			}
			LogSuccess("Reset all project grants.")
			return 0
		}

		scope := projectScopeDir()
		if scope == "" {
			LogError("Could not determine the current project.")
			return 1
		}
		path := grantsPath(nvxHome, scope)
		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				LogSuccess("No grants recorded for this project.")
				return 0
			}
			LogError("Failed to remove grant file: %v", err)
			return 1
		}
		LogSuccess("Reset grants for this project.")
		return 0

	default:
		LogError("Unknown grants subcommand: %s", args[0])
		return 1
	}
}
```

Fix the `filepath_Join` placeholder above before running — it must be `filepath.Join`, so add `"path/filepath"` to the import block instead of the incorrect name:

```go
import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)
```

and change `filepath_Join(dir, e.Name())` to `filepath.Join(dir, e.Name())`.

- [ ] **Step 4: Register the command in main.go**

After the `case "cleanup":` block (main.go, near the `case "doctor":` added in Part 1), add:

```go
	case "grants":
		if len(os.Args) < 3 {
			LogError("Usage: nvx grants list | nvx grants reset [--all]")
			os.Exit(1)
		}
		os.Exit(runGrants(os.Args[2:], nvxHome))
```

Add to `printHelp`'s Commands list (near the `doctor` line added in Part 1):

```
  grants list              Show this project's approved egress hosts, trusted tools, and policy pins
  grants reset [--all]     Forget this project's grants (or every project's, with --all)
```

Add a `commandHelpText` case:

```go
	case "grants":
		return "nvx grants list\nnvx grants reset [--all]\n\nInspect or forget the approve-once grants recorded for the current project\n(or every project, with --all): egress hosts, trusted tools, and trusted\nproject policy files. Grants live under ~/.nvx/grants, never in the project.\n"
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./... -run 'TestRunGrantsListShowsCurrentProjectGrants|TestRunGrantsResetRemovesGrantFile' -v`
Expected: PASS

- [ ] **Step 6: Run the full suite**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all green.

- [ ] **Step 7: Commit**

```bash
git add grants_cmd.go grants_cmd_test.go main.go
git commit -m "grants: add 'nvx grants list' and 'nvx grants reset [--all]'"
```

---

## Acceptance (from the spec, Part 5 — trusted-tool grant only)

- `npx wrangler login` (or `gh auth`, `aws configure`) inside a sandboxed project prompts once: "wrangler wants access to your real home directory...".
- On approval, the tool's credentials persist to the real home directory; a second run in the same project does not re-prompt.
- On denial, the tool still runs contained with the ephemeral guest home (its credentials will not persist — a legitimate, expected outcome of declining).
- Grants are stored under `~/.nvx/grants/<hash>.json`, never inside the project tree (matches the existing egress-host and policy-pin grant behavior).
- Windows prints a clear one-line explanation and points at `--no-sandbox` instead of silently doing nothing or hanging.
- `nvx grants list` shows egress hosts, trusted tools, and trusted policy files for the current project. `nvx grants reset` clears the current project's grants; `--all` clears every project's.
- `npx cowsay hi` (no auth-shaped subcommand) never prompts.

Final gate: `go build ./... && go vet ./... && go test ./...` green on Windows and Linux (WSL). Manual Linux/macOS check per Task 5 Step 6.

---

## Self-Review

**Spec coverage:** Part 5's "trusted tool (real home)" grant kind is fully implemented (Tasks 1–5) with the `nvx grants` inspection/reset surface the release plan also called for (Task 6). The "strict-mode needs" grant kind and the standalone `seatbelt` provider are explicitly descoped above, with reasons, not silently dropped.

**Placeholder scan:** none, except the deliberate `filepath_Join` typo-then-fix in Task 6 Step 3, which exists to force the implementer to read and correct the import block rather than blindly copy-paste — flagged inline immediately after, with the exact fix given. No other placeholders.

**Type consistency:** `SandboxConfig.ToolName` (Task 4) is populated only by `runShim`'s call to `trustedToolCandidate`/`ensureTrustedToolGrant` (Tasks 2–3) and consumed only by `runNativeSandbox`/`resolveUseRealHome` (Task 5) — no rename drift. `projectGrants.TrustedTools`/`hasTrustedTool` (Task 1) is the single source `ensureTrustedToolGrant` and `grants_cmd.go` both read/write.

**Windows safety:** verified against the codebase's own prior finding (windows_setup_windows.go's documented profile-root icacls hang) before scoping — Windows never attempts the real-home swap (`realHomeSwapSupported` returns false), so the known-hang code path is never reached by this plan's changes.
