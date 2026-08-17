# Daemon-Capable Sandbox — Design

**Date:** 2026-07-20
**Status:** Approved (brainstorm), pending implementation plan(s)

## Context

Twice this session, nvx's Windows AppContainer sandbox melted the developer machine it was running on: MCP servers launched via `npx` (by Claude Code and other MCP clients) got routed through nvx's shim once nvx was first on PATH, hung for ~70s in sandbox setup, got killed by the MCP client's connection timeout, and left orphaned process trees that accumulated across repeated respawns. Root-caused live (see "Root causes," below) rather than assumed.

This exposed two separable problems:
1. **Scope**: nvx's global PATH interception (required — see `docs/superpowers/plans/2026-07-07-shim-interception-part1.md`) was containing commands launched from *outside any project* — machine infrastructure, not user work.
2. **Capability**: even inside a project, the sandbox cannot currently host a long-running daemon — it's slow, drops stdio, and leaks its process tree on failure.

This spec fixes both, and connects the capability fix to the persistence mechanism already designed in `2026-07-08-trusted-tool-live-promotion-design.md` — a daemon needing to stay authenticated across restarts is the same problem as a trusted tool needing to stay authenticated across runs.

## Root causes (live-tested, not assumed)

- **Latency (~70s per launch, isolated via instrumented reproduction):** `grantWorkdirAncestors` re-grants every ancestor directory of the workdir and guest home on *every launch*, via a 15–20s-timeout `icacls` call per ancestor. Measured: 4 of 5 ancestor grants hang to their full timeout. Two of those (`.nvx`, `.nvx\sandbox_home`) already carry the AppContainer ACE — the grant is redundant. The other two (`AppData`, `AppData\Local`) aren't needed at all — the sandboxed command ran correctly without them. A later live test on a *different drive* (`H:\...\harness-router`) showed the *direct* workdir grant (not just ancestors) also hit the same hang — the OneDrive/Defender filter-driver interference is broader than "paths near the profile root," not scoped to `C:\Users`.
- **Stdio:** `launchAppContainerProcessOnce` calls `CreateProcess` with `bInheritHandles=FALSE` and no `STARTF_USESTDHANDLES` — a piped stdin never reaches the child. Fatal for any stdio-JSON-RPC daemon (every MCP server).
- **Reaping:** no process-tree lifecycle management. A killed/orphaned setup leaves its children running indefinitely.

## Non-goals

- True mid-syscall interception, restricted-token Windows principals, firewall-based network isolation — all real alternatives (see "Prior art," below), none adopted here. The latency root cause is fixable within the existing AppContainer model; switching primitives would be a much larger change for no benefit to *this* problem.
- Full bubblewrap adoption on Linux — real upside (battle-tested, PID-namespace isolation included), real cost (breaks nvx's zero-dependency property, regresses portability on kernels without unprivileged userns, doesn't replace nvx's own network-allowlist layer, Linux-only). The one concrete gap it would have closed (PID namespace isolation) is hand-rolled instead — see Part 3.
- Detecting "is this a daemon" as a heuristic. Unnecessary — see Part 2.

## Part 1 — Containment scope: project-directory-gated, not global

Interception stays global (`~/.nvx/bin` unconditionally first on PATH — required, or nvx is silently inactive). **Containment** — the decision to apply the sandbox at all — becomes scoped to project directories: any tree containing `package.json` or `.git`. No explicit opt-in file required; this is the existing "secure by default where it matters" posture, just correctly scoped. Outside a project directory (home dir, editor config, wherever an MCP client's cwd happens to be), a wrapped command passes straight through to the real runtime — shim-dispatch overhead only (~38ms), no AppContainer/Landlock/Seatbelt machinery touched.

This directly fixes the incident's *cause*, independent of Parts 2–3: an MCP server launched from an arbitrary cwd never reaches the sandbox at all.

## Part 2 — Why no daemon detection is needed

A command inside a project directory that's classified as `install` or `ad-hoc-tool` (per the existing Parts 2–4 classification) gets contained regardless of whether it happens to be long-running. Once the sandbox is fast (Part 3), stdio-transparent (Part 3), and leak-proof (Part 3) — a daemon is simply "a contained process that runs for a while" and needs no special-casing. Building a `mcp`/`serve`/`dev`-subcommand heuristic to detect and exempt daemons would be additional, fragile surface solving a problem that doesn't exist once the sandbox itself is correct.

## Part 3 — Platform fixes

### Windows

- **Fix A (latency):** stop the per-launch ancestor re-grant entirely. The guest-home grant, workdir grant, and node-runtime-dir grant (all fast, all necessary) remain; `grantWorkdirAncestors`'s redundant/unneeded ancestor walk is removed.
- **Fix B (stdio):** `launchAppContainerProcessOnce` sets `bInheritHandles=TRUE` and populates `STARTF_USESTDHANDLES` in the `STARTUPINFOEX`, so a piped stdin reaches the sandboxed child.
- **Fix C (reaping):** the child is assigned to a Windows Job Object created with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`. If nvx dies for any reason, the OS tears down the entire child tree atomically — no polling, no bespoke tracking.
- **Lifecycle model:** nvx remains the parent for the daemon's entire life — this is not a detach-and-exit design. The MCP client holds nvx's own process handle and stdio pipes; nvx blocks on `WaitForSingleObject` after setup succeeds, which is cheap (near-zero CPU) for as long as the daemon runs. When the client wants the daemon gone, it kills nvx (or closes the pipe) and the Job Object guarantees the child dies too.

### Linux

- Add `CLONE_NEWPID` alongside the existing `CLONE_NEWNET` unshare already used for the network namespace in `__landlock-exec` (nvx's existing Linux supervisor process, which already sits in the correct position — inside the sandbox boundary, direct parent of the real target).
- Add basic zombie-reaping in that same supervisor (a `wait4(-1, ..., WNOHANG, ...)` loop for any exited descendant, not just the tracked child) — required once a daemon can fork/reap subprocesses repeatedly over a long session; PID-1-of-a-namespace semantics mean orphans reparent there and are never cleaned up otherwise.
- **Deferred to v2:** remounting `/proc` inside a private mount namespace for a fully accurate process listing. The security-relevant property (a sandboxed process cannot signal/ptrace anything outside its namespace) is enforced by the kernel based on the *caller's* namespace regardless of what a stale `/proc` displays — this gap is an information-disclosure nicety, not an attack vector.
- **Reaping is free:** when a namespace's PID 1 (nvx's own supervisor) dies, the kernel SIGKILLs everything else in that namespace and reclaims it. No Job-Object-equivalent needed — this is a direct consequence of the primitive.

### macOS

- No kernel-enforced kill-on-parent-death primitive exists on Darwin (no namespaces, no Job Objects). Best available: process-group kill via a signal handler on graceful nvx exit (covers normal exit, `SIGTERM`, `SIGINT`).
- **Explicit, accepted gap:** a hard `SIGKILL` of nvx itself orphans the sandboxed child, with no fix available short of a much larger architecture change (e.g. running inside a VM, as Docker Desktop does on macOS for the same underlying reason). Documented here rather than silently left unhandled.
- Stdio and latency are not affected on macOS — `runSeatbeltSandbox`/`platformLaunchNative` already use Go's `os/exec` with `cmd.Stdin = os.Stdin` etc. (correct stdio inheritance by construction) and Seatbelt's profile-file model has no per-launch ACL-grant step to hang on.

## Part 4 — Daemon persistence reuses live-promotion Tier 2, automatically

A daemon has no TTY to answer a grant prompt, so the trusted-tool grant flow (Part 5 of the earlier containment spec) cannot apply as-is. Resolution: the persistent virtual profile (Tier 2 from `2026-07-08-trusted-tool-live-promotion-design.md`) applies **automatically, without a prompt**, to any contained process — daemon or not. This isn't a special case; it's a consequence of Tier 2 never touching the real home. There is nothing consent-worthy about a profile that only persists inside `~/.nvx/tool_home` — the prompt was only ever needed to gate *real-home* access (Tier 3), which a daemon doesn't need for auth persistence to work.

**Keying: per (project, tool).** An MCP server launched from project A gets a profile distinct from the same server launched from project B — consistent with Part 1's whole premise (containment, and therefore any state a contained process accumulates, is scoped to the project you're working in). Global-per-tool keying would optimize for fewer re-auths at the direct cost of reintroducing cross-project credential leakage — the same class of problem this spec exists to close, relocated from filesystem access to auth tokens. Not adopted.

## Prior art considered, not adopted

- **Restricted tokens** (Codex's Windows "unelevated" mode): inherits the caller's existing ACL entries rather than needing fresh grants everywhere — would sidestep the ancestor-grant problem differently. Not adopted because Fix A already solves the actual latency bug within the current AppContainer model; switching principals is a materially larger change (different security semantics — allow-list vs. deny-list) for no additional benefit to the problem at hand.
- **Firewall-based network isolation** (Codex's "elevated" mode): per-process Windows Firewall rules instead of AppContainer capability SIDs. Not part of this spec — nvx's existing egress-proxy/capability-SID network model is unchanged here; this remains a candidate for a future network-specific pass.
- **Bubblewrap** (agent-run, Flatpak): see Non-goals.

## Components & boundaries

- `sandbox_appcontainer_windows.go` — remove the ancestor-grant loop from `grantWorkdirAncestors`'s call sites (or remove the function; confirm no other caller depends on it).
- `sandbox_appcontainer_launch_windows.go` — `launchAppContainerProcessOnce`: add `bInheritHandles=TRUE`, `STARTF_USESTDHANDLES`, and Job Object creation/assignment with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`.
- `sandbox_landlock_linux.go` — `applyLandlockSandbox`/`runLandlockExecChild`: add `CLONE_NEWPID` to the existing namespace unshare; add a reaping loop.
- `sandbox_seatbelt.go` / `sandbox_native_darwin.go` — add a signal handler for process-group cleanup on graceful exit; document the `SIGKILL` gap in code comments, not just this spec.
- Project-scope gate (Part 1) — a new function, walking up from cwd the same way `findProjectRoot` (project_bin.go) already does, but checking for `package.json` **or** `.git` (not reused as-is: `findProjectRoot` is npm-specific — package.json-only — and scoped to project-bin-shim generation; changing its behavior would silently affect that unrelated feature). Consulted in `shouldSandbox`/`shouldContain` alongside the existing classification logic from Parts 2–4.
- Daemon persistence (Part 4) — reuses `ensurePersistentGuestProfile`/`toolHomeKey` from the live-promotion Plan 1 work (guest-home tiering), keyed the same way, with the grant-prompt step skipped for the automatic case.

## Testing

- Unit: project-directory detection (table test: presence/absence of `package.json`/`.git`, nested cases); the ancestor-grant removal doesn't regress the (still-needed) direct guest-home/workdir/node-dir grants.
- Integration, Windows (manual, given the existing CI AppContainer-skip limitation): a scratch MCP-server-shaped stdio daemon launched through nvx from inside a project — confirm sub-second setup (not ~70s), confirm the daemon receives stdin correctly, confirm killing nvx kills the daemon (Job Object).
- Integration, Linux (CI-testable): a contained long-running process that forks children repeatedly — confirm no zombie accumulation over a multi-minute run, confirm killing the supervisor kills the whole tree.
- Integration, macOS: confirm graceful-exit cleanup works; confirm (and comment) that hard-kill does not.
- Regression: the two live incidents from this session (`npx`-launched MCP servers from outside a project; `npx`-launched MCP servers from inside one) should not reproduce the process pile-up under any circumstance after this ships.

## Rollout / ordering

This is separate from, but must land before or alongside, live-promotion Plan 1 (guest-home tiering) — Part 4 of this spec directly depends on `ensurePersistentGuestProfile`/`toolHomeKey` existing. Suggested order:
1. Live-promotion Plan 1 (guest-home tiering) — already planned, not yet executed.
2. Part 1 of this spec (project-scope gate) — independent, can land first or in parallel.
3. Part 3 per-platform fixes (Windows Fix A/B/C, Linux `CLONE_NEWPID` + reaping, macOS signal handler) — the largest chunk, likely 2 plans (Windows; Linux+macOS).
4. Part 4 (automatic Tier 2 for contained processes) — small, once 1 and 3 are in place.
