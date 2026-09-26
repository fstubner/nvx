---
title: Policy
description: The global and project policy files, how they merge, and every setting they accept.
---

Corporate policies can be defined globally in `~/.nvx/policy.json` and customized per-project via `.nvx-policy.json`:

```json
{
  "blocked_packages": ["rimraf", "malicious-pkg-*"],
  "enforce_ignore_scripts": false,
  "typosquatting": {
    "enabled": true,
    "max_distance": 2,
    "trusted_packages": ["my-internal-helper"]
  },
  "release_age": {
    "enabled": true,
    "min_age_hours": 24,
    "trusted_packages": ["chrome-devtools-mcp", "@upstash/*"]
  },
  "install_scripts": {
    "trusted_packages": ["esbuild", "sharp"]
  },
  "vulnerabilities": {
    "allowed_advisories": ["GHSA-xxxx-yyyy-zzzz"],
    "min_severity": "high"
  },
  "runtime": {
    "default": "node",
    "versions": { "node": "20" }
  },
  "isolation": {
    "enabled": true,
    "filesystem": {
      "provider": "native"
    },
    "network": {
      "mode": "proxy",
      "default_allow": ["registry.npmjs.org:443", "registry.yarnpkg.com:443", "api.osv.dev:443"],
      "allow_hosts": ["localhost:5432"],
      "prompt_unknown": true
    }
  },
  "environment": {
    "isolated_tools": false
  }
}
```

**Not yet implemented.** `prompts.interactive`, `prompts.non_interactive` and
`prompts.network_unknown` are parsed and merged but nothing reads them, so setting
any of them does nothing — including tightening one. They were previously shown in
this example and scaffolded by `nvx policy init`, which made them look effective.

`isolation.filesystem.mode` was in the same state and sat in the example above,
where `"mode": "strict"` read as a tightening someone had chosen. It was removed
on 2026-09-05 rather than left inert, so a policy naming it now gets an
unknown-key warning instead of silence. Prompt behaviour is fixed:
interactive asks, non-interactive denies, and the two decisions that widen nvx's
trust boundary ignore `-y`/`NVX_YES` entirely (see [Commands](/docs/commands/#policy-files)).

Policies cascade: the global policy applies everywhere, and local policy files merge over it as you get closer to the working directory (the nearest policy wins on conflicting settings; blocklists and trusted packages are unioned).

## Reference
* **`enforce_ignore_scripts`**: When `true`, this forces npm/yarn/pnpm to install packages with `--ignore-scripts`. This blocks execution of hook scripts (`preinstall`/`postinstall`/`install`), which are heavily used in supply chain attacks to download and execute arbitrary binaries on the host machine.
* **Per-check exemptions.** Every install-time check applies to every package
  until a policy names an exception, and each list waives only its own check —
  naming a package in one never affects another. Adding an entry to any of them is
  a loosening, so a project file doing it needs approval.
  - **`typosquatting.trusted_packages`**: this name is not a misspelling of a
    popular one. Names and globs.
  - **`release_age.trusted_packages`**: skip the cooling-off window for this
    package. Use it for a package that publishes often and is started
    non-interactively, such as an MCP server.
  - **`install_scripts.trusted_packages`**: run this package's install scripts
    without asking, and past `enforce_ignore_scripts` — which is how "block
    install scripts except for these" is written. `esbuild`, `sharp` and
    Playwright fetch a platform binary in theirs. The sharpest of these
    exemptions: it is arbitrary code at install time, and every run that uses one
    says which package it let through.
  - **`vulnerabilities.allowed_advisories`**: accept an OSV advisory you have
    assessed, by ID. Per advisory rather than per package, so a finding published
    after your assessment still stops the install.
  - **`vulnerabilities.min_severity`**: `low`, `moderate` (or `medium`), `high` or
    `critical`. Advisories below the floor are reported and do not stop the
    install. Unset by default, which stops on every advisory. An advisory nvx
    could not rate stops the install at every floor — the rating comes from a
    network lookup, so a failed one must never be why a finding slipped under the
    line — and an unrecognised value is no floor at all, reported at load time.
* **`isolation.filesystem.provider`**: Where the process runs (filesystem + process boundary). See the [enforcement matrix](https://github.com/fstubner/nvx/blob/main/docs/enforcement-matrix.md) for exact guarantees.
  - `native` (default): AppContainer (Windows), Landlock + namespaces (Linux), Seatbelt (macOS). Zero-config, fail-closed.
  - `docker`: runs in a container (hardened; `offline`/`loopback` enforced via `--network none`). Requires Docker running. Does not carry `--connect`, and says so when asked: the relay needs a process of nvx's inside the sandbox, and this provider launches the target command as the container's only process.

  Any other name is an error and stops the run. `wsl`, `wslc` and
  `systemd-nspawn` have been removed; see the changelog for why.
* **`isolation.network.mode`**: How egress is governed.
  - `proxy` (default): parent-process HTTP CONNECT + SOCKS5 proxy with policy allowlist; injects `HTTP_PROXY` / `HTTPS_PROXY`.
  - `open`: no egress filtering.
  - `offline`: no network at all.
  - `loopback`: the services on your own 127.0.0.1 are reachable at their own
    addresses, over any TCP protocol; everything else is blocked. On Windows it
    reaches proxy-aware tools' HTTP and HTTPS traffic only, since the reach there
    comes from nvx's proxy. Selecting it in a project policy is a loosening and
    needs approval.
* **`runtime.versions`**: Pin runtime versions used inside the sandbox (e.g. `"node": "20"`).
* **`environment.isolated_tools`**: When `true`, globally installed npm packages (`npm install -g`) are scoped to the project (`<project>/.nvx/npm_global`) instead of being shared through the active Node version. This lets different projects pin different versions of CLI tools (e.g. `vercel`, `eslint`) without conflicts. Takes effect on the next `nvx use` or directory auto-switch. Because that directory goes on your PATH, a project file that turns this on counts as a loosening and needs the same approval as an egress host.

Override filesystem provider per shim: `npm --filesystem-provider=docker install`.
