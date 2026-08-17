# nvx Containment Model v2 — Design

**Date:** 2026-07-07
**Status:** Approved (brainstorm), pending implementation plan

## Context

nvx wraps runtime and package-manager commands (`node`, `npm`, `npx`, `bun`,
`bunx`, `deno`, `python`, `uv`, `uvx`) via PATH shims and can run them inside an
OS-native sandbox. Two problems prompted this redesign:

1. **The model was mis-framed as "sandbox everything you run."** That made the
   value unclear (if a sandbox is ephemeral, why install in it?) and imposed
   friction on running your own code. The real supply-chain threat is
   concentrated: install-time scripts and ad-hoc third-party tools are where
   untrusted code executes; your own code you already trust.

2. **A critical correctness gap:** nvx only intercepts commands if its shim
   directory (`~/.nvx/bin`) is first on `PATH`. In practice the raw runtime dir
   (`~/.nvx/current`) can shadow it, so `npm`/`npx` run raw and nvx silently does
   nothing — a security tool that is invisibly inactive. This must be fixed first.

**Intended outcome:** a coherent, honestly-scoped model — *contain code you
didn't write, persist the output you wanted, grant-once anything legitimate* —
with an opt-in to extend containment to your own code, and a guarantee that nvx
actually intercepts commands.

## Non-goals

- Runtime containment of your own app by default (breaks legitimate FS/network).
- Replacing package managers or resolving dependencies (nvx is not a package
  manager).
- Perfect malware detection — this is defense-in-depth, not a guarantee.

## Part 1 — Shim interception (prerequisite, build first)

Nothing else matters unless nvx runs. Guarantee that `~/.nvx/bin` is
**unconditionally first** on `PATH`, independent of whether `nvx use`/`nvx auto`
has fired in the current shell, and that the raw runtime directory
(`~/.nvx/current` and `versions/*`) never precedes it.

- **Install + `nvx env`:** ensure the shim dir is prepended to `PATH` at shell
  init (not only after `use`/`auto`). The active runtime is resolved *by the
  shims*, not by putting the runtime dir ahead of them.
- **`nvx doctor` (new command):** verifies `npm`/`npx`/`node` resolve to the nvx
  shim (not a raw runtime or a system install), checks shim-dir precedence, and
  repairs `PATH`/profile when it can; otherwise prints exactly what to fix.
- **Self-check on shim run:** when a shim executes, if it detects it was bypassed
  previously (heuristic/telemetry-free), it can hint once. (Optional; keep minimal.)

Acceptance: in a fresh shell with no `nvx use`/`auto`, `Get-Command npx` /
`command -v npx` resolves to `~/.nvx/bin`, and a wrapped command prints the nvx
banner.

## Part 2 — Operation classification (subcommand-aware)

Every wrapped invocation is classified into exactly one class. Classification is
subcommand-aware, not just command-name-aware.

| Class | Examples |
|---|---|
| **your-code** | `node app.js`, `npm run <s>`, `npm test`, `npm start`, `bun run`, `bun <file>`, `python x.py`, project `node_modules/.bin` tools |
| **install** | `npm/yarn/pnpm install·ci·add`, `bun install·add`, `uv add`, `uv pip install`, `deno add npm:<pkg>` |
| **ad-hoc-tool** | `npx <t>`, `bunx <t>`, `uvx <t>` |

Classification lives in one pure, table-tested function
(`classifyInvocation(cmd, args) -> class`). It reuses the existing
install-subcommand detection (`detectShimPackagesForVerification` alias set) so
the "is this an install" logic is defined once.

## Part 3 — Containment levels (per-project policy + per-command flag)

`isolation.level` in `.nvx-policy.json` (committable, cascades like existing
policy) selects how classes map to "contained?":

| Class | `standard` (default) | `strict` (opt-in) |
|---|---|---|
| your-code | not contained | **contained** |
| install | contained | contained |
| ad-hoc-tool | contained | contained |

- Per-command override: `nvx --strict <cmd>` / `nvx --standard <cmd>` (a leading
  nvx flag, like `--no-sandbox`; smuggled payload flags are ignored).
- A cloned untrusted repo can ship its own `.nvx-policy.json` with
  `isolation.level: strict` — but per the existing policy-trust rules, a project
  policy that *loosens* settings (e.g. lowering from strict to standard) requires
  one-time confirmation; *tightening* to strict applies silently.
- `--no-sandbox` remains the explicit per-command escape (direct nvx invocation
  only), unchanged.

The sandbox decision becomes: `contained = shouldContain(classify(cmd,args), level, opts)`.

## Part 4 — Containment profile (applied whenever a call is contained)

Identical profile regardless of class — consistency across the product:

- **Filesystem:** the project directory is **writable** (so `node_modules`,
  build output, and scaffolder output persist); everything else is read-only;
  `HOME`/`USERPROFILE` is redirected to an **ephemeral guest profile** so a
  contained process cannot read `~/.aws`, `~/.ssh`, `~/.npmrc`, etc., or drop a
  persistent backdoor.
- **Environment:** sensitive variables scrubbed (existing `scrubEnvironment`).
- **Network:** allowlisted egress — registry + provider defaults allowed;
  unknown hosts prompt once (deny in non-interactive/CI) and are remembered.

This is the current native-sandbox behavior; the change is *when* it applies
(classification + level), not *how* it isolates.

**Nested invocations:** a command already inside a sandbox (a postinstall that
runs `node`, or `npm run` that calls `npx`) is not re-sandboxed — the existing
`NVX_SANDBOX=1` / `inSandboxSession()` short-circuit is preserved, so the
outermost boundary contains the whole subtree. Classification applies only at
the outermost wrapped call.

## Part 5 — Unified grant model (approve once → `~/.nvx/grants`)

One mechanism for every "needs more than the default" moment, all persisted
per-project under `~/.nvx/grants` (already used for egress), deny-by-default when
non-interactive:

- **Egress host** — "Allow outbound to `github.com`?" (exists today).
- **Trusted tool (real home)** — "`wrangler` wants access to your real home to
  save credentials. Allow?" → grants the *real* `HOME` to that tool on future
  runs. Solves `wrangler login`, `gh auth`, `aws configure` without disabling the
  sandbox. Scoped per tool name, per project.
- **Strict-mode needs** — in `strict`, your own code's unmet needs (a home path,
  a port/host) prompt through the same grant flow.

Grant records extend the existing `projectGrants` struct:
```
{
  "project_path": "...",
  "allow_hosts": ["host:port", ...],
  "trusted_tools": ["wrangler", ...],   // real-home tools
  "policy_pins": { "<policy path>": "<sha256>" }
}
```

## Components & boundaries

- `classifyInvocation(cmd, args)` — pure classifier (new; table-tested).
- `shouldContain(class, level, opts)` — pure policy decision (new; replaces the
  command-name check in `shouldSandbox`).
- `isolation.level` — new policy field (`policy.go`), default `standard`.
- Grant model — extend `projectGrants` + prompts (`policy_persist.go`,
  `egress_proxy.go` prompt reuse); trusted-tool → real `HOME` in the sandbox env
  builder (`scrubEnvironment` / native launch).
- `nvx doctor` + shim-precedence guarantee (new command + `env.go`/installers).

## Testing

- Unit: `classifyInvocation` table (every runtime × install/run/tool/file),
  `shouldContain` matrix (class × level × flags), grant round-trips incl.
  trusted-tools, policy-trust for `level` changes.
- Integration (local, per-OS; Windows verified manually given the CI AppContainer
  skip): `npm install` in a project → `node_modules` persists, secrets absent
  inside, egress limited; `node app.js` in `standard` → not contained; same in
  `strict` → contained + prompt; `npx wrangler login` → trusted-tool grant lets
  the token persist to real home.
- `nvx doctor`: fresh shell resolves `npx` to the shim.

## Rollout / ordering

1. Part 1 (shim interception + `nvx doctor`) — makes nvx actually run. Ship-blocking.
2. Parts 2–4 (classification, levels, profile wiring) — the core model.
3. Part 5 (trusted-tool + strict grants) — the "persist after verified" layer.

Each part builds and tests green independently.
