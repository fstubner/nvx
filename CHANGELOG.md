# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.5.0] - 2026-08-18

**0.4.0 was tagged but never published.** The tag was cut, then held back
because the README it shipped with made three claims about the sandbox that
were subsequently disproved by execution — including that a malicious package
could not read your `.env`, which it can. Those claims were corrected before
this release. So 0.5.0 is the first published build carrying the 0.4.0 fixes
as well as its own, and the newest downloadable build before it remains
`v0.2.0-beta`.

### Security

* **Windows egress is now actually restricted to the allowlist.** It was not
  before, on any released build. The sandbox held the `internetClient`
  capability, so `HTTP_PROXY` was a request a package could simply decline —
  measured against 0.4.0, a `postinstall` script opened connections to
  `1.1.1.1:443` and `registry.npmjs.org:443` with no restriction at all.

  The AppContainer is now granted no network capability, so Windows itself
  refuses direct connections and DNS does not resolve. The parent's egress proxy
  is exposed on an AF_UNIX socket — a filesystem object, which the AppContainer
  network restriction does not cover — and a new in-container supervisor,
  `nvx __appcontainer-exec`, re-exposes it as loopback TCP for tools that only
  understand `host:port`. That is the same parent-proxy-plus-relay shape Linux
  already used, and it needs no elevation.

  The same `postinstall` script now gets `EACCES` and `ENOTFOUND` for both hosts
  while `npm install` completes normally against the real registry.

* **`nvx setup` no longer registers a loopback exemption, and removes an existing
  one.** The exemption was how a sandbox reached the proxy before the relay; it
  also let the sandbox reach every other loopback listener on the machine. With
  the relay it grants access for no remaining reason. Setup is now only about
  drive-root stat access, and is no longer needed for allowlisted egress.

### Fixed

* **Every sandboxed command failed on Windows without an nvx-managed runtime.**
  A runtime nvx does not manage is copied somewhere the sandbox can reach, and
  that copy walked the source with `filepath.Walk` — which inspects each path
  with `Lstat`, so a directory *link* arrived looking like a file and the copy
  tried to open its own destination folder for writing. nvm for Windows makes
  `C:\Program Files\nodejs` exactly such a link, and it is how most Windows
  developers install node, so the whole sandbox died with
  `open <nvxHome>\sandbox-exec\<hash>: is a directory` — a message naming
  neither node nor the link.

  Staging now recurses with `os.ReadDir`/`os.Stat`, which follow both kinds of
  Windows directory link. Resolving the path up front would have fixed only
  half: a symbolic link sets `ModeSymlink` and `filepath.EvalSymlinks` resolves
  it, but a junction reports `ModeIrregular` and `EvalSymlinks` returns it
  unchanged with no error. Links below the root are followed too; previously
  they would have been copied as empty folders, leaving a runtime missing files
  and failing later somewhere unrelated.

* **The same path then launched node against a directory it could not read.**
  The staged copy was used for the interpreter while `npm-cli.js` still pointed
  at the original directory, so node failed with `Cannot find module
  C:\Program Files\nodejs\node_modules\npm\bin\npm-cli.js`. The command is now
  made reachable before it is rewritten, so both come from the copy.

### Changed

* `network.mode: open` is now the only mode that grants the Windows sandbox a
  network capability. An unrecognised mode relays rather than connecting direct,
  so a typo cannot silently disable the allowlist.
* Proxied Windows runs fail closed if the egress socket cannot be created, rather
  than falling back to a direct connection.

## [0.4.0] - 2026-08-18

**0.3.0 was never published.** It has a dated entry below and `version.go` claims
it, but no `v0.3.0` tag exists, so the newest downloadable build is `v0.2.0-beta`
— which predates every fix listed here. Anyone tracking releases has none of this.

### Security

Each of these was reproduced by execution, not inferred from reading.

* **Linux containment did not work at all.** Every sandboxed launch failed on
  `/dev/null`: the read-only rules requested a directory-only Landlock right on a
  character device, which the kernel rejects, and the failure was fatal. It failed
  closed, so nothing was exposed — the feature was simply dead on every Linux system.
* **Linux proxy mode could not reach allowed hosts.** The egress proxy was started
  *inside* the loopback-only network namespace, leaving allowlisted traffic no route
  out. The proxy now runs outside the namespace and the contained process reaches it
  over a UNIX socket, which a namespace does not contain.
* **The Linux seccomp filter was inverted**, allowing the UDP it claimed to block
  and denying the AF_UNIX the sandbox needs. Unreachable while containment was dead;
  fixed alongside it.
* **`linux/arm64` used the wrong syscall numbers** — every entry one too high, so
  `landlock_restrict_self` invoked `memfd_secret` and the error misleadingly blamed
  the kernel version. This is a published release target.
* **The Linux sandbox could read all of `~/.nvx`**, including other tools' persisted
  credentials in `tool_home`, the grants pin store, and `policy.json`. Narrowed to
  the runtime trees it actually needs.
* **macOS granted the sandbox write access to `~/.nvx` and the runtime directory**
  on the default path, so a contained process could rewrite `policy.json`,
  self-approve grants, poison `npm_global`, or replace the `node` binary every later
  run executes. A persistent sandbox defeat.
* **Windows never delivered piped stdio to the sandboxed child**, so every
  stdio-protocol daemon — that is, every MCP server — failed deterministically, while
  interactive use looked healthy.
* **Orphaned sandbox processes were not cleaned up** (Linux reaping, Windows job
  object), so abandoned launches accumulated until the tool had to be removed.
* **A cached binary path is now validated before execution** instead of being
  trusted to still be on `PATH`.
* **Fixed a data race in the egress proxy** that could abort a run outright: the
  session map was read without the lock guarding its writes, and ordinary parallel
  package-manager traffic could trigger it.

### Changed

* **macOS: `npm install -g` inside the sandbox is now denied**, matching Windows and
  Linux. Global installs write under `~/.nvx`, which is no longer writable from
  inside. This is the documented design, but it is a behaviour change for anyone who
  relied on the gap.
* **Windows: `nvx setup` is optional.** The sandbox runs unelevated; setup adds the
  loopback exemption that enables allowlisted egress.
* **Windows sandbox startup is roughly 13x faster** — a measured launch went from
  90.7s to 6.6s. The cost was an ACL walk re-granting directory ancestors on every
  launch, hanging on one of them until it timed out.
* **Uncontained runs are announced** rather than proceeding silently.

### Fixed

* **Documentation corrected where it overclaimed.** `README.md`, `SECURITY.md` and
  `docs/enforcement-matrix.md` stated that Windows restricts egress to the policy
  allowlist. It does not, unless an elevated `nvx setup` has run: by default the
  sandbox is granted `internetClient` and the proxy variables are stripped, so the
  allowlist is never consulted — not even cooperatively.
* **CI verifies containment instead of reporting green.** Both Windows smoke tests
  exited 0 unconditionally on CI, and the egress test asserted only that blocked
  traffic fails — which a sandbox denying everything passes perfectly. The privileged
  Linux tests now run, and both egress tests assert the allow path too.

### Added

* `nvx doctor` — diagnoses and repairs shim interception, including a shadowed
  persistent `PATH` on Windows.
* `nvx grants list` and `nvx grants reset [--all]`.
* `nvx import`, quiet/agent-mode flags, and wildcard trusted-package patterns.
* Containment v2: subcommand-aware classification, `isolation.level`
  (`standard`/`strict`), and `--strict`/`--standard` flags.
* Persistent per-tool guest profiles under `~/.nvx/tool_home`, so a trusted tool
  keeps its own state without being handed the real home directory.

### Known limitations

* **Windows egress is not allowlisted without an elevated `nvx setup`.** A
  no-elevation design has been shown feasible — an AppContainer can reach an AF_UNIX
  socket held by the parent, and intra-container loopback works — but it needs an
  in-container supervisor that does not exist yet.
* **The macOS fixes are verified at profile-generation level only.** Whether
  `sandbox-exec` enforces the generated profile as written has not been re-tested on
  macOS hardware.
* **The sandbox re-entrancy marker is a plain environment variable** and can be
  forged by a process able to set its own environment.

## [0.3.0] - 2026-07-05

### Added
* **Bun runtime**: `nvx install bun@1.2` (and `bun`/`latest`), managed the same way as Node.js with mandatory checksum verification. `bun`/`bunx` shims route to the Bun provider.
* **`runtime@version` CLI**: install/use/default/uninstall accept a runtime prefix; a bare version stays Node.js for nvm compatibility. Node and Bun can be active in one shell without evicting each other from `PATH`.
* **FilesystemProvider registry**: `native` and `docker` are first-class; `wsl`/`wslc`/`systemd-nspawn` are gated behind `NVX_EXPERIMENTAL=1`. An unavailable backend (e.g. Docker not running) fails closed before launch.
* **Docker hardening**: image selected per runtime; `offline`/`loopback` enforced with `--network none`; `--cap-drop=ALL`, `no-new-privileges`, `--pids-limit`, `tmpfs /tmp`.
* **Audit log**: `~/.nvx/audit.log` records egress allow/deny and policy-trust events.
* **Docs**: `SECURITY.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `docs/runtime-providers.md`, `docs/enforcement-matrix.md`, and a tag-triggered release workflow.

### Changed
* **JS runtime focus**: shipped runtimes are Node.js and Bun only. Deno, Go, Python, and uv/pyx work remains on the `feature/polyglot-runtimes` branch.
* **Project policy trust**: approved egress hosts persist under `~/.nvx/grants` (outside the project tree) instead of `.nvx-policy.json`. A project policy file that would weaken settings is ignored unless its exact contents are trusted for that project (prompted once; fail-closed when non-interactive).
* **Fail-closed policy parsing**: the Linux sandbox child aborts on a policy parse error instead of falling back to defaults.
* **Faster shims**: resolved runtime binary paths are cached (keyed by `PATH`) so the shim skips the expensive Windows `PATH` scan on repeat calls — dispatch overhead drops from ~100 ms to ~38 ms on Windows, and measures ~3 ms on Linux and ~4 ms on macOS (GitHub-hosted runners). See `scripts/bench.py`.

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
