# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
