# nvx — Secure Runtime Version and Package Manager

![nvx logo](./assets/nvx_logo.png)



[![Go Version](https://img.shields.io/badge/Go-1.23%2B-00ADD8?style=flat-square&logo=go)](#) [![Platforms](https://img.shields.io/badge/Platform-Windows%20%7C%20macOS%20%7C%20Linux-blue?style=flat-square)](#) [![Release](https://img.shields.io/badge/Release-0.5.0-orange?style=flat-square)](#) [![License](https://img.shields.io/badge/License-MIT-yellow.svg?style=flat-square)](LICENSE)




`nvx` is a fast, cross-platform **JavaScript runtime version manager** that **audits and sandboxes npm-family toolchain commands — including whatever your AI coding agents run**. It manages **Node.js and Bun** like nvm/fnm, then adds an ambient security layer: installs are checked for typosquatting and known vulnerabilities, and the commands that run untrusted code — package installs and `npx`-style tool runners — are executed inside a native OS sandbox.

Zero dependencies, one static binary, Windows/macOS/Linux. Originally built to fix the lack of a fast native runtime manager on Windows; the security layer is what makes it worth switching to.


---

## Why nvx?

With modern LLMs, it's now practical to just build the exact tools you want. While setting up a clean development machine on Windows and facing the usual version manager headaches, I got thinking: *Why not build a modern, fast, secure runtime manager from scratch and solve this problem for good?*

Along the way, I wanted to tackle a few other common frustrations:
- **Supply Chain Safety**: Typosquatting and malicious postinstall scripts are a growing issue. `nvx` intercepts installs on the fly to flag or block suspected threats based on policies and registry checks.
- **Agentic & AI Safety**: If you use AI coding agents (like Gemini, Claude, or Copilot) to build projects, they execute terminal commands in your local workspace. By automatically wrapping typical package manager commands (`npm`, `yarn`, `pnpm`, `npx`, `bun`, `bunx`), `nvx` audits what an AI agent installs and contains the commands that execute untrusted code, with no agent configuration required. No tool can promise a package is safe, so the goal is to limit what one can reach if the checks miss it.

  What a contained install can reach: your `package.json`, your lockfile, `node_modules`, and the rest of the project directory it is installing into. Environment variables are scrubbed and writes cannot leave the project.

  What it cannot reach: your home directory — SSH keys, cloud credentials, `~/.npmrc` and its publish token — along with every other project on disk, and anywhere outside the project for writes. That is the class of attack this is built for: credential theft and persistence.

  **Two limits worth stating plainly.** A `.env` file *inside the project* is readable by a contained install, because the project directory has to be readable for the install to work at all — see [Known limitations](#known-limitations). And containment covers installs and ad-hoc tools, not your own code: `npm run build` runs uncontained by default, so a dependency your own code imports is not sandboxed. `isolation.level: strict` extends containment to your own code.
- **Process Isolation**: I wanted a sandbox to run untrusted stuff (like `npx` packages) with a clean slate: a throwaway `HOME`, scrubbed env secrets, and writes locked to the project.
- **Thin wrapper**: A single static Go binary with no runtime dependencies. Each wrapped command runs through one extra short-lived process; resolved binary paths are cached (keyed by `PATH`) so the shim doesn't rescan `PATH` on every call. Measured dispatch overhead: **~3 ms on Linux, ~4 ms on macOS, ~38 ms on Windows** (process creation is costlier there) — imperceptible next to the commands you actually wait on like `npm install`. Reproduce it with [`scripts/bench.py`](scripts/bench.py).
- **Clean UX**: Polished CLI output and automatic shell integration hooks for PowerShell, bash, and zsh.


---


## Features

- **Multi-Runtime Core**: Manages **Node.js and Bun** through a `RuntimeProvider` interface — see [docs/runtime-providers.md](docs/runtime-providers.md). Select Bun with `runtime@version` (e.g. `nvx install bun@1.2`); a bare version stays Node.js for nvm compatibility.
- **Cascading Security Policies**: Resolves global and local directory-level policy blocks from `.nvx-policy.json`.
- **Registry-Backed Typosquatting Audits**: Cross-checks package names against a synced list of popular packages and queries the npm registry download API dynamically to verify download counts and distinguish typosquats from legitimate packages.

- **OSV Vulnerability Batch Scanning**: Audits direct install packages, executor packages, and packages resolved from `package-lock.json` against the live Open Source Vulnerabilities database during install. If no lockfile is present, `package.json` package names are checked as a best-effort fallback.
- **Supply-Chain Verification**: Flags package versions published within a configurable window (default 24 hours); trusted packages are exempt.
- **Native Sandbox Engine**: 
  - Purges credential/secret environment keys before execution.
  - Redirects home profile path (`HOME` / `USERPROFILE`) to temporary guest environments.
  - Uses Windows AppContainer, Linux Landlock + kernel namespaces, and macOS Seatbelt (`sandbox-exec`) for secure sandboxing.
- **Shell Integrations**: Automatic shell configuration for bash, zsh, and PowerShell.

---

## How nvx compares

| | **nvx** | nvm | fnm | volta | asdf / mise | uv |
|---|---|---|---|---|---|---|
| Windows / macOS / Linux | ✅ / ✅ / ✅ | ➖ / ✅ / ✅ | ✅ / ✅ / ✅ | ✅ / ✅ / ✅ | ➖\* / ✅ / ✅ | ✅ / ✅ / ✅ |
| Single static binary | ✅ (Go) | shell script | ✅ (Rust) | ✅ (Rust) | ✅ (mise) | ✅ (Rust) |
| Runtimes managed | Node.js, Bun | Node | Node | Node | many (plugins) | Python |
| Auto-switch on `cd` | ✅ | shell hook | ✅ | ✅ | ✅ | project pin |
| Session-scoped switching (no global mutation) | ✅ | ✅ | ✅ | shims | shims | n/a |
| Checksum-verified downloads | ✅ | ✅ | ✅ | ✅ | varies | ✅ |
| Package resolution / lockfiles | ➖ | ➖ | ➖ | ➖ | ➖ | ✅ |
| Typosquat / OSV / release-age checks | ✅ | ➖ | ➖ | ➖ | ➖ | ➖ |
| OS sandbox for install/run | ✅ | ➖ | ➖ | ➖ | ➖ | ➖ |
| Egress allowlist for scripts | ✅ | ➖ | ➖ | ➖ | ➖ | ➖ |
| Env-secret scrubbing in sandbox | ✅ | ➖ | ➖ | ➖ | ➖ | ➖ |

<sub>\* asdf is Unix-only; mise adds Windows support. Rows reflect out-of-the-box defaults at time of writing.</sub>

nvx is not a package manager and does not resolve dependencies or manage lockfiles.

---

## Installation

### Windows (PowerShell)

Run the installer via PowerShell:

```powershell
irm https://raw.githubusercontent.com/fstubner/nvx/main/install.ps1 | iex
```

*Note: In future releases, I plan to make `nvx` accessible directly via **WinGet**, the **Windows Store**, and other popular repository managers for even easier setup.*

### macOS / Linux (Shell)

Run the installer via bash:

```bash
curl -fsSL https://raw.githubusercontent.com/fstubner/nvx/main/install.sh | sh
```

---

## CLI Usage

```text
nvx <command> [arguments]

Commands:
  install <[rt@]version>   Install a runtime version. Bare version = Node.js
                           (e.g. 20, lts, latest); prefix a runtime with @
                           (e.g. bun@1.2, bun).
  uninstall <[rt@]version> Remove an installed runtime version
  use <[rt@]version>       Switch the current terminal session (installs if missing)
  default <[rt@]version>   Set the global default for a runtime (creates a link)
  list, ls                 List installed runtimes and versions
  list-remote, ls-remote   List available Node.js versions from nodejs.org
  env [--shell=<type>]     Print shell integration script (powershell, bash, zsh)
  auto [--shell=<type>]    Auto-switch based on .nvmrc / .node-version /
                           .bun-version / package.json engines
  verify-install <pkgs>    Verify package safety before installing (called by wrappers)
  init-shims               Generate PATH shims in ~/.nvx/bin
  policy init              Create default policy files (--global, --project, --force)
  cleanup                  Remove stale sandbox sessions from previous runs
  version, -v              Print version info

Shim flags (node, npm, npx, yarn, pnpm, bun, bunx):
  --no-sandbox             Run without sandbox for this invocation
  --filesystem-provider=<name>  Override isolation.filesystem.provider
                           (native | docker; experimental: wsl, wslc, systemd-nspawn)
```

### Zero-config sandbox

After `nvx env` / `init-shims`, **`node`, `npm`, `npx`, `yarn`, `pnpm`, `bun` and `bunx` are all intercepted**, and the ones that execute code you did not write — package installs and `npx`-style tool runners — are sandboxed. Running your own code (`node server.js`, `npm run dev`) is *not* contained at the default `standard` level; `isolation.level: strict` extends containment to it. See [Known limitations](#known-limitations). No separate sandbox subcommand — just run commands normally:

```bash
npm install
npm run dev
node server.js
```

Use `nvx --no-sandbox <command>` to bypass isolation for one command — the flag must come *before* the command. Passed after it (`npm --no-sandbox install`) it is stripped and ignored, deliberately: otherwise a package's own arguments could turn the sandbox off around itself. `--strict` is the exception and is honoured in either position, because it only ever adds containment.

After `npm install`, run `nvx init-shims` (or any npm/yarn/pnpm shim) to refresh **project bin shims**. These route `node_modules/.bin` tools (e.g. `vite`, `eslint`) through nvx, so they use the pinned runtime and are audited. They are **not** contained at the default `standard` level: a local CLI is code your project chose to install, which nvx classifies the same as your own code. `isolation.level: strict` contains them too.

The shims live in `~/.nvx/project-bin/<project hash>`, not inside the project, and a name that already resolves elsewhere on your `PATH` is never shimmed. Both rules exist because this directory sits ahead of System32 on your interactive `PATH`: inside the project, a contained install could write a `git` there and have your next `git` run it uncontained. The cost is that a global tool of the same name now wins over the project-local one through nvx — `npx <tool>` still runs the local one.

### Non-Interactive Use (CI)

Security prompts (vulnerability warnings, install script confirmations, typosquatting alerts) **fail closed** when no interactive terminal is available: the operation is denied rather than silently approved. In CI pipelines, set `NVX_YES=true` to approve prompts explicitly. For direct `nvx` commands, leading `-y` / `--yes` is also supported; package-manager flags after a shim command are forwarded to the package manager.

### Auto-Swapping

`nvx` automatically detects configuration files (`.nvmrc`, `.node-version`, and `package.json` engines) when you navigate to a directory, prompting to install the required Node.js version if missing, and switching to it seamlessly.

---

## Policies (`policy.json` / `.nvx-policy.json`)

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
    "min_age_hours": 24
  },
  "runtime": {
    "default": "node",
    "versions": { "node": "20" }
  },
  "isolation": {
    "enabled": true,
    "filesystem": {
      "provider": "native",
      "mode": "strict"
    },
    "network": {
      "mode": "proxy",
      "default_allow": ["registry.npmjs.org:443", "api.osv.dev:443"],
      "allow_hosts": ["localhost:5432"],
      "prompt_unknown": true
    }
  },
  "environment": {
    "isolated_tools": false
  }
}
```

**Not yet implemented.** `prompts.interactive`, `prompts.non_interactive`,
`prompts.network_unknown` and `isolation.filesystem.mode` are parsed and merged
but nothing reads them, so setting any of them does nothing — including tightening
one. They were previously shown in this example and scaffolded by
`nvx policy init`, which made them look effective. Prompt behaviour is fixed:
interactive asks, non-interactive denies, and the two decisions that widen nvx's
trust boundary ignore `-y`/`NVX_YES` entirely (see above).

Policies cascade: the global policy applies everywhere, and local policy files merge over it as you get closer to the working directory (the nearest policy wins on conflicting settings; blocklists and trusted packages are unioned).

### Policy Reference
* **`enforce_ignore_scripts`**: When `true`, this forces npm/yarn/pnpm to install packages with `--ignore-scripts`. This blocks execution of hook scripts (`preinstall`/`postinstall`/`install`), which are heavily used in supply chain attacks to download and execute arbitrary binaries on the host machine.
* **`isolation.filesystem.provider`**: Where the process runs (filesystem + process boundary). See the [enforcement matrix](docs/enforcement-matrix.md) for exact guarantees.
  - `native` (default): AppContainer (Windows), Landlock + namespaces (Linux), Seatbelt (macOS). Zero-config, fail-closed.
  - `docker`: runs in a container (hardened; `offline`/`loopback` enforced via `--network none`). Requires Docker running.
  - `wsl`, `wslc`, `systemd-nspawn`: experimental; require `NVX_EXPERIMENTAL=1`.
* **`isolation.network.mode`**: How egress is governed.
  - `proxy` (default): parent-process HTTP CONNECT + SOCKS5 proxy with policy allowlist; injects `HTTP_PROXY` / `HTTPS_PROXY`.
  - `open`: no egress filtering.
  - `offline` / `loopback`: block non-loopback egress at the proxy.
* **`runtime.versions`**: Pin runtime versions used inside the sandbox (e.g. `"node": "20"`).
* **`environment.isolated_tools`**: When `true`, globally installed npm packages (`npm install -g`) are scoped to the project (`<project>/.nvx/npm_global`) instead of being shared through the active Node version. This lets different projects pin different versions of CLI tools (e.g. `vercel`, `eslint`) without conflicts. Takes effect on the next `nvx use` or directory auto-switch.

Override filesystem provider per shim: `npm --filesystem-provider=docker install`.



---

## Sandboxed Executions

Shimmed commands run in a sandbox session automatically:

```bash
npm run dev
node app.js
```

When running in the sandbox:
* Environment secrets (e.g. `AWS_*`, `GITHUB_*`, `SSH_*`) are scrubbed.
* Home and temp paths are virtualized to an ephemeral guest profile.
* **Filesystem** (`isolation.filesystem`): Windows AppContainer; Linux Landlock + namespaces; macOS Seatbelt.
* **Network** (`isolation.network.mode: proxy`): egress via loopback proxy with allowlist; unknown hosts prompt interactively (fail-closed in CI unless `NVX_YES=true`).

### Verification matrix

| Guarantee | Windows (native) | Linux (native) | macOS (native) |
|-----------|------------------|----------------|----------------|
| Host profile write blocked | Yes (AppContainer) | Yes (Landlock) | Yes (Seatbelt) |
| Workdir write allowed | Yes | Yes | Yes |
| Egress via policy proxy | Yes* (AppContainer + parent proxy over a UNIX socket) | Yes (loopback-only netns + parent proxy over a UNIX socket) | Yes (Seatbelt + loopback proxy) |
| Raw TCP/UDP bypass blocked at OS | Yes* (no network capability granted) | Yes (netns + seccomp UDP deny) | Yes (Seatbelt `(deny network*)`) |
| Fail-closed if FS/network primitive missing | Yes | Yes (Landlock 5.13+, iproute2 for netns) | Yes |

\* **Windows egress became enforced in 0.5.0 and was not before.** Until then the
sandbox held the `internetClient` capability and connected directly, so
`HTTP_PROXY` was a request a package could decline. It now holds no network
capability at all: the OS refuses direct connections and DNS does not resolve, and
the only route out is the parent's proxy, reached over a UNIX socket and re-exposed
inside the container by `nvx __appcontainer-exec`. No elevation is required.
`network.mode: open` opts out. See `docs/enforcement-matrix.md` for how it was
measured.

---

## Known limitations

Stated here rather than left implicit, because a security tool that overstates its
reach is worse than one that is narrow and honest. Each of these is measured, not
assumed; see `docs/enforcement-matrix.md` for the per-OS detail.

- **A `.env` inside the project is readable by a contained install.** The project
  directory must be readable for an install to work, and `.env` lives in it.
  Environment *variables* are scrubbed, but a file is a file. Secrets outside the
  project — `~/.ssh`, `~/.aws`, `~/.npmrc` — are unreachable.
- **Your own code is not contained by default.** Containment applies to installs and
  ad-hoc tool runners (`npx`, `bunx`). `npm run build`, `npm test` and `node` run
  uncontained under the default `standard` level, so a compromised dependency your
  own code imports is not sandboxed. Set `isolation.level: strict` to extend
  containment to your own code, at the cost of breaking anything that needs
  unrestricted filesystem or network access.

  Spelled out, because the two halves are usually stated apart: this means **a
  contained install can decide what a later uncontained command does.**
  `node_modules/.bin` is writable by an install by design, project-local CLIs
  there (`eslint`, `tsc`, `vitest`, `prettier`) get a shim on your `PATH`, and at
  `standard` those shims run uncontained as you. The shim relocation below stops
  a *system* command being shadowed; it does not make a project-local tool's
  contents trustworthy. `strict` contains them.
- **A contained process can see directory NAMES outside the project, though not
  their contents.** On Windows it can list your home directory, `C:\Users` and
  `C:\` — enough to learn that `.ssh`, `.aws` or `.1password` exist. File contents
  in those places stay unreadable.

  Two sources, and only one of them is Windows. The profile root carries an ACE for
  ALL APPLICATION PACKAGES that Windows ships and nvx cannot revoke (deny rules were
  measured not to override it). On a machine where an elevated `nvx setup` has run,
  `C:\`, `C:\Users` and the profile root *also* carry a read+execute grant that nvx
  added itself — an earlier version of this entry said nvx never adds it, which was
  wrong. `nvx setup --undo` removes those.
- **A loopback exemption left by a pre-0.5.0 `nvx setup` lets contained code reach
  every service on 127.0.0.1.** Local databases, daemon ports, another project's
  dev server — none of them need an `allow_hosts` entry while it is registered.
  Windows normally refuses an AppContainer's loopback connections, which is what
  the 0.5.0 egress design depends on; the older setup registered an exemption
  because the proxy then ran on the host's loopback. 0.5.0 never adds one and
  removes it during `nvx setup`, but that needs an Administrator terminal and is
  otherwise no longer required, so on an upgraded machine it persists.

  **Treat the egress allowlist as unenforced while it is registered.** Only
  *direct* connections to other hosts stay blocked. Any reachable loopback
  service that forwards traffic — a debugging proxy like mitmproxy or Charles, an
  `ssh -D` dynamic forward, a dev server's proxy route — turns this into
  arbitrary egress: measured on 2026-08-19 by completing a TLS exchange with an
  external host from inside a sandbox, through a CONNECT proxy on 127.0.0.1.
  nvx warns on every affected launch and `nvx doctor` reports it; removing it is
  one elevated command, which both of them print.
- **Projects granted by nvx before 0.5.0 stay reachable until nvx runs in them
  again.** Up to 0.5.0 every sandbox shared one identity and the permissions nvx
  granted were never revoked, so any project you used nvx in is readable and
  writable from any sandbox. The old permissions are removed the first time nvx runs
  in that project — but nvx has no list of where it has been, so the ones you do not
  revisit stay. To clean one by hand:
  `icacls <project> /remove:g *S-1-15-2-...` for each such entry `icacls <project>`
  lists.
- **A contained command is roughly 2 seconds, and the first one after a new runtime
  is staged can be minutes.** The ~38ms dispatch figure above measures the shim, not
  the sandbox: a contained launch has to prepare an isolated home and check
  permissions. Steady state has been measured at ~1s and ~2.2s on different
  machines; the first run after nvx stages a runtime copies the whole distribution
  and has been measured at 45s to 3 minutes. Uncontained commands are unaffected.
- **`npm install -g` is refused inside the sandbox**, because a global install
  writes outside the project. nvx points you at `nvx --no-sandbox npm install -g`,
  which is an uncontained install — treat it as one.
- **On Windows, a contained process cannot pipe a child's output.** An AppContainer
  is not allowed to create a named pipe, and that is how Windows builds piped child
  stdio — so a contained program that captures a subprocess's output (`execSync`
  with default options, `spawn(..., {stdio: 'pipe'})`) hangs rather than failing.
  Inherited and discarded stdio both work normally.

  **Synchronous capture is handled.** The restriction is on creating a pipe, not on
  file descriptors, so a preload in every contained node process routes
  `spawnSync`, `execSync` and `execFileSync` through temp files in the guest home.
  Their contract is "run it, give me the output at the end", which a file satisfies
  exactly. **`esbuild`** is the package this was measured against — its postinstall
  calls `execFileSync(..., {stdio: "pipe"})` and `npm install esbuild` used to hang
  forever; it now completes in seconds.

  **Streaming capture is not, and hangs.** Async `spawn(..., {stdio: 'pipe'})` is a
  real stream that a file cannot stand in for. A contained tool that reads a child's
  output as it is produced still blocks. nvx warns after two minutes naming this as
  a likely cause; install such a package with `nvx --no-sandbox npm install <pkg>`
  and treat it as an uncontained install.
- **The macOS sandbox profile is verified at generation level only.** The Seatbelt
  profile is asserted by tests; its runtime enforcement has not been re-verified on
  macOS hardware.
- **Detection is best-effort.** Typosquat and vulnerability checks reduce risk; they
  do not certify a package. Containment is the backstop, not the checks.

## Design DX & Architecture FAQ

### How does auto-swapping work alongside concurrent terminal sessions?
Traditional managers change system-wide paths or symbolic links, which can disrupt active builds running in other windows. `nvx` avoids this by configuring the paths (`PATH`, `NPM_CONFIG_PREFIX`) strictly at the **shell session level**. When you change versions in one shell (or navigate to a directory triggering auto-swap), only that shell’s environment is updated. Other concurrent processes are completely unaffected.

### How do sandboxed containers handle local servers, ports, and networking?
Web development requires running local dev servers (e.g. listening on port `3000`) and calling external backend APIs or databases:
* **Native Sandbox**: Loopback bind/connect works for dev servers. With `network.mode: proxy`, outbound TCP goes through the nvx allowlist proxy (HTTP_PROXY / SOCKS5). Host services on `localhost` remain reachable via `allow_hosts`.
* **Docker Sandbox**: With `network.mode: open`, the container can reach host services via the standard Docker host gateway. With `offline`/`loopback` the container runs with `--network none` (no network at all). Allowlisted `proxy` mode is not supported under Docker — use the native provider when you need per-host egress control.

### What if a project needs both Node and Bun?
`nvx use node@20` and `nvx use bun@1.2` activate independently in the same shell without evicting each other from `PATH`.
* **Native Sandbox**: Other toolchains already installed on your host remain visible and run alongside nvx-managed runtimes (not sandboxed unless shimmed).
* **Docker Sandbox**: The image is chosen from the active runtime (`node:<v>` or `oven/bun:<v>`). For a multi-language stack, supply your own image via a Dockerfile or `docker-compose`.

### Does nvx handle TypeScript and bundler commands?
Yes! Since `nvx` hooks into the active runtime context, any globally or locally installed tools—including `tsc`, `ts-node`, `vite`, or `webpack`—execute within the selected Node.js environment automatically.

### How does automatic command wrapping protect me when using AI coding agents?
When AI coding agents (like Gemini, Claude, or Copilot) interact with your workspace, they typically run standard commands such as `npm install <package>` or `npx <command>`. Because `nvx` automatically wraps these typical binaries inside the shell session, those commands are transparently intercepted. The packages are checked against typosquatting and vulnerability (OSV) registries, and executors run inside the native sandbox — with no special configuration or wrapper commands required from the agent. This is defense-in-depth that raises the bar against common supply-chain patterns (typosquats, known-vulnerable versions, install-script execution); it reduces risk substantially but is not a guarantee against a determined or novel attacker. See [SECURITY.md](SECURITY.md) for the threat model and its limits.

---

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE) for details.

