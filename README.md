# nvx — Secure Runtime Version and Package Manager

![nvx logo](./assets/nvx_logo.png)



[![Go Version](https://img.shields.io/badge/Go-1.23+-00ADD8?style=flat-square&logo=go)](#) [![Platforms](https://img.shields.io/badge/Platform-Windows%20%7C%20macOS%20%7C%20Linux-blue?style=flat-square)](#) [![Release](https://img.shields.io/badge/Release-0.1.0--beta-orange?style=flat-square)](#) [![License](https://img.shields.io/badge/License-MIT-yellow.svg?style=flat-square)](LICENSE)




`nvx` is a zero-dependency, ultra-fast, and security-conscious version and package manager. Originally built to address the lack of robust, fast native runtime version managers on Windows, it naturally supports macOS and Linux as a cross-platform tool. 

It wraps package managers (`npm`/`yarn`/`pnpm`) and executors (`npx`/`bunx`) to enforce cascading policy files, check for typosquatting, scan for vulnerabilities, and run untrusted code inside native process-level sandboxes.


---

## Why nvx?

With modern LLMs, it's now practical to just build the exact tools you want. While setting up a clean development machine on Windows and facing the usual version manager headaches, I got thinking: *Why not build a modern, fast, secure runtime manager from scratch and solve this problem for good?*

Along the way, I wanted to tackle a few other common frustrations:
- **Supply Chain Safety**: Typosquatting and malicious postinstall scripts are a growing issue. `nvx` intercepts installs on the fly to flag or block suspected threats based on policies and registry checks.
- **Agentic & AI Safety**: If you use AI coding agents (like Gemini, Claude, or Copilot) to build projects, they execute terminal commands in your local workspace. By automatically wrapping typical package manager commands (`npm`, `yarn`, `pnpm`, `npx`), `nvx` ensures that any package installed or run by an AI agent is audited and secured out of the box. You don't have to worry about agents running raw commands in your shell; they are wrapped automatically, significantly reducing the risk of an agent pulling down a compromised package or executing rogue code.
- **Process Isolation**: I wanted a sandbox to run untrusted stuff (like `npx` packages) with a clean slate, scrubbing env secrets and locking down filesystem writes.
- **Sub-millisecond Performance**: The tool has to be fast enough that there's no noticeable overhead compared to running raw commands.
- **Clean UX**: Polished CLI output and automatic shell integration hooks for PowerShell, bash, and zsh.


---


## Features

- **Multi-Runtime Core**: Built to manage multiple runtimes (currently supports Node.js; extendable via `RuntimeProvider` to Bun, Deno, or other non-JavaScript/non-TS runtimes like Python, Go, and Rust).
- **Cascading Security Policies**: Resolves global and local directory-level policy blocks from `.nvx-policy.json`.
- **Registry-Backed Typosquatting Audits**: Cross-checks package names against a synced list of popular packages and queries the npm registry download API dynamically to verify download counts and distinguish typosquats from legitimate packages.

- **OSV Vulnerability Batch Scanning**: Audits dependency packages against the live Open Source Vulnerabilities database during install.
- **Supply-Chain Verification**: Flags package updates released in the last 24 hours (mitigating compromise propagation windows).
- **Native Sandbox Engine**: 
  - Purges credential/secret environment keys before execution.
  - Redirects home profile path (`HOME` / `USERPROFILE`) to temporary guest environments.
  - Uses Windows AppContainer + Low Integrity Level, Linux Landlock + kernel namespaces, and macOS Seatbelt (`sandbox-exec`) for secure sandboxing.
- **Shell Integrations**: Automatic shell configuration for bash, zsh, and PowerShell.

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
  install <version>      Download and install a Node.js version (e.g. 20, lts, latest)
  uninstall <version>    Remove an installed Node.js version
  use <version>          Switch Node.js version in the current terminal session (downloads automatically if missing)
  default <version>      Set the global default Node.js version (creates a link)
  list, ls               List all installed Node.js versions
  list-remote, ls-remote List available Node.js versions from nodejs.org
  env [--shell=<type>]   Print shell integration script (powershell, bash, zsh)
  auto [--shell=<type>]  Auto-switch version based on .nvmrc / .node-version / package.json
  verify-install <pkgs>  Verify package safety before installing (called by wrappers)
  init-shims             Generate PATH shims in ~/.nvx/bin
  cleanup                Remove stale sandbox sessions from previous runs
  version, -v            Print version info

Shim flags (npm, node, npx, yarn, pnpm, bunx via PATH):
  --no-sandbox           Run without sandbox for this invocation
  --filesystem-provider=<name>  Override isolation.filesystem.provider
  -y, --yes              Auto-approve prompts
```

### Zero-config sandbox

After `nvx env` / `init-shims`, **`npm`, `node`, `npx`, `yarn`, `pnpm`, and `bunx` are sandboxed by default** when `isolation.enabled` is true. No separate sandbox subcommand — just run commands normally:

```bash
npm install
npm run dev
node server.js
```

Use `--no-sandbox` on a shim invocation to bypass isolation for one command.

### Non-Interactive Use (CI)

Security prompts (vulnerability warnings, install script confirmations, typosquatting alerts) **fail closed** when no interactive terminal is available: the operation is denied rather than silently approved. In CI pipelines, pass `-y` / `--yes` or set `NVX_YES=true` to approve prompts explicitly.

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
  "prompts": {
    "interactive": "ask",
    "non_interactive": "deny",
    "network_unknown": "ask"
  },
  "environment": {
    "isolated_tools": false
  }
}
```

Policies cascade: the global policy applies everywhere, and local policy files merge over it as you get closer to the working directory (the nearest policy wins on conflicting settings; blocklists and trusted packages are unioned).

### Policy Reference
* **`enforce_ignore_scripts`**: When `true`, this forces npm/yarn/pnpm to install packages with `--ignore-scripts`. This blocks execution of hook scripts (`preinstall`/`postinstall`/`install`), which are heavily used in supply chain attacks to download and execute arbitrary binaries on the host machine.
* **`isolation.filesystem.provider`**: Where the process runs (filesystem + process boundary).
  - `native`: AppContainer + Low IL (Windows), Landlock + namespaces (Linux), Seatbelt (macOS). Fail-closed.
  - `docker`, `wslc`, `wsl`, `sandbox-exec`, `systemd-nspawn`: container or alternate providers (see below).
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
* **Filesystem** (`isolation.filesystem`): Windows AppContainer + Low IL; Linux Landlock + namespaces; macOS Seatbelt.
* **Network** (`isolation.network.mode: proxy`): egress via loopback proxy with allowlist; unknown hosts prompt interactively (fail-closed in CI unless `-y`).

### Verification matrix

| Guarantee | Windows (native) | Linux (native) | macOS (native) |
|-----------|------------------|----------------|----------------|
| Host profile write blocked | Yes (AppContainer + Low IL) | Yes (Landlock) | Yes (Seatbelt) |
| Workdir write allowed | Yes | Yes | Yes |
| Egress via policy proxy | Yes (AppContainer blocks WAN; loopback to parent proxy) | Yes (parent proxy; seccomp blocks new inet sockets in offline/loopback) | Yes (Seatbelt `(deny network*)` + loopback proxy ports) |
| Raw TCP bypass blocked at OS | Yes (no internet capability) | Partial (proxy for HTTP/SOCKS stacks; raw connect not filtered by seccomp) | Yes (Seatbelt) |
| Fail-closed if FS primitive missing | Yes | Yes (kernel 5.13+) | Yes |

---

## Design DX & Architecture FAQ

### How does auto-swapping work alongside concurrent terminal sessions?
Traditional managers change system-wide paths or symbolic links, which can disrupt active builds running in other windows. `nvx` avoids this by configuring the paths (`PATH`, `NPM_CONFIG_PREFIX`) strictly at the **shell session level**. When you change versions in one shell (or navigate to a directory triggering auto-swap), only that shell’s environment is updated. Other concurrent processes are completely unaffected.

### How do sandboxed containers handle local servers, ports, and networking?
Web development requires running local dev servers (e.g. listening on port `3000`) and calling external backend APIs or databases:
* **Native Sandbox**: Loopback bind/connect works for dev servers. With `network.mode: proxy`, outbound TCP goes through the nvx allowlist proxy (HTTP_PROXY / SOCKS5). Host services on `localhost` remain reachable via `allow_hosts`.
* **Docker Sandbox**: The Docker container automatically exposes loopback TCP configurations so that the containerized process can reach a backend running locally on the host machine.

### What if a project needs multiple runtimes (e.g., Node frontend and Python/Go backend)?
* **Native Sandbox**: Since the sandbox scrubs environment variables and configures paths on top of your host's standard toolchains, other runtimes installed on your machine (like Python or Go) are fully visible and execute alongside Node.js.
* **Docker Sandbox**: The default Docker isolation uses a base Node.js image (e.g., `node:20`). If you require a multi-language stack (Node + Python), you can easily package a custom Dockerfile or spin them up via standard container tools (like `docker-compose`) to network them together.

### Does nvx handle TypeScript and bundler commands?
Yes! Since `nvx` hooks into the active runtime context, any globally or locally installed tools—including `tsc`, `ts-node`, `vite`, or `webpack`—execute within the selected Node.js environment automatically.

### How does automatic command wrapping protect me when using AI coding agents?
When AI coding agents (like Gemini, Claude, or Copilot) interact with your workspace, they typically run standard commands such as `npm install <package>` or `npx <command>`. Because `nvx` automatically wraps these typical binaries inside the shell session, those commands are transparently intercepted. The packages are verified via typosquatting and vulnerability registry checks, and executors are run within the native sandbox environment. This happens automatically without any special configuration or wrapper commands required from the agent, meaning you don't have to worry about an autonomous agent accidentally downloading or executing a malicious supply-chain package.

---

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE) for details.

