# Trusted-Tool Live Promotion — Design

**Date:** 2026-07-08
**Status:** Approved (brainstorm), pending implementation plan(s)

## Context

Containment v2 Part 5 (shipped this session) let a sandboxed ad-hoc tool
(`npx wrangler login`) receive the real user home directory after a one-time
name-based approval, so its credentials persist across runs. That mechanism —
swap `HOME`/`USERPROFILE` to the real path and grant the OS sandbox write
access to it — works on Linux (Landlock) and macOS (Seatbelt), but is
deliberately **not implemented on Windows**: granting an AppContainer write
access to the real profile root reproduces a known hang (`icacls` writes to
`C:\Users\<user>` stall indefinitely behind the OneDrive/Defender filter
driver — the same reason `nvx setup` already excludes the profile root).

This session's follow-up investigation (see "Findings" below) produced a
better mechanism that removes the Windows gap **by removing the need for an
OS-level real-home grant at all**, on any platform. This spec replaces the
Part 5 real-home-swap mechanism (`SandboxConfig.ToolName` /
`resolveUseRealHome` / the `guestHome = realHome` branch in
`runNativeSandbox`) with **live promotion**: the sandboxed process never gets
real-filesystem access; nvx (running unsandboxed, as the parent) watches for
what it produced and, on approval, copies exactly that into the real
location itself.

**Intended outcome:** `npx wrangler login` (and `gh auth`, `aws configure`,
and anything else auth-shaped) works uniformly on Windows, Linux, and macOS,
with a *stronger* security property than the mechanism it replaces — nvx
reviews concrete artifacts before persisting them, rather than pre-trusting a
tool name with broad access.

## Findings from live investigation (this session, on Windows)

These are empirical, not assumed — each was tested live and reverted:

1. **The known hang is specific to the profile root, not to subdirectories.**
   An ACL grant on a subdirectory one level under the real profile
   (`~/.nvx-icacls-test`) completed near-instantly. A grant on the profile
   root itself (`C:\Users\Felix`, this-folder-only, non-recursive) reproduced
   the hang, timing out at 20s with no partial state left behind (confirmed:
   the pre-existing `ALL APPLICATION PACKAGES` ACE stayed at `(RX)`, no `(M)`
   landed).
2. **An AppContainer process cannot write to `HKCU\Environment` by default.**
   `reg.exe add HKCU\Environment ...` launched under the AppContainer token
   returned a real `ACCESS DENIED` from the registry write itself (not a
   launch failure) — the registry behaves like an unauthorized filesystem
   write, consistent with the capability model.
3. **A scoped ACL grant on `HKCU\Environment` itself (not the whole hive, not
   the profile root) completed in under a second** — same fast pattern as the
   filesystem subdirectory case, not the profile-root hang pattern.
4. **`$HOME`/`%USERPROFILE%` redirection is a real, existing "communication
   channel," not a workaround.** Well-behaved tools resolve their config
   location by reading `$HOME`/calling `os.UserHomeDir()` — that's *why*
   redirecting it to an ephemeral directory contains them today without their
   cooperation. The live-promotion design exploits the same indirection point
   nvx already controls, rather than adding a new one.
5. **`HKCU` is not redirectable the way `$HOME` is.** `$HOME` is a simple
   env-var convention every reasonable tool reads. `HKEY_CURRENT_USER` is
   resolved by the Win32 registry API from the calling thread's security
   token, not from an env var — there is no equivalent "point a fake HKCU at
   an ephemeral hive" trick available to an ordinary (non-hive-loading)
   third-party process. This means the file case and the registry case are
   *not* symmetric — see Part 3.

## Non-goals

- True mid-syscall interception (pause the exact operation, resume it after
  approval). This would need `seccomp USER_NOTIF` (Linux), EndpointSecurity
  (macOS, requires Apple entitlement/signing), or a minifilter driver
  (Windows, requires WHQL signing) — a much larger, per-platform-certified
  undertaking. Not ruled out forever, just out of scope here.
- Injected/hooked interception (`LD_PRELOAD`, DLL injection, API hooking).
  Trivially bypassable (static binary, raw syscalls, a re-exec'd child that
  drops the injection) — would be cooperative-only, a real regression from
  the kernel-enforced boundaries nvx already has everywhere.
- Live observation of a running process's in-memory environment variable
  mutations. Not available on any of the three platforms without
  process-tracing (ptrace/ETW-class mechanisms) — see Part 4.
- Windows registry-based persistent env var writes succeeding under the
  sandbox. Explicitly deferred — see Part 4's conclusion.

## Part 1 — Tiered guest-home lifecycle

Today every sandboxed run gets a brand-new random ephemeral guest home
(`generateSandboxID` + `createGuestProfile`), always cleaned up after
(`cleanupGuestProfile`). This introduces a second, stable mode:

- **Tier 1 (default, unchanged):** fully ephemeral, wiped every run. Anything
  not opted into Tier 2/3.
- **Tier 2 (new):** *persistent virtual* profile. The guest home is keyed by
  `(project scope, tool name)` — a stable, hash-derived directory — instead of
  a random per-session ID, and is **not** cleaned up after the run. State
  (config files, tokens) survives across invocations of that tool in that
  project, but never touches the real home. This is the default landing tier
  for an approved trusted-tool file (see Part 2) — it solves "don't make me
  re-authenticate every run" with zero real-home risk.
- **Tier 3 (explicit escalation):** promote specific approved files from the
  Tier-2 virtual profile into the real home (Part 2's mechanism) — only
  needed when the tool is also run *outside* nvx and a shared credential is
  wanted.

`createGuestProfile`/`cleanupGuestProfile` need a stable-key mode (skip
cleanup, reuse if present) alongside the existing random-per-session mode.

## Part 2 — Live file promotion (the core mechanism)

1. A trusted-tool candidate (`npx wrangler login`) runs exactly like any
   other sandboxed ad-hoc-tool invocation — ephemeral or Tier-2 guest home,
   full containment, **no OS-level real-home grant, ever, on any platform.**
2. **Concurrently**, while the process runs, nvx watches the guest home for
   new/changed files (excluding the skeleton `tmp`/`.config`/`.cache` dirs)
   using real OS file-change notification — `inotify` (Linux), `FSEvents`
   (macOS), `ReadDirectoryChangesW` (Windows). Hand-rolled per platform, no
   new dependency (`fsnotify` was considered and explicitly declined to keep
   nvx's zero-dependency property; nvx already hand-rolls comparable
   Windows API work for AppContainer support, so this is consistent with
   existing style).
3. **The moment a new file appears**, prompt live — while the tool may still
   be finishing up, not after a separate re-run: *"wrangler created
   `.wrangler/config/default.toml` — persist to your real home?"* with
   **[Y]es / [N]o / [V]iew** (capped-length content preview, so you can
   inspect before deciding, not approve blind).
4. **On approval:** land the file in the Tier-2 virtual profile (already
   true — that's where it was written) and, if this is a Tier-3 escalation,
   also copy it to the matching relative path under the real home —
   immediately, out-of-band, as the unsandboxed parent nvx process. A plain
   file copy; no sandbox grant is ever requested for this.
5. **On the next run** of that tool: if Tier 2, the guest home already has
   the file (nothing to seed, it's stable). If Tier 3, nvx also seeds a
   fresh copy from the real home back into whatever guest home that
   invocation will use, before launch.

## Part 3 — Profile hashing (skip re-prompting when nothing changed)

Reuses the existing pattern from `policy_persist.go` (`hashPolicyFile` +
`PolicyPins`, SHA256) rather than new infrastructure: hash
`(tool name, resolved version/binary, approved file relpath + content hash,
approved hosts)`. Package registry provenance/signature is folded in as an
extra signal when available (e.g. npm's `--provenance` attestations), never
required — most packages don't have one.

- Unchanged hash on the next run → auto-apply, no re-prompt.
- Anything different (new file, version bump, changed content, new host) →
  prompt only for the delta, not the whole profile again.

This is the same decision `ensureTrustedToolGrant`/`hasTrustedTool` already
make today for the name-based grant; it's being made content-addressed and
per-file instead of per-tool-name.

## Part 4 — Env vars: what's covered, what's an explicit, honest gap

- **Unix persistent env vars are already file-based** — `.bashrc`/`.zshrc`/
  `.profile` get sourced by each new shell. A sandboxed tool trying to
  "persist an env var" for future sessions is, mechanically, just writing a
  file — already fully covered by Part 2, no special-casing needed.
- **Windows persistent env vars are registry-based**
  (`HKCU\Environment`, read fresh into each new process's environment block
  at `CreateProcess` time). Finding 5 above means this **cannot** use the
  same "redirect to an ephemeral location, watch, promote" trick that makes
  the file case safe, because there is no ephemeral-HKCU-redirection
  mechanism available to an ordinary process — `HKCU` resolution is
  token-based, not env-var-based.
  - The only way to let such a write succeed under the sandbox would be to
    grant real registry access to `HKCU\Environment` **before** knowing what
    will be written — which breaks the review-before-persist property that
    makes the file mechanism trustworthy. That would be a "trust in advance"
    tradeoff, not a "review after the fact" one.
  - **Decision for this design: registry-based persistence attempts stay
    fail-closed** (`ACCESS DENIED`, exactly like today, unchanged) for v1.
    This is a narrow, explicit, documented gap — not a "we haven't gotten to
    it yet" gap. In practice it's low-impact: the trusted-tool examples this
    whole feature is built around (`wrangler`, `gh`, `aws`) all persist via
    config files, not the Windows registry, precisely because file-based
    config is the portable convention most CLI tools use for exactly this
    reason.
  - A future, explicitly-opt-in escape hatch (a single upfront "allow this
    run to persist environment variables" toggle, granted before launch,
    Tier-3-equivalent trust) is a plausible follow-up if a real tool that
    needs this shows up — not part of this design.
- **Env vars a tool needs *at launch* that get stripped by `scrubEnvironment`**
  is a different, already-identified problem (a static input-side allowlist
  gap, not an output-capture gap). Stays solved by an explicit, user-driven
  per-tool passthrough list — added to the same profile-hash bundle from
  Part 3 — not by any form of observation, live or otherwise.

## Components & boundaries

- `guestHomeLifecycle.go` (or similar) — stable-key vs. random-key guest
  profile creation/cleanup (Part 1).
- Platform-specific watcher files (`watch_linux.go`/`watch_darwin.go`/
  `watch_windows.go`) — hand-rolled inotify/FSEvents/ReadDirectoryChangesW,
  each exposing the same small interface (watch a directory, emit new/changed
  file events) so the promotion logic above them is platform-agnostic.
- `promote.go` — the review/preview prompt, the copy-to-real-home step, and
  the profile-hash compute/compare (Part 2 + Part 3).
- Extends existing `projectGrants` (policy_persist.go) with per-file/profile
  hash state, building on the `TrustedTools` field Part 5 already added.
- `nvx grants` (grants_cmd.go, already shipped) gains visibility into which
  tier each trusted tool is at and what's been promoted — no new subcommand
  needed, an extension of the existing `list` output.
- **Removed:** `resolveUseRealHome` and the `guestHome = realHome` branch in
  `runNativeSandbox` (Part 5, this session) — the direct home-swap consumer
  goes away entirely. `SandboxConfig.ToolName` itself (the field) stays — it
  now flows into the new promotion logic (Part 2's watcher needs the tool
  name to pick the Tier-2 stable-key directory and to key the profile hash),
  it just no longer triggers a `guestHome` substitution in
  `runNativeSandbox`. The `ensureTrustedToolGrant` prompt-once orchestrator
  and `trustedToolCandidate` classifier are still used as-is, just wired to
  the new mechanism instead of a direct home swap.

## Testing

- Unit: guest-home stable-key derivation (Part 1); each platform's watcher
  (create/rename/write events map to the right callback — can be tested
  against a real temp directory on the CI runner for that OS); profile-hash
  compute/compare table (Part 3); the Unix "env persistence is just a file"
  claim needs no special test (it's already covered by the general file-watch
  test).
- Integration (per-OS, manual + CI where the existing AppContainer CI-skip
  limitation doesn't block it): `npx wrangler login` in a scratch project →
  live prompt fires while the process is still running (not after a
  re-invocation) → approve → Tier 2 profile has the file → re-run without
  re-prompting (hash match) → explicit Tier-3 promote → file appears in real
  home.
- Windows-specific: confirm a registry-persistence attempt still fails
  closed with a clear message (not a silent no-op) — this is a regression
  test for Part 4's documented boundary, not new behavior to build.

## Rollout / ordering

This is larger than a single implementation plan (three platform-specific
watchers, a guest-home lifecycle change, promotion/hashing logic, prompt/
preview UX) — realistically 2–3 plans, split roughly along:

1. Guest-home tiering (Part 1) — foundational, no user-visible behavior
   change yet beyond "trusted tools stay in a stable dir now."
2. Live watching + promote + hash-pinning (Parts 2–3) — the core new
   capability, replacing Part 5's real-home-swap code.
3. Grants CLI visibility + the Part 4 fail-closed regression test + docs.

Each part should build and test green independently, same discipline as the
containment-v2 plans already executed this session.
