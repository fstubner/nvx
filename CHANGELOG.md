# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Testing
- **Sandbox escape-assertion integration tests** (`sandbox_escape_test.go`, `-tags integration`) + a per-OS CI workflow (`sandbox-escape.yml`): a process run inside the sandbox must fail to read a host secret, write outside its workdir, or open a raw TCP connection, while the workdir write must succeed. `NVX_ESCAPE_STRICT=1` turns "primitive unavailable" skips into failures.

### Security (containment hardening)
- **Loopback egress is no longer auto-allowed.** A sandboxed process can no longer freely reach host-local services (DBs, Docker socket proxies, cloud metadata) — the pivot/exfil channel. Only the proxy's own ports are permitted; other host-local destinations need an explicit allowlist entry (e.g. a local registry). In `offline`/`loopback` modes, loopback is forced through the proxy and blocked.
- **macOS Seatbelt now denies reads of the user's home by default** (with carve-outs for the guest home, workdir, and `~/.npmrc`), plus the system keychain and host SSH keys — replacing the prior credential-dir blocklist. Reads of `~/.ssh`, cloud creds, tokens, and other repos are denied rather than allowed. (Validate on macOS before relying on it.)

### Security (round-2 multi-persona audit remediation)
- **Signature downgrade closed**: a package with no signature is now treated as tampering when the registry publishes signing keys (was: silent warning). Signing-key cache no longer fails open on a transient error; keys are P-256-pinned and expiry-checked.
- **Supply-chain gate now covers `npx`, `bun`, `bunx`** (was: npm/yarn/pnpm only) — `npx <pkg>` / `bun add` / `bunx <pkg>` / `bun x` are verified before running/installing.
- **Proxy seccomp filter fixed**: it was inverted (allowed UDP, denied TCP); it now denies IPv4/IPv6 UDP and allows TCP, with socket-type flag masking. Covered by a BPF-interpreter regression test.
- **Isolation downgrade closed**: an untrusted project `.nvx-policy.json` can no longer weaken the isolation provider, `network.mode`, `filesystem.mode`, or `enabled` below the trusted (global/default) baseline — only tighten them.
- **Egress-proxy data race fixed** (session/prompted maps now mutex-guarded).
- **Lockfile scan bypasses fixed**: pnpm v5 slash-format and yarn `npm:` aliases are now parsed; `findNearestLockfile` is package-manager-aware; a recognized lockfile that parses to zero packages now warns instead of silently skipping.
- **OSV batches are chunked** (≤1000) so large trees aren't dropped; yarn-berry `enforce_ignore_scripts` uses `YARN_ENABLE_SCRIPTS`.

### Added
- **Multi-runtime support** via an open provider registry (`RegisterRuntimeProvider`). Node.js and **Bun** ship in-box; select with `nvx install bun@1.1`.
- **Pluggable isolation backends** via `RegisterIsolationProvider` — the sandbox dispatcher is now an open registry instead of a closed switch. Configure with `isolation.provider` (legacy `isolation.filesystem.provider` still honored) or `--isolation-provider`.
- **Real semver engine**: correct comparison (incl. prerelease precedence) and range resolution (`^`, `~`, `>=`, `<`, x-ranges, `||`) for `install`/`use`/`auto` and `package.json` engines.
- **npm package signature verification**: ECDSA verification of the registry signature over `name@version:integrity` using the registry's published keys — real provenance where there was previously none. An invalid signature blocks the install.
- **Supply-chain tree scanning** now covers `package-lock.json`, `yarn.lock`, and `pnpm-lock.yaml` on bare `npm install` / `npm ci`; plus `fail_closed` policy option and real `--ignore-scripts` enforcement.
- `nvx upgrade [--check]` self-update (checksum-verified, fail-closed) with a cached once-a-day update notification.
- `nvx doctor` (lists runtime + isolation providers, availability, and effective policy), `nvx which <cmd>`, and `nvx current`.
- `docs/EXTENDING.md`: guide + worked examples for adding custom runtime and isolation providers.
- Build-time version injection (`-ldflags -X main.version`) with VCS fallback; `SECURITY.md` with disclosure process and threat model; `uninstall.sh` / `uninstall.ps1`.
- Release automation workflow (`.github/workflows/release.yml`) building the cross-platform matrix with checksums and cosign signing.

### Fixed
- Linux Landlock now actually engages: correct `create_ruleset`/`add_rule` syscall arguments, ABI-version negotiation with access-bit masking, and corrected arm64 syscall numbers.
- seccomp network filter now validates `seccomp_data.arch` (blocks i386/x32 ABI bypass).
- macOS Seatbelt denies reads of credential stores (`~/.ssh`, `~/.aws`, …).
- `nvx env` output is shell-escaped (prevents PATH-based injection into the eval'd script).
- Honest logging/README: Windows no longer claims "Low Integrity" it does not apply; verification matrix documents CI-validation limits.

## [0.2.0-beta] - 2026-07-02

### Added
* **Isolation v1 policy schema**: `isolation.filesystem.provider` and `isolation.network.mode` replace the flat `isolation.provider`; top-level `runtime` and `prompts` blocks.
* **Shim-only sandbox path**: `npm`, `node`, `npx`, `yarn`, `pnpm`, and `bunx` run sandboxed by default when `isolation.enabled` is true; use `--no-sandbox` to bypass per invocation.
* **Embedded egress proxy**: `network.mode: proxy` starts an in-process HTTP CONNECT + SOCKS5 proxy on loopback with policy allowlist and interactive approval for unknown hosts (persisted to `.nvx-policy.json` on approve).
* **RuntimeProvider execution hooks**: binary resolution and default network allowlists go through `RuntimeProvider` so sandbox code is not Node-specific.
* **Cross-platform smoke tests**: filesystem, egress block, and macOS runtime smokes in CI.
* **`nvx policy init`**: scaffold global and project policy files.
* **Project bin shims**: sandbox `node_modules/.bin` tools via `.nvx/project-bin/`.

### Changed
* **Default isolation**: `isolation.enabled` defaults to `true`; `network.mode` defaults to `proxy`.
* **Removed legacy CLI**: `nvx sandbox`, `nvx s`, `nvx exec`, and the `nvxs` shim target are removed; shims are the sole sandbox entry point.
* **Fail-closed Windows native path**: AppContainer setup failure no longer falls back to Low IL alone.
* **Linux network isolation**: loopback-only network namespace with in-child egress proxy; seccomp blocks UDP and offline TCP.

### Removed
* **`--provider` flag**: use `--filesystem-provider=` on shim invocations instead.

## [0.1.0] - 2026-06-30

### Added
* **Multi-Platform Swapping**: Zero-dependency swapping of Node.js versions in under a millisecond by modifying only session-level shell environment variables (`PATH`, `NPM_CONFIG_PREFIX`), supporting PowerShell, Zsh, and Bash.
* **Auto-Configuration Swapping**: Instantly switches Node.js version when navigating into directories containing configuration files (`.nvmrc`, `.node-version`, `package.json`, or Volta configurations).
* **Dynamic PATH Shim Architecture**: Uses dynamic shims in `~/.nvx/bin` to intercept execution reliably in subshells, IDEs, and scripts, resolving early shell alias vulnerabilities.
* **Registry Checksum Integrity**: Enforces cryptographic integrity for Node.js downloads using SHA-256 hashes from nodejs.org, mitigating MITM or server compromise attacks.
* **Interactive Security Interceptor**: Intercepts `npm`, `yarn`, and `pnpm` install commands to perform:
  * Vulnerability scans against the OSV database.
  * Typosquatting audits based on Levenshtein distance and registry download comparison.
  * Release-age warning for packages published less than 24 hours ago.
  * Install script blocking/warning to prevent arbitrary code execution during dependencies installation.
* **Flexible Process Sandboxing**: Runs executions inside isolated environments across platforms with selectable filesystem providers: OS-native isolation (`native`), Docker containers (`docker`), Microsoft WSL Containers via `wslc.exe` (`wslc` — Hyper-V utility VM, separate from WSL distros), default-WSL-distro fallback (`wsl`), macOS Seatbelt sandboxing (`sandbox-exec`), and Linux volatile containers (`systemd-nspawn`, requires root). Providers are selected via `isolation.filesystem.provider` or the `--filesystem-provider` shim flag, and unknown providers fail closed.
* **Project-Scoped Tool Isolation**: Optional `environment.isolated_tools` policy setting scopes globally installed npm packages to `<project>/.nvx/npm_global`, so different projects can pin different versions of global CLI tools.
* **Fail-Closed Prompts**: Security prompts deny by default in non-interactive environments (including CI); approval requires an explicit `-y` / `--yes` or `NVX_YES=true`.
* **CI Integration**: Added remote GitHub Actions CI pipeline testing across Windows, macOS, and Linux matrix with `gosec` and `govulncheck` static analysis scanners.
