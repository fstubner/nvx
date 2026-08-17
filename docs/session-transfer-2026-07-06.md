# nvx — Session Transfer Document

**Created:** 2026-07-06  
**Repo:** https://github.com/fstubner/nvx  
**Purpose:** Hand off context from the July 2–6 product/engineering sessions to a fresh chat or collaborator.

---

## How to use this in a new session

Paste or attach this file and say something like:

> Read `docs/session-transfer-2026-07-06.md` and continue from the "Recommended next steps" section. Do not re-litigate product scope unless I ask.

---

## Product north star (agreed)

**One line:** Cross-platform **JS runtime switching** (Node + Bun) with **default OS containment** for toolchain commands.

**Three layers (priority order):**

1. **Switch** — session-scoped PATH, auto-switch on `cd`, `runtime@version` (bare version = Node, nvm-compatible)
2. **Contain** — shims, OS sandbox, secret scrub, egress allowlist, fail-closed policy (the durable differentiator)
3. **Check** — npm-family supply-chain audit (typosquat, OSV, release-age, install-script prompts) — valuable today, not the long-term identity

**Explicitly out of scope (for now):**

- Polyglot runtime management (Go, Python, Deno as shipped providers)
- PyPI / uv / JSR auditing
- Competing with mise/asdf on "manage every language"
- Landing-page / marketing copy rewrite (deferred)

**Preserved elsewhere:** full polyglot implementation on branch `feature/polyglot-runtimes` @ `e7d9043`.

---

## Key product decisions from this session

| Topic | Decision |
|-------|----------|
| Runtime surface | **Node + Bun only** on main release path |
| Go / Python | Toolchain integrity / uv own those stories; nvx value there was sandbox-only — not worth shipping as equal citizens |
| Deno / JSR | JSR is a separate registry (not npm-shaped); nvx can sandbox `deno` but did not build JSR verifier; Deno removed from shipped providers for focus, code kept on preservation branch |
| Audit vs sandbox | Audit pipeline is **npm-registry-shaped** only (`runVerifyInstall`); sandbox is runtime-agnostic |
| `nvx env` | Install plumbing (installer appends to profile); should be de-emphasized in user-facing docs, not removed |
| Agents | Use case / proof point, not product category — shims intercept `npm`/`npx`/`bun` without agent config |
| JSR / "future" | npm problem still dominates today; sandbox story ages better than audit-only story |

---

## Repository state (as of 2026-07-06)

### Branches

| Branch | Commit | Role |
|--------|--------|------|
| `main` | `11772dc` | Production line @ **v0.2.0-beta** (Isolation v1) |
| `audit-remediation` | `8047f6e` | **Active work** — v0.3.0 candidate, Node+Bun focus, CI green |
| `feature/polyglot-runtimes` | `e7d9043` | **Archive** — Deno, Go, Python, uv/pyx before trim |

### Tags

- `v0.1.0`, `v0.2.0-beta` — published
- **`v0.3.0` — not tagged yet** (CHANGELOG and `appVersion = "0.3.0"` ready on `audit-remediation`)

### Pull request

- **PR #2** (OPEN): https://github.com/fstubner/nvx/pull/2  
  Title may still say "five runtimes" — body should be updated to Node+Bun before merge.  
  CI green on latest push (`8047f6e`).

### Working tree

Clean on `audit-remediation`, synced with `origin/audit-remediation`.

---

## What shipped on `audit-remediation` (vs `main`)

### Runtime management

- **Node** — unchanged baseline (nodejs.org, `.nvmrc`, etc.)
- **Bun** — GitHub releases, checksum verify, `.bun-version`, `bun`/`bunx` shims
- **`runtime@version` CLI** — per-runtime version state; Node + Bun can coexist in one shell PATH

### Security / isolation

- Shim-only sandbox (no `nvx sandbox` subcommand)
- FilesystemProvider registry (`native`, `docker`; experimental WSL/nspawn)
- Policy hardening: user-scoped egress grants (`~/.nvx/grants`), project policy trust, audit log
- Release-age policy (`release_age` in policy JSON, trusted-package exemption)
- Shim binary cache (~3 ms Linux, ~4 ms macOS, ~38 ms Windows dispatch overhead)
- Windows AppContainer fixes (local; GHA smoke still skipped)

### Docs / release

- `SECURITY.md`, `CONTRIBUTING.md`, `docs/enforcement-matrix.md`, `docs/runtime-providers.md`
- Tag-triggered `release.yml` workflow (draft releases + attestation)
- README comparison table vs nvm/fnm/volta/asdf/uv

### Removed in trim commit `8047f6e`

- `provider_deno.go`, `provider_go.go`, `provider_python.go`
- uv/uvx/pyx wrapping
- Deno npm-bridge verification in shims

---

## Architecture cheat sheet

### Shim flow

    shell → ~/.nvx/bin/<cmd> shim → nvx runShim
      → (optional) runVerifyInstall for npm/yarn/pnpm/npx/bun/bunx
      → runSandbox (native/docker) OR direct exec if --no-sandbox

### Audit (`runVerifyInstall`) — npm only

- Registry metadata from registry.npmjs.org
- Typosquat via Levenshtein + npm weekly downloads
- OSV with `ecosystem: "npm"`
- Install script detection, release-age window

Triggered from `env.go` for: `npm`, `yarn`, `pnpm`, `npx`, `bun`, `bunx`.

### Key files

| Area | Files |
|------|-------|
| Providers | `version.go` (Providers map), `provider_bun.go`, Node in `version.go` / `runtime_exec.go` |
| Shims | `env.go`, `runtime_exec.go`, `shim_options.go` |
| Sandbox | `sandbox.go`, `sandbox_native*.go`, `fs_provider.go`, `egress_proxy.go` |
| Policy | `policy.go`, `policy_persist.go`, `audit.go` |
| Security checks | `security.go`, `main.go` (`runVerifyInstall`) |
| CI smoke | `scripts/sandbox-smoke*`, `.github/workflows/ci.yml` |

### User preferences (honor in future sessions)

- Cross-platform product, not Windows-only framing
- Net-positive install; security invisible until it matters
- **Commit only when asked**; user may want logical commit stages
- No unsolicited report/summary files unless requested
- No landing-page copy drafts unless asked (product shape still under review)

---

## Known limitations (documented, not bugs)

- Windows AppContainer smoke **skipped on GitHub Actions** runners
- macOS Seatbelt allows broad **reads** (dyld/shared cache); writes + egress still strict
- Linux strongest network story (netns + seccomp); Windows/macOS egress partly cooperative
- Docker provider: `offline`/`loopback` enforced; **proxy allowlist not enforced** under Docker
- Verify-install: direct packages + lockfile best-effort, not full transitive tree
- **`nvx upgrade` not built** — reinstall via install scripts or GitHub Releases

---

## Recommended next steps (priority order)

1. **Update PR #2** description for Node+Bun scope (title/body may be stale).
2. **Merge `audit-remediation` → `main`.**
3. **Tag `v0.3.0`** — verify release workflow produces expected assets.
4. **Smoke install** on Windows + one Unix: `nvx install lts`, `nvx install bun@1.2`, confirm shims/sandbox.
5. **Optional doc pass:** de-emphasize `nvx env`; align `SECURITY.md` opening with switch+contain thesis.
6. **v0.3.1 candidate:** `nvx upgrade` + embedded version polling (discussed, not built).

### Explicitly defer

- Merge anything from `feature/polyglot-runtimes` unless scope expands again
- JSR/Deno verifier
- Marketing/README positioning rewrite
- Transitive dependency verify (unless prioritized)

---

## Conversation arc (for context)

1. **CI / v0.2.0-beta** — Linux seccomp, Windows AppContainer marathon, GHA skip compromise, tag published.
2. **Product review** — 10k ft review; user pushed back on Windows-only and weak-nvm framing; agreed cross-platform nvm-class + invisible security.
3. **Architecture Q&A** — updates, secrets, `nvx env`, extensibility; release-age policy implemented.
4. **Runtime expansion** — five runtimes on `audit-remediation`; product shape tension identified.
5. **JSR / audit depth** — explained npm-only audit; JSR not "unsecurable" but needs separate verifier.
6. **Focus decision** — JS ecosystem; Node+Bun; polyglot on preservation branch; trim @ `8047f6e`.
7. **What's next** — ship v0.3.0, then upgrade command.

---

## Related artifacts in repo

| File | Note |
|------|------|
| `docs/session-history-2026-07-04.md` | Earlier session report (pre-trim, some details stale) |
| `docs/session-transfer-2026-07-06.md` | **This file** — current handoff |
| `CHANGELOG.md` | v0.3.0 notes reflect Node+Bun focus |
| `docs/runtime-providers.md` | Node + Bun only; points to preservation branch |

---

## Quick commands for next session

    git checkout audit-remediation
    git pull
    go test ./...
    gh pr view 2
    gh pr checks 2

After merge:

    git checkout main && git pull
    git tag v0.3.0
    git push origin v0.3.0

---

*End of transfer document.*
