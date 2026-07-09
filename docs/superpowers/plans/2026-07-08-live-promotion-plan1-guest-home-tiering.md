# Live Promotion Plan 1 — Guest-Home Tiering (Persistent Virtual Profiles) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the trusted-tool real-home *swap* (shipped this session, Linux/macOS-only) with a persistent *virtual* guest profile keyed by (project, tool) — so a granted tool's credentials survive across runs, on all three platforms, without the sandbox ever touching the real home directory.

**Architecture:** A trusted-tool grant currently causes `runNativeSandbox` to point `guestHome` at the real home (`resolveUseRealHome`), which is why Windows is excluded (the AppContainer real-home grant hits the OneDrive/Defender profile-root icacls hang). This plan removes that swap entirely. Instead, a granted tool gets a stable directory under `~/.nvx/tool_home/<hash(project,tool)>` that is *not* wiped after the run. State persists across invocations; the real home is never in the picture on any platform, so the Windows gap closes by construction.

**Tech Stack:** Go 1.23 (stdlib only), `go test`.

**Scope note:** This is **Plan 1 of 3–4** for `docs/superpowers/specs/2026-07-08-trusted-tool-live-promotion-design.md` (Part 1, guest-home tiering). Plan 2 adds live file-watching + real-home *promotion* (Tier 3) on top of this foundation. This plan implements Tier 1 (ephemeral, unchanged) and Tier 2 (persistent virtual). It does **not** add any real-home write, any file watcher, or the env-var `envwrap` — those are later plans.

---

## Why this closes the Windows gap on its own

The user-facing complaint was "`wrangler login` shouldn't make me re-authenticate every run." Part 5 solved that by handing the tool the real home — which works on Linux/macOS but not Windows. A **persistent virtual profile** solves the exact same complaint differently: the login token is written into `~/.nvx/tool_home/<hash>/` on the first run and is still there on the next run, so `wrangler deploy` in that project finds it. That directory is an ordinary nvx-owned folder — no profile-root ACL, no OneDrive filter driver, no platform exclusion. Tier 2 is therefore the better *default* answer to the original problem, and it ships everywhere. Real-home sharing (for when you also run the tool outside nvx) is a strictly narrower need handled by Plan 2's promotion.

This plan removes the code Part 5 added this session: `resolveUseRealHome`, `realHomeSwapSupported`, and the `guestHome = realHome` branch. The macOS Seatbelt-profile-tempfile fix (commit `4ddcabf`) is independent and stays.

---

## File Structure

- **Modify `sandbox.go`** — extract `createProfileSkeleton`; add `getToolHomeDir`, `toolHomeKey`, `ensurePersistentGuestProfile`; remove `realHomeSwapSupported`.
- **Modify `sandbox_native.go`** — replace the real-home-swap branch in `runNativeSandbox` with the persistent/ephemeral decision; remove `resolveUseRealHome`.
- **Modify `sandbox_native_test.go`** — remove `TestResolveUseRealHome`; add a test for the new guest-home decision.
- **Modify `grants_trusted_tools.go`** — `ensureTrustedToolGrant` drops the Windows platform refusal (Tier 2 works everywhere); reword the prompt (it grants a persistent profile now, not real-home access); rename `trustedToolCandidate`'s `wantsRealHome` return to `wantsPersistence`.
- **Modify `env.go`** — update the `trustedToolCandidate` call site's local variable name to match the rename (mechanical).
- **Test `sandbox_test.go`** (new or existing) — `ensurePersistentGuestProfile`/`toolHomeKey` unit tests.

Each task builds and `go test ./...` stays green.

---

### Task 1: Persistent guest-home infrastructure

**Files:**
- Modify: `sandbox.go`
- Test: `sandbox_test.go` (create if it does not exist; check with a glob first)

- [ ] **Step 1: Write the failing test**

Create/append `sandbox_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestToolHomeKeyIsStableAndScoped(t *testing.T) {
	a := toolHomeKey(`/home/u/projA`, "wrangler")
	if a != toolHomeKey(`/home/u/projA`, "wrangler") {
		t.Fatal("toolHomeKey must be stable for the same (scope, tool)")
	}
	if a == toolHomeKey(`/home/u/projB`, "wrangler") {
		t.Fatal("different project scope must yield a different key")
	}
	if a == toolHomeKey(`/home/u/projA`, "gh") {
		t.Fatal("different tool must yield a different key")
	}
	if len(a) == 0 {
		t.Fatal("key must be non-empty")
	}
}

func TestEnsurePersistentGuestProfileCreatesAndReuses(t *testing.T) {
	nvxHome := t.TempDir()
	scope := filepath.Join(t.TempDir(), "project")

	p1, err := ensurePersistentGuestProfile(nvxHome, scope, "wrangler")
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	// Skeleton dirs exist.
	for _, sub := range []string{"tmp", ".config", ".cache"} {
		if _, err := os.Stat(filepath.Join(p1, sub)); err != nil {
			t.Fatalf("expected skeleton dir %s: %v", sub, err)
		}
	}
	// It lives under the persistent tool_home root, NOT the ephemeral sandbox_home.
	if filepath.Dir(filepath.Dir(p1)) != nvxHome {
		t.Fatalf("persistent profile should be two levels under nvxHome, got %s", p1)
	}
	if filepath.Base(filepath.Dir(p1)) != "tool_home" {
		t.Fatalf("persistent profile should live under tool_home, got %s", p1)
	}

	// Second call with the same (scope, tool) returns the SAME path and does not error.
	if err := os.WriteFile(filepath.Join(p1, "marker"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	p2, err := ensurePersistentGuestProfile(nvxHome, scope, "wrangler")
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if p2 != p1 {
		t.Fatalf("reuse: got %s, want %s", p2, p1)
	}
	// Reuse must not wipe existing state.
	if _, err := os.Stat(filepath.Join(p2, "marker")); err != nil {
		t.Fatalf("persistent profile must preserve state across calls: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run 'TestToolHomeKey|TestEnsurePersistentGuestProfile'`
Expected: FAIL — undefined: `toolHomeKey`, `ensurePersistentGuestProfile`

- [ ] **Step 3: Write minimal implementation**

In `sandbox.go`, first read `createGuestProfile` (currently ~lines 114-135). Extract the skeleton-creation into a shared helper and have `createGuestProfile` call it. Replace `createGuestProfile` with:

```go
// createProfileSkeleton creates the minimal directory structure a guest home
// needs (scratch tmp + config/cache dirs) so a low-privilege sandboxed process
// can write to expected locations.
func createProfileSkeleton(guestHome string) error {
	subdirs := []string{"tmp", ".config", ".cache"}
	if runtime.GOOS == "windows" {
		subdirs = append(subdirs, filepath.Join("AppData", "Roaming"), filepath.Join("AppData", "Local"))
	} else {
		subdirs = append(subdirs, filepath.Join(".local", "share"))
	}
	for _, subdir := range subdirs {
		if err := os.MkdirAll(filepath.Join(guestHome, subdir), 0700); err != nil {
			return fmt.Errorf("failed to create guest profile subdirectory %s: %w", subdir, err)
		}
	}
	return nil
}

// createGuestProfile creates an ephemeral guest home directory for the sandbox session.
// Returns the path to the guest home and any error encountered.
func createGuestProfile(nvxHome string, sandboxID string) (string, error) {
	guestHome := filepath.Join(getSandboxHomeDir(nvxHome), sandboxID)
	if err := os.MkdirAll(guestHome, 0700); err != nil {
		return "", fmt.Errorf("failed to create guest profile directory: %w", err)
	}
	if err := createProfileSkeleton(guestHome); err != nil {
		return "", err
	}
	return guestHome, nil
}
```

Then add the persistent-profile functions near `getSandboxHomeDir`:

```go
// getToolHomeDir returns the root directory for persistent per-tool guest
// profiles. It is a sibling of getSandboxHomeDir (the ephemeral root) so that
// cleanupStaleSandboxes, which wipes sandbox_home, never touches persistent
// tool state.
func getToolHomeDir(nvxHome string) string {
	return filepath.Join(nvxHome, "tool_home")
}

// toolHomeKey derives a stable directory name for a (project scope, tool)
// pair, so a tool trusted in one project gets its own persistent profile that
// is not shared with other projects or other tools.
func toolHomeKey(scopeDir, toolName string) string {
	h := sha256.Sum256([]byte(filepath.Clean(scopeDir) + "\x00" + strings.ToLower(toolName)))
	return hex.EncodeToString(h[:])[:16]
}

// ensurePersistentGuestProfile returns a stable guest home for (scopeDir,
// toolName), creating it (with the standard skeleton) on first use and reusing
// it — without wiping existing state — thereafter. Unlike createGuestProfile,
// this directory is never cleaned up after a run, so credentials a trusted
// tool writes survive across invocations. It lives entirely under nvxHome and
// never touches the user's real home.
func ensurePersistentGuestProfile(nvxHome, scopeDir, toolName string) (string, error) {
	guestHome := filepath.Join(getToolHomeDir(nvxHome), toolHomeKey(scopeDir, toolName))
	if err := os.MkdirAll(guestHome, 0700); err != nil {
		return "", fmt.Errorf("failed to create persistent tool profile: %w", err)
	}
	if err := createProfileSkeleton(guestHome); err != nil {
		return "", err
	}
	return guestHome, nil
}
```

Confirm `sandbox.go` already imports `crypto/sha256`, `encoding/hex`, `strings`, `runtime`, `fmt`, `path/filepath`, `os`. It imports `crypto/rand`, `encoding/hex`, `fmt`, `os`, `os/exec`, `path/filepath`, `runtime`, `strings` today — it is MISSING `crypto/sha256`. Add `crypto/sha256` to the import block. (`encoding/hex` and `strings` are already present.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run 'TestToolHomeKey|TestEnsurePersistentGuestProfile' -v`
Expected: PASS

- [ ] **Step 5: Run the full suite**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all green — the new functions are not yet called by any production path, `createGuestProfile`'s refactor is behavior-preserving.

- [ ] **Step 6: Commit**

```bash
git add sandbox.go sandbox_test.go
git commit -m "sandbox: add persistent per-tool guest profiles under ~/.nvx/tool_home"
```

---

### Task 2: Rewire the trusted-tool path to persistent profiles; remove the real-home swap

**Files:**
- Modify: `sandbox_native.go`
- Modify: `sandbox.go` (remove `realHomeSwapSupported`)
- Modify: `grants_trusted_tools.go`
- Modify: `grants_trusted_tools_test.go` (fix a stale comment referencing the removed function)
- Modify: `env.go` (rename call-site variable)
- Modify: `sandbox_native_test.go`

- [ ] **Step 1: Write the failing test**

The core decision — "does this run get a persistent profile?" — is `config.ToolName != ""` (set in `runShim` only after a grant). Extract it as a pure helper so it's testable without launching a sandbox. In `sandbox_native_test.go`, REMOVE the existing `TestResolveUseRealHome` (its function is being deleted) and add:

```go
func TestUsePersistentProfile(t *testing.T) {
	if usePersistentProfile("") {
		t.Fatal("no tool name -> ephemeral profile")
	}
	if !usePersistentProfile("wrangler") {
		t.Fatal("granted tool name -> persistent profile")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestUsePersistentProfile`
Expected: FAIL — undefined: `usePersistentProfile` (and, once you delete `TestResolveUseRealHome`, the old test is gone)

- [ ] **Step 3: Rewrite `runNativeSandbox` and remove the swap**

In `sandbox_native.go`, replace `resolveUseRealHome` (lines ~10-16) with:

```go
// usePersistentProfile reports whether a run should use a persistent per-tool
// guest profile instead of an ephemeral one. ToolName is set (in runShim) only
// for an approved trusted tool, so its presence is the signal.
func usePersistentProfile(toolName string) bool {
	return toolName != ""
}
```

Replace the `guestHome` selection block in `runNativeSandbox` (the `realHome, _ := os.UserHomeDir()` ... `else { ... defer cleanupGuestProfile }` section, lines ~30-44) with:

```go
	var guestHome string
	if usePersistentProfile(config.ToolName) {
		scope := projectScopeDir()
		guestHome, err = ensurePersistentGuestProfile(config.NvxHome, scope, config.ToolName)
		if err != nil {
			LogError("Failed to create persistent tool profile: %v", err)
			return 1
		}
		// Persistent: intentionally NOT cleaned up, so credentials survive to
		// the next run. Still fully contained; the real home is never used.
		LogInfo("%q: using a persistent profile for this project (contained; your real home is untouched).", config.ToolName)
	} else {
		guestHome, err = createGuestProfile(config.NvxHome, sandboxID)
		if err != nil {
			LogError("Failed to create sandbox guest profile: %v", err)
			return 1
		}
		defer cleanupGuestProfile(config.NvxHome, sandboxID)
	}
```

This removes the `realHome`/`useRealHome`/`guestHome = realHome` logic entirely. `os` is still used elsewhere in the function (`os.Getwd`), so its import stays. Verify `os.UserHomeDir` has no other caller in this file after the edit (it should not).

- [ ] **Step 4: Remove `realHomeSwapSupported` from `sandbox.go`**

Delete the `realHomeSwapSupported` function (sandbox.go ~lines 102-112). After this task's `grants_trusted_tools.go` edit (next step), it will have no callers. `runtime` stays imported (used by `createProfileSkeleton`).

- [ ] **Step 5: Update `ensureTrustedToolGrant` and the classifier rename**

In `grants_trusted_tools.go`:

Rename `trustedToolCandidate`'s second return from `wantsRealHome` to `wantsPersistence` and update its doc comment (it no longer implies real-home access):

```go
// trustedToolCandidate inspects an ad-hoc-tool invocation (npx/bunx/uvx/pyx)
// and returns the bare tool name and whether its subcommand looks like it
// needs to persist credentials/config across runs (an auth-shaped subcommand).
// Returns ("", false) for any command that is not an ad-hoc-tool executor.
func trustedToolCandidate(cmd string, args []string) (tool string, wantsPersistence bool) {
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

Rewrite `ensureTrustedToolGrant` — remove the Windows platform refusal (Tier 2 works everywhere now) and reword the prompt (it grants a persistent virtual profile, not real-home access):

```go
// ensureTrustedToolGrant decides whether toolName gets a persistent per-project
// profile for this and future sandboxed runs (so logins/config persist).
// Returns true if it should (already granted, or the user just approved — even
// if that approval couldn't be persisted); false if denied or toolName/nvxHome
// is empty. Prompts at most once per project per tool; the decision persists in
// the project's grant file under nvxHome, never in the project tree. The
// profile is always contained under nvxHome and never touches the real home, so
// this works uniformly on all platforms.
func ensureTrustedToolGrant(nvxHome, toolName string) bool {
	if toolName == "" || nvxHome == "" {
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

	msg := fmt.Sprintf("Let %q keep a persistent profile for this project so its logins/config survive across runs? (Still sandboxed; your real home is untouched.)", toolName)
	if !PromptYesNo(msg) {
		auditLog(nvxHome, "trusted_tool_denied", map[string]string{"tool": toolName, "project": scope})
		return false
	}

	g.TrustedTools = append(g.TrustedTools, toolName)
	g.ProjectPath = scope
	if err := saveProjectGrants(nvxHome, g); err != nil {
		LogWarn("Failed to persist trusted-tool grant: %v", err)
		auditLog(nvxHome, "trusted_tool_grant_persist_failed", map[string]string{"tool": toolName, "project": scope})
		return true
	}
	auditLog(nvxHome, "trusted_tool_granted", map[string]string{"tool": toolName, "project": scope})
	return true
}
```

This deletes the `if !realHomeSwapSupported()` block and its `trusted_tool_platform_unsupported` audit event.

- [ ] **Step 6: Update the `env.go` call site**

In `env.go`'s `runShim`, the sandboxed branch calls `trustedToolCandidate`. Find the local variable that captured the old `wantsRealHome` return and rename it for clarity (it is a local, so this is cosmetic but keeps the code readable):

```go
		toolName := ""
		if tool, wantsPersistence := trustedToolCandidate(cmdName, args); wantsPersistence {
			if ensureTrustedToolGrant(nvxHome, tool) {
				toolName = tool
			}
		}
```

- [ ] **Step 6b: Fix the stale comment in `grants_trusted_tools_test.go`**

That test file (lines ~103-112) has a comment justifying an omitted save-failure test in terms of `realHomeSwapSupported()` returning false on Windows. That function is now gone, so the justification's wording is stale (the save-failure branch is still unreachable in `go test` — but now because `PromptYesNo` denies without a TTY, not because of a platform gate). Replace that comment block with:

```go
// Note: a save-failure test for ensureTrustedToolGrant (approve, then have
// saveProjectGrants fail, and confirm the function still returns true) is
// deliberately omitted. Under `go test` there is no interactive TTY, so
// PromptYesNo denies before saveProjectGrants is ever reached — there's no
// way to drive the persist-failure branch without a test-only prompt
// override, which is more machinery than the assertion is worth. The behavior
// is covered directly by TestEnsureTrustedToolGrantEmptyNvxHome (guard) and
// the already-granted path in TestEnsureTrustedToolGrantReturnsTrueWhenAlreadyGranted.
```

- [ ] **Step 7: Run test to verify it passes + full suite**

Run: `go test ./... -run TestUsePersistentProfile -v && go build ./... && go vet ./... && go test ./...`
Expected: all green. If any test still references `resolveUseRealHome` or `realHomeSwapSupported`, it will fail to compile — grep for both across `*_test.go` and remove/update those references (the plan already removes `TestResolveUseRealHome`; check there are no others).

- [ ] **Step 8: Cross-compile check (the swap removal touches platform files' assumptions)**

Run: `GOOS=darwin go build ./... && GOOS=linux go build ./... && GOOS=windows go build ./...`
Expected: all three compile clean.

- [ ] **Step 9: Commit**

```bash
git add sandbox_native.go sandbox.go grants_trusted_tools.go grants_trusted_tools_test.go env.go sandbox_native_test.go
git commit -m "sandbox: trusted tools use a persistent virtual profile, not the real home"
```

---

### Task 3: Messaging, comments, and cross-platform verification pass

**Files:**
- Modify: `sandbox.go` (comment on `getSandboxHomeDir`/cleanup relationship)
- Modify: any lingering comments referencing the real-home swap

- [ ] **Step 1: Audit for stale references**

Grep the whole repo for stale mentions of the removed mechanism:

Run: `grep -rn "real home\|realHome\|real-home\|resolveUseRealHome\|realHomeSwapSupported" --include=*.go .`
Expected after review: no references in production code except intentional ones (e.g. a comment noting Plan 2 will add real-home *promotion*). Update or remove any stale comment that still describes the swap as current behavior.

- [ ] **Step 2: Add a note that persistent profiles are not cleaned by cleanupStaleSandboxes**

`cleanupStaleSandboxes` (sandbox.go) wipes `getSandboxHomeDir` (`sandbox_home`). Persistent profiles live under `getToolHomeDir` (`tool_home`), a sibling, so they are correctly untouched. Add a one-line comment on `cleanupStaleSandboxes` making this explicit so a future reader does not "helpfully" extend it to wipe `tool_home`:

```go
// cleanupStaleSandboxes removes leftover EPHEMERAL sandbox homes from previous
// runs. It deliberately touches only sandbox_home, never tool_home (persistent
// per-tool profiles), whose whole purpose is to survive across runs.
```

(Adapt to the function's existing comment; keep the rest.)

- [ ] **Step 3: Full verification**

Run: `go build ./... && go vet ./... && go test ./... && gofmt -l sandbox.go sandbox_native.go grants_trusted_tools.go env.go sandbox_native_test.go sandbox_test.go`
Expected: build/vet/test green; `gofmt -l` prints nothing for these files.

- [ ] **Step 4: Manual smoke (any platform, including Windows)**

Build and, in a scratch project, confirm a granted trusted tool now gets a persistent profile path under `~/.nvx/tool_home/` and that a second run reuses it. Because Tier 2 is platform-neutral, this is the FIRST time the trusted-tool flow can be smoke-tested on Windows. Suggested: a throwaway invocation that writes a file into `$HOME`/`%USERPROFILE%` inside the sandbox (e.g. `npx` a tiny tool, or reuse the existing manual test approach), then confirm the file is still present under the `tool_home` hash dir on a second run. Note the observed path in your report. (If a full `npx wrangler login` isn't practical in the scratch environment, a simpler tool that writes a dotfile to `$HOME` demonstrates the persistence just as well.)

- [ ] **Step 5: Commit**

```bash
git add sandbox.go
git commit -m "sandbox: clarify that cleanupStaleSandboxes never wipes persistent tool_home"
```

---

## Acceptance (from the spec, Part 1)

- A granted trusted tool (`ensureTrustedToolGrant` approved) runs with a stable guest home under `~/.nvx/tool_home/<hash(project,tool)>` that survives across runs; a non-trusted invocation still gets an ephemeral profile that is wiped after the run.
- The real home is never used as the sandbox home on any platform. `resolveUseRealHome`, `realHomeSwapSupported`, and the `guestHome = realHome` branch are gone.
- `ensureTrustedToolGrant` no longer refuses on Windows — the trusted-tool flow works uniformly on all three platforms (Tier 2 is a plain nvx-owned directory, no profile-root ACL).
- Persistent profiles are per (project, tool); a tool trusted in project A does not share state with project B.
- `cleanupStaleSandboxes` wipes only `sandbox_home`, never `tool_home`.

Final gate: `go build ./... && go vet ./... && go test ./...` green; all three `GOOS` variants compile; manual persistence smoke on at least one platform (ideally Windows, since it's newly unblocked).

---

## Self-Review

**Spec coverage (Part 1):** Tier 1 (ephemeral, unchanged) and Tier 2 (persistent virtual, per project+tool) are both implemented; the real-home swap is removed as the spec requires. Tier 3 (real-home promotion) and the file watcher are explicitly deferred to Plan 2 — not touched here.

**Placeholder scan:** none — every step has complete code or an exact, greppable edit target. The one "adapt to existing comment" instruction (Task 3 Step 2) is a comment tweak with the full replacement text given.

**Type consistency:** `usePersistentProfile`/`ensurePersistentGuestProfile`/`toolHomeKey`/`getToolHomeDir`/`createProfileSkeleton` are defined once and consumed consistently. `trustedToolCandidate`'s renamed `wantsPersistence` return is updated at its sole call site (`env.go`). `SandboxConfig.ToolName` keeps its meaning (set only for a granted tool) and remains the gate.

**Removal safety:** `realHomeSwapSupported` and `resolveUseRealHome` are removed only after all callers are updated in the same task (Task 2), and Task 2 Step 7/8 explicitly grep for and compile-check stragglers across all `GOOS`. The independent macOS tempfile fix (`4ddcabf`) is untouched.

**Windows unblock:** the whole point — a granted tool on Windows now gets `~/.nvx/tool_home/<hash>` (an ordinary folder, no profile-root icacls, no OneDrive hang), so the platform refusal in `ensureTrustedToolGrant` is correctly deleted rather than left as dead-but-safe code.
