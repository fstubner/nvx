# Split the sandbox into a standalone tool ("cage") — Design

**Date:** 2026-07-23
**Status:** Approved (brainstorm), pending implementation plan(s)

## Context

A product/market review of nvx (5 independent personas: seed VC, enterprise
AppSec buyer, individual developer, competitive analyst, OSS governance
consultant) converged unprompted on the same structural problem: nvx bundles
two products with different buyers into one binary.

- **Version manager** (competes with nvm/fnm/volta): individual-dev,
  zero-budget, inertia-driven adoption. The switching cost is "change what's
  in my PATH."
- **Supply-chain security layer** (typosquat/OSV checks + OS-level sandbox):
  security-team, budget-line, procurement-driven adoption. The switching
  cost, as currently built, is "also replace your version manager to get
  it" — because the sandbox only fires on invocations that already go
  through nvx's shims.

That bundled switching cost is the single biggest distribution blocker
identified across every persona. The founder's own original motivation
(README: "the usual version manager headaches... on Windows") was really
about the version-manager half; the sandbox is the more differentiated
technical asset but the harder sell as a package deal.

This spec splits the two into separate products so each can be pitched,
adopted, and evolved on its own terms.

## Decision summary

- **Two products, two repos, starting now**: `nvx` (existing repo) stays the
  version manager; a new repo, **`cage`**, becomes the standalone OS-level
  sandbox.
- **Sandboxing becomes explicit, not implicit.** cage is invoked directly
  (`cage -- npm install`); nvx's shims no longer auto-wrap commands in the
  sandbox. (nvx's *lightweight* checks — typosquat/OSV/release-age — stay
  implicit/automatic, since those don't require process containment.)
- **cage is pure containment, no npm-specific knowledge.** It sandboxes
  whatever command it's given; it does not know what a package manager is,
  does not do typosquat/OSV checks, and does not resolve runtime versions.
  This makes it genuinely general-purpose (any CLI command, not just JS
  tooling) — closer to what an AI-agent vendor could embed directly.
- **nvx and cage are fully decoupled.** Neither has code-level awareness of
  the other. A user who wants both installs both.
- **Naming**: binary/brand name `cage`; domain `runcage.dev` (registered
  availability confirmed via RDAP — the DNS-based check used earlier in
  discussion was unreliable and gave a false positive for `cage.dev`, which
  is actually taken).

## Non-goals

- **No shared internal Go module between the repos.** Once the split is
  complete, nvx has no sandbox code left to share — the native sandbox
  engines, filesystem provider, and egress proxy move to cage in their
  entirety. There is no ongoing duplication to manage.
- **No convenience passthrough from nvx to cage** (e.g. `nvx run --caged`).
  Considered and rejected — it would reintroduce a soft coupling and
  version-skew risk between the two repos for marginal UX benefit.
- **No automatic detection of "should this be sandboxed."** `classify.go`
  and `containment.go` (the invocation-class / isolation-level machinery
  that currently drives nvx's auto-sandbox decision) are deleted outright,
  not moved. Their entire purpose was deciding *whether* to sandbox
  automatically — a decision that no longer exists once sandboxing is
  always explicit.
- **cage does not carry its own supply-chain checks** (typosquatting/OSV).
  Considered as an option (so non-nvx users get the full story from one
  tool) and rejected in favor of a smaller, sharper surface area — those
  checks stay behind in nvx.

## Architecture

### What moves to `cage` (new repo)

Everything that is pure OS-level containment mechanism, with zero
package-manager-specific knowledge:

- `sandbox.go`, `sandbox_native*.go`, `sandbox_windows*.go`,
  `sandbox_appcontainer*.go`, `sandbox_seatbelt.go`, `sandbox_linux.go`,
  `sandbox_landlock*.go`, `sandbox_network*.go`, `sandbox_prctl*.go`,
  `sandbox_seccomp*.go`, `sandbox_unix.go`, `sandbox_session.go`,
  `sandbox_wsl*.go`, `sandbox_nspawn.go`, `sandbox_loopback_windows.go`
- `fs_provider.go` (the `native`/`docker`/experimental filesystem-provider
  registry)
- `egress_proxy.go` (HTTP CONNECT + SOCKS5 allowlist proxy, secret scrubbing)
- `windows_setup.go`, `windows_setup_windows.go`, `windows_setup_other.go`
  (elevated one-time AppContainer setup)
- `grants_trusted_tools.go` — generalized: today it hardcodes "npm/yarn/pnpm
  get a persistent guest home"; in cage it becomes an opaque
  `--persist=<label>` flag with no built-in notion of which tools exist.
- `grants_cmd.go` (`nvx grants list` / `nvx grants reset`) — this is the CLI
  surface for the grants system above (approved egress hosts, trusted
  tools, policy pins); it moves and becomes `cage grants list` / `cage
  grants reset`, since nvx retains nothing for it to list once the
  isolation/egress/trusted-tool state lives entirely in cage.
- Corresponding tests: `sandbox_test.go`, `sandbox_windows_test.go`,
  `sandbox_native_test.go`.
- The egress-decision portion of `audit.go`'s logging (cage gets its own
  `~/.cage/audit.log`; nvx keeps its own log for policy-trust events).

### What stays in `nvx`

- Runtime management: `version.go`, `provider_bun.go`, `runtime_exec.go`,
  `runtime_spec.go`, `import_cmd.go`.
- Supply-chain checks: `security.go`, `policy.go` (minus the `isolation`
  section — see below), `env.go`'s `hasInstallVerb` /
  `installPackagesArg` / `detectInstallPackages` /
  `detectShimPackagesForVerification` — these still decide which packages
  need typosquat/OSV verification, a concern independent of sandboxing.
- `nvx import`, `nvx doctor`, `project_bin.go`, shell integration.
- Its own `policy.json` schema, narrowed to `blocked_packages`,
  `enforce_ignore_scripts`, `typosquatting`, `release_age`, `runtime`,
  `environment.isolated_tools` — the `isolation` and sandbox-relevant
  `prompts` keys move to cage's config schema.

### What gets deleted outright (not moved to either repo)

- `classify.go` (`classifyInvocation`, `invocationClass`,
  `executorCommands`) and `containment.go` (`shouldContain`,
  `isolationLevel`) — their sole purpose was the auto-sandbox decision.
- `classify_test.go`, `containment_test.go`.
- `shim_options.go`'s sandbox-related flags (`--no-sandbox`,
  `--filesystem-provider`) and the `runSandbox`/`shouldSandbox` call sites
  in `main.go`/`env.go`'s shim dispatch.

## cage CLI & config

```
cage -- <command> [args...]                  # run anything sandboxed
cage --filesystem-provider=docker -- npm install
cage --persist=my-tool -- some-cli-tool
```

Config: `~/.cage/config.json` (global) + `.cage.json` (project-local,
cascading — nearest wins, same merge model nvx uses today). Schema is the
`isolation` + sandbox-relevant `prompts` subset lifted directly out of
nvx's current `policy.json`:

```json
{
  "filesystem": { "provider": "native", "mode": "strict" },
  "network": {
    "mode": "proxy",
    "default_allow": ["registry.npmjs.org:443"],
    "allow_hosts": [],
    "prompt_unknown": true
  },
  "prompts": { "interactive": "ask", "non_interactive": "deny" }
}
```

Env vars mirror nvx's existing `NVX_*` pattern: `CAGE_YES`,
`CAGE_NONINTERACTIVE`, `CAGE_EXPERIMENTAL`.

## Data flow

`cage -- npm install lodash`:

1. Resolve `npm` via a plain PATH lookup — no runtime-provider awareness,
   no opinion about which version manager (if any) put it there.
2. Build a `SandboxConfig` from cwd + flags/config (filesystem provider,
   network mode, persistence label if given).
3. Scrub secret-shaped environment variables, build/reuse the guest home.
4. Apply OS-native containment (AppContainer / Landlock+netns+seccomp /
   Seatbelt) or the Docker provider.
5. Run the command, stream stdio through unmodified, propagate the exit
   code.

Fail-closed behavior is unchanged from nvx's current implementation:
missing OS primitive refuses to run rather than downgrading silently;
non-interactive session + unknown egress host denies unless explicitly
pre-approved.

## nvx/cage coupling

None, by design (see Non-goals). Each is independently installable,
independently versioned, independently useful. A user who wants both types
"install nvx" and "install cage" separately; nvx's README can *mention*
cage as a companion tool, but nvx ships and functions with zero knowledge
that cage exists.

## Naming & distribution

- Binary: `cage`. Repo: `github.com/fstubner/cage`.
- Domain: `runcage.dev` (~$13/yr; confirmed available via RDAP query
  against `rdap.org`, which proxies the authoritative registry — not the
  DNS-nameserver heuristic used earlier in discussion, which produced a
  false positive for `cage.dev`).
- License: MIT, matching nvx.

## Testing

- Sandbox-specific tests move wholesale to the cage repo unchanged.
- `classify_test.go` / `containment_test.go` are deleted with the code
  they cover.
- nvx's remaining test suite (typosquat/OSV/policy/version/import tests)
  is unaffected by this split.

## Sequencing (high level; detailed steps belong in the implementation plan)

1. Create the `cage` repo; move the files listed above verbatim, adjust
   package name, get it building and passing its existing tests
   standalone (no nvx dependency).
2. Design and wire up cage's own CLI entry point (`cmd/cage/main.go`) and
   its narrower config schema.
3. Remove the moved files from nvx; delete `classify.go`/`containment.go`
   and their tests; strip the sandbox call sites and flags from nvx's shim
   dispatch.
4. Update nvx's README/docs to drop sandbox claims and (optionally)
   mention cage as a companion project.
5. Register `runcage.dev`, stand up a minimal README for cage explaining
   the standalone pitch.
