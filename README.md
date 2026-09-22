# nvx — contain what your agent installs

![nvx logo](./assets/nvx_logo.png)



[![Go Version](https://img.shields.io/badge/Go-1.23%2B-00ADD8?style=flat-square&logo=go)](#) [![Platforms](https://img.shields.io/badge/Platform-Windows%20%7C%20macOS%20%7C%20Linux-blue?style=flat-square)](#) [![Release](https://img.shields.io/badge/Release-0.5.7-orange?style=flat-square)](#) [![License](https://img.shields.io/badge/License-MIT-yellow.svg?style=flat-square)](LICENSE)




When a coding agent runs `npm install`, it executes code from strangers with your
credentials within reach. `nvx` puts that command inside an OS sandbox: a throwaway
`HOME`, writes confined to the project, and an allowlist for anything it tries to
reach over the network. On Windows and Linux it cannot read `~/.ssh` or `~/.npmrc`
either; on macOS reads are not contained, which [Known limitations](#known-limitations)
states plainly.

**You do not change how you run anything.** No wrapper command, no policy file to
write first, no agent configuration. nvx installs shims on `PATH`, so `npm install`
is still `npm install`, contained when it runs code you did not write.
That is the part other sandboxes leave to you: they need `theirtool run -- npm
install`, and an agent will not remember to type it.

It is also a **Node.js and Bun version manager**, because it has to be: the shims
that intercept the toolchain are the same ones that switch runtimes on `cd`. If you
use nvm, fnm or volta today, nvx replaces them.

Zero dependencies, one static binary, Windows/macOS/Linux. Installs are additionally
checked for typosquatting, known CVEs and suspiciously fresh releases.


---

## Why nvx?

With modern LLMs, it's now practical to just build the exact tools you want. While setting up a clean development machine on Windows and facing the usual version manager headaches, I got thinking: *Why not build a modern, fast, secure runtime manager from scratch and solve this problem for good?*

Along the way, I wanted to tackle a few other common frustrations:
- **Supply Chain Safety**: Typosquatting and malicious postinstall scripts are a growing issue. `nvx` intercepts installs on the fly to flag or block suspected threats based on policies and registry checks.
- **Agentic & AI Safety**: If you use AI coding agents (like Gemini, Claude, or Copilot) to build projects, they execute terminal commands in your local workspace. By automatically wrapping typical package manager commands (`npm`, `yarn`, `pnpm`, `npx`, `bun`, `bunx`), `nvx` audits what an AI agent installs and contains the commands that execute untrusted code, with no agent configuration required. No tool can promise a package is safe, so the goal is to limit what one can reach if the checks miss it.

  What a contained install can reach: your `package.json`, your lockfile, `node_modules`, and the rest of the project directory it is installing into. Environment variables are scrubbed and writes cannot leave the project.

  What it cannot reach, **on Windows and Linux**: your home directory — SSH keys, cloud credentials, `~/.npmrc` and its publish token — along with every other project on disk, and anywhere outside the project for writes. That is the class of attack this is built for: credential theft and persistence.

  **On macOS, reads are not contained.** The Seatbelt profile allows filesystem reads, so a contained install can read `~/.ssh`, `~/.aws` and `~/.npmrc` by absolute path. Redirecting `$HOME` does not prevent that. What macOS does enforce is write containment and egress control. The reason is in [docs/enforcement-matrix.md](docs/enforcement-matrix.md): the dynamic linker must read system libraries whose locations vary by macOS version, and a strict read allowlist stops processes launching at all. If credential *reads* are what you need contained, macOS does not yet give you that.

  **Two limits worth stating plainly.** A `.env` file *inside the project* is readable by a contained install, because the project directory has to be readable for the install to work at all — see [Known limitations](#known-limitations). And containment covers installs and ad-hoc tools, not your own code: `npm run build` runs uncontained by default, so a dependency your own code imports is not sandboxed. `isolation.level: strict` extends containment to your own code.
- **Process Isolation**: I wanted a sandbox to run untrusted stuff (like `npx` packages) with a clean slate: a throwaway `HOME`, scrubbed env secrets, and writes locked to the project.
- **Thin wrapper**: A single static Go binary with no runtime dependencies. Each wrapped command runs through one extra short-lived process; resolved binary paths are cached (keyed by `PATH`) so the shim doesn't rescan `PATH` on every call. Measured dispatch overhead: **about 75 ms on Windows**, and not currently established on Linux or macOS. Every figure this line carried before 2026-09-03 has been withdrawn: [`scripts/bench.py`](scripts/bench.py) invoked the shim as `nvx shim node --no-sandbox`, and nvx deliberately passes a wrapped command's own arguments through untouched, so `--no-sandbox` went to node, which answered `bad option` and exited 9. The wrapped half of every published measurement was timing an argument-parsing failure, not a run. The script now verifies both halves exit 0 and print a marker from inside the runtime before it times anything. The Windows figure is three runs on one machine (median 73.8, 74.2, 77.1 ms, p10–p90 roughly 63–93); a fourth run on a busy machine was refused by the script's own spread check rather than published. In a Linux container the median came out at 4–10 ms but the spread swamped it and the script declined to give a figure; macOS has no measurement at all. Still imperceptible next to the commands you actually wait on, like `npm install`, but do not quote a number this file does not.
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
  import [nvm|fnm|volta]   Import Node.js versions already installed via nvm, fnm
                           or volta (defaults to scanning all three)
  init-shims               Generate PATH shims in ~/.nvx/bin (and project bin
                           shims when run inside a project)
  policy init              Create default policy files (--global, --project, --force)
  policy check             Check this project against the policy in force, with a
                           distinct exit code per failure class, for CI
                           (--format=json, --online)
  policy explain           Show each setting's effective value and where it came
                           from
  doctor [--fix]           Check that nvx intercepts node/npm/npx on PATH.
                           Diagnosis is read-only; --fix repairs a shadowed
                           persistent PATH
  grants list              Show this project's approved egress hosts, trusted
                           tools, and policy pins
  grants reset [--all]     Forget this project's grants (or every project's)
  audit [--summary]        Review the local record of security decisions, and of
                           past runs when NVX_TRACE=1 (--runs, --failures,
                           --limit=N, --all)
  audit export             Export that record as json, jsonl or csv, filtered by
                           time and event (--since, --event, --format, --out)
  report [--out=FILE]      Collect version, interception, policy and log tails
                           into one file to read and send on. Nothing is uploaded
  cleanup                  Reclaim disk from interrupted runs now (rarely needed;
                           every run reclaims some automatically)
  setup [--undo]           (Windows, Administrator) Grant the sandbox stat access
                           to the roots of the volumes nvx, your profile and the
                           current directory live on, and remove a loopback
                           exemption an older nvx left. Optional: installs and
                           `npx` do not need it. Slow on a large volume
  verify-install <pkgs>    Verify package safety before installing (called by shims)
  shim <cmd> [args]        Internal shim router (called by generated wrappers)
  version, -v              Print version info
  help [command]           Show this list, or detail for one command

Isolation flags — ALL of these go BEFORE the command, as
`nvx --no-sandbox npm ...`. Written after it they belong to the command: nvx
notices them, says they did not apply, and passes them through untouched. That
holds in both directions — no package can escape the sandbox by tacking a flag
onto npm, and nvx does not reinterpret a word that belongs to another tool:
  --no-sandbox             Run this invocation without the sandbox
  --standard               Force standard containment, overriding a project
                           policy that sets strict
  --strict                 Contain your own code too, not just installs and
                           ad-hoc tools
  --expose <in>[:<host>]   (Windows) Publish a port a server inside the sandbox
                           listens on, so the host can reach it — Windows
                           refuses connections into an AppContainer. The two
                           numbers must differ; omit the host one to have a free
                           port picked and printed. Grants no network capability
  --connect <host>[:<in>]  Let the sandbox reach one service already running on
                           your machine — the mirror of `--expose`. Give the port
                           your service uses; nvx runs a listener and dials the
                           real one itself. The two numbers must differ. Grants
                           no network capability

Passed to the wrapped command only, not before it:
  --filesystem-provider=<name>  Override isolation.filesystem.provider
                           (native | docker). `nvx npm --filesystem-provider=…`,
                           not `nvx --filesystem-provider=… npm`

Options:
  --shell=<type>           Shell syntax to emit: powershell, bash, zsh
  -y, --yes                Auto-approve all prompts
  -q, --quiet              Suppress success/info messages (errors and warnings
                           still print)
  --verbose                Show what nvx is doing on the way: checks, session
                           ids, permission work. Also NVX_VERBOSE=1 (env)
  NVX_TRACE=1              (env) Record one line per run in ~/.nvx/audit.log for
                           `nvx audit`. Off by default; a local debugging aid
  --agent-mode             Equivalent to -y -q; also settable with
                           NVX_AGENT_MODE=1
  NVX_NONINTERACTIVE=1     (env) Deny every prompt instead of asking, so a run
                           needing approval fails rather than waits
  NVX_TRUST_YES=true       (env) Approve trust prompts specifically — adding an
                           egress host, trusting a tool or a project policy.
                           -y and --agent-mode deliberately do not
  NVX_HOME=<dir>           (env) Use a different nvx home instead of ~/.nvx
```

### Zero-config sandbox

After `nvx env` / `init-shims`, **`node`, `npm`, `npx`, `yarn`, `pnpm`, `bun` and `bunx` are all intercepted**, and the ones that execute code you did not write (package installs and `npx`-style tool runners) are sandboxed. **On Windows, `npm`, `npx` and `yarn` run inside the sandbox. `pnpm` runs for a first install only, and `bun` does not run, see [Known limitations](#known-limitations).** Running your own code (`node server.js`, `npm run dev`) is *not* contained at the default `standard` level; `isolation.level: strict` extends containment to it. There is no separate sandbox subcommand. Run commands normally:

```bash
npm install
npm run dev
node server.js
```

Use `nvx --no-sandbox <command>` to bypass isolation for one command — the flag must come *before* the command. **All three containment flags (`--no-sandbox`, `--standard`, `--strict`) work only in that leading position.** Written after the command they belong to the command: nvx notices them, tells you they did not apply, and passes them through untouched. That is deliberate in both directions — a package's own arguments must not be able to turn the sandbox off around itself, and nvx must not quietly reinterpret a word that belongs to another tool. `--strict` was honoured in either position until 0.5.6, which meant `nvx tsc --strict` was silently sandboxed: `--strict` is TypeScript's flag, not nvx's.

After `npm install`, run `nvx init-shims` (or any npm/yarn/pnpm shim) to refresh **project bin shims**. These route `node_modules/.bin` tools (e.g. `vite`, `eslint`) through nvx, so they use the pinned runtime and are audited. They are **not** contained at the default `standard` level: a local CLI is code your project chose to install, which nvx classifies the same as your own code. `isolation.level: strict` contains them too.

The shims live in `~/.nvx/project-bin/<project hash>`, not inside the project, and a name that already resolves elsewhere on your `PATH` is never shimmed. Both rules exist because this directory sits ahead of System32 on your interactive `PATH`: inside the project, a contained install could write a `git` there and have your next `git` run it uncontained. The cost is that a global tool of the same name now wins over the project-local one through nvx — `npx <tool>` still runs the local one.

### Non-Interactive Use (CI)

Security prompts (vulnerability warnings, install script confirmations, typosquatting alerts) **fail closed** when no interactive terminal is available: the operation is denied rather than silently approved. In CI pipelines, set `NVX_YES=true` to approve prompts explicitly. For direct `nvx` commands, leading `-y` / `--yes` is also supported; package-manager flags after a shim command are forwarded to the package manager.

### Auto-Swapping

`nvx` automatically detects configuration files (`.nvmrc`, `.node-version`, and `package.json` engines) when you navigate to a directory, prompting to install the required Node.js version if missing, and switching to it.

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
trust boundary ignore `-y`/`NVX_YES` entirely (see above).

Policies cascade: the global policy applies everywhere, and local policy files merge over it as you get closer to the working directory (the nearest policy wins on conflicting settings; blocklists and trusted packages are unioned).

### Policy Reference
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
* **`isolation.filesystem.provider`**: Where the process runs (filesystem + process boundary). See the [enforcement matrix](docs/enforcement-matrix.md) for exact guarantees.
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

The three columns are not backed by equal evidence, and printing the same "Yes"
in each would imply they are. What backs each cell is stated in it: **measured**
means someone ran the attack and watched it fail, **CI** means an automated check
on a real machine of that OS would fail if it stopped holding, and **profile
only** means the generated policy says so and nothing has tested the running
system.

| Guarantee | Windows (native) | Linux (native) | macOS (native) |
|-----------|------------------|----------------|----------------|
| Host profile write blocked | Yes — measured | Yes — CI (Landlock) | Yes — CI (Seatbelt) |
| Workdir write allowed | Yes — measured | Yes — CI | Yes — CI |
| Host profile read blocked | Yes — measured | Yes — CI (Landlock allowlist) | **No** — CI confirms reads are allowed |
| Egress blocked when not allowlisted | Yes — measured | Yes — CI | Yes — CI |
| Allowlisted host reachable through the proxy | Yes — measured (AppContainer + parent proxy over a UNIX socket) | Yes — CI (loopback-only netns + parent proxy over a UNIX socket) | Yes — CI (Seatbelt + loopback proxy) |
| Raw TCP/UDP bypass blocked at OS | Yes — measured (no network capability granted) | Yes — CI (netns + seccomp UDP deny) | Yes — CI (TCP and UDP; which layer refuses TCP is untested) |
| Fail-closed if FS/network primitive missing | Yes — measured | Yes — CI (Landlock 5.13+, iproute2 for netns) | Yes — CI (refuses to run without `sandbox-exec`) |
| A contained server reachable from the host | Only via `--expose` | Yes | Yes |
| One named host service reachable from the sandbox | Only via `--connect` | Only via `--connect`, except in `offline`/`loopback` | Only via `--connect` |

**What backs the Windows column, and what does not.** Every "measured" above means
a person ran it on a real Windows machine before a release. **No automated check
proves that run happened.** Hosted Windows runners refuse to create AppContainer
children — `CreateProcess` returns "Access is denied" for every executable — so
the containment probes skip in CI, and a skip is a pass. Since 2026-09-03 CI does
fail if a probe skips for any reason *other* than that known host limitation,
which catches a probe that quietly stops running; it cannot substitute for the
manual gate. The last hand-run gate — `NVX_PROBE=1 go test -race -timeout 40m ./internal/nvx`,
with a runtime installed and set as the global default — was **492 pass, 6 skip,
0 fail** on 2026-09-03, the six skips being the ones `CONTRIBUTING.md` names. Weigh
the Windows column as a person's word plus a reproducible command
(`CONTRIBUTING.md`), and the Linux and macOS columns as a machine's.

**What backs the macOS column, as of 2026-08-24.**
`scripts/sandbox-enforcement-macos.sh` runs on a hosted macOS runner on every CI
build and asserts the denials, not just that the command ran. Latest run:
`WRITE_OUTSIDE=DENIED`, `WRITE_INSIDE=ALLOWED`, `READ_OUTSIDE=ALLOWED`,
`EGRESS=DENIED`, `UDP_EGRESS=DENIED`, and an allowlisted host tunnelling through
the proxy with `CONNECT=200`.

That last one matters more than its size suggests: every other assertion runs
with an empty allowlist, so all of them would also pass against a sandbox that
had failed to start. Requiring an allowlisted host to *succeed* is what separates
enforcement from breakage. The read weakness is asserted too, deliberately — if
the profile is ever tightened, CI fails and forces this table to be updated with
it.

One macOS cell is still not claimed: the probe's outbound TCP attempt is refused,
but nothing distinguishes a refusal at DNS from one at connect, and per the
per-OS notes that distinction is real on macOS. It is left open rather than
rounded up.

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

A security tool that overstates its reach is worse than one that is narrow and
honest, so the limits that change what you should expect are listed here. The
threat model behind them is in [SECURITY.md](SECURITY.md), and the per-platform
evidence each claim rests on -- probe output, dates, the machines it was measured
on -- is in [docs/enforcement-matrix.md](docs/enforcement-matrix.md).

### What containment does not cover

- **Your own code is not contained by default.** `npm run build`, `npm test` and
  `node` run uncontained at the `standard` level, so a compromised dependency your
  own code imports is not sandboxed. A contained install can therefore influence a
  later uncontained command, because `node_modules/.bin` is writable by design.
  `isolation.level: strict` contains them, at the cost of breaking anything that
  needs unrestricted filesystem or network access.
- **A `.env` inside the project is readable by a contained install.** The project
  directory has to be readable for the install to work, and `.env` lives in it.
  Environment *variables* are scrubbed; a file is a file.
- **On macOS, reads are not contained.** Writes and egress are. The Seatbelt
  profile has to allow filesystem reads because the dynamic linker loads system
  libraries whose locations move between macOS versions.
- **On Windows, your home directory is listable.** Contents stay unreadable, so
  `~/.ssh`, `~/.aws` and `~/.npmrc` cannot be read, but their presence is visible.
  That is an ACE Windows ships on your profile and nvx cannot revoke.
- **Detection is best-effort.** Typosquat and vulnerability checks reduce risk
  without certifying a package. Containment is the backstop, not the checks.

### What surprises people

- **A stray `package.json` above your projects merges them into one sandbox
  scope.** `nvx doctor` reports it when the manifest sits in your home directory
  or at a volume root.
- **`npm install -g` is refused** inside the sandbox, because a global install
  writes outside the project. `nvx --no-sandbox npm install -g` is an uncontained
  install, so treat it as one.
- **A contained command sees almost none of your environment.** A tool reading
  `CI` or `NODE_ENV` changes behaviour without erroring. nvx names the variables
  it drops, and `isolation.environment.allow` keeps the ones a project needs.
- **On Windows, a contained process cannot create a pipe.** Synchronous and
  streaming capture are brokered by nvx; `child_process.fork` is refused outright
  and names `--no-sandbox`.
- **A contained server needs `--expose` to be reachable from your machine**, and a
  contained tool needs `--connect` to reach a service you are already running.
- **On Windows, `pnpm` runs inside the sandbox for a first install only, and
  `bun` does not run.** pnpm asks the OS to turn a file handle back into a
  drive-letter path, which an AppContainer is refused: `GetFinalPathNameByHandle`
  and `QueryDosDevice` answer "Access is denied" from inside the container,
  while the NT form of the same path comes back fine. The drive letters live in
  an object directory the system owns, and no file permission reaches it. A
  session-local drive letter pointing at the same volume, unelevated, is
  reachable and openable from inside the container once granted, but the
  refused call does not fall back to it: `sandbox_local_drive_probe_windows_test.go`
  has the measurement. Measured 2026-09-17 with pnpm 8.7.5: a first `pnpm install` in a fresh
  project exits 0, and a second one fails with `EPERM realpath
  'node_modules'`. `bun install` fails on every run, with `ENOENT` on 1.3.1
  and `EBADF` on 1.4.2, and `bun -e` cannot read its own working directory;
  bun's call has not been traced, but the shape is the same. npm and yarn
  resolve paths in JavaScript and never ask. Use `--no-sandbox` for those two,
  or npm or yarn instead.
- **On Windows, `yarn` classic fails in a project under your user profile if you
  have a `~/.yarnrc`.** yarn reads every `.yarnrc` on the way up from the
  project to the drive root, and the sandbox refuses the one in your real home;
  yarn treats that refusal as fatal. Projects outside the profile are fine.
  Measured 2026-09-17 with yarn 1.22.19.
- **A package published in the last 24 hours is held** pending your approval, so
  an MCP server launched by an editor fails to start rather than prompting.
- **The first contained run in a project takes seconds; later ones take a few
  hundred milliseconds.**
- **Windows may flag nvx as malware, and releases are not Authenticode-signed.**


## Design DX & Architecture FAQ

### How does auto-swapping work alongside concurrent terminal sessions?
Traditional managers change system-wide paths or symbolic links, which can disrupt active builds running in other windows. `nvx` avoids this by configuring the paths (`PATH`, `NPM_CONFIG_PREFIX`) strictly at the **shell session level**. When you change versions in one shell (or navigate to a directory triggering auto-swap), only that shell’s environment is updated. Other concurrent processes are completely unaffected.

### How do sandboxed containers handle local servers, ports, and networking?
Web development requires running local dev servers (e.g. listening on port `3000`) and calling external backend APIs or databases:
* **Native Sandbox**: outbound TCP goes through the nvx allowlist proxy (HTTP_PROXY / SOCKS5) under `network.mode: proxy`, and host services on `localhost` are reachable via `allow_hosts`.

  **On Windows, a dev server started inside the sandbox needs `--expose` to be reachable from your browser.** The bind succeeds and the server reports itself listening, but Windows blocks connections into an AppContainer from outside it — the same restriction the egress relay exists to work around, and unaffected by the loopback exemption. This affects `nvx npx vite`, `npx serve` and anything else that serves a port. Since 0.5.5, `nvx --expose 5173:8080 npx vite` publishes it: give the port your server uses inside, then the port you want to visit (they cannot be the same number — see [Known limitations](#known-limitations)). `npm run dev` is uncontained at the default `standard` level anyway, and `nvx --no-sandbox npx <tool>` remains the way to opt out entirely. This entry claimed dev servers worked, with no platform qualifier, until 2026-08-20, and still said they were simply unreachable until 0.5.5 shipped the fix.
* **Docker Sandbox**: With `network.mode: open`, the container can reach host services via the standard Docker host gateway. With `offline`/`loopback` the container runs with `--network none` (no network at all). Allowlisted `proxy` mode is not supported under Docker — use the native provider when you need per-host egress control.

### What if a project needs both Node and Bun?
`nvx use node@20` and `nvx use bun@1.2` activate independently in the same shell without evicting each other from `PATH`.
* **Native Sandbox**: Other toolchains already installed on your host remain visible and run alongside nvx-managed runtimes (not sandboxed unless shimmed).
* **Docker Sandbox**: The image is chosen from the active runtime (`node:<v>` or `oven/bun:<v>`). For a multi-language stack, supply your own image via a Dockerfile or `docker-compose`.

### Does nvx handle TypeScript and bundler commands?
Yes! Since `nvx` hooks into the active runtime context, any globally or locally installed tool (`tsc`, `ts-node`, `vite`, `webpack`) executes within the selected Node.js environment automatically.

### How does automatic command wrapping protect me when using AI coding agents?
When AI coding agents (like Gemini, Claude, or Copilot) interact with your workspace, they typically run standard commands such as `npm install <package>` or `npx <command>`. Because `nvx` automatically wraps these typical binaries inside the shell session, those commands are transparently intercepted. The packages are checked against typosquatting and vulnerability (OSV) registries, and executors run inside the native sandbox, with no special configuration or wrapper commands required from the agent. This is defense-in-depth that raises the bar against common supply-chain patterns (typosquats, known-vulnerable versions, install-script execution); it reduces risk substantially but is not a guarantee against a determined or novel attacker. See [SECURITY.md](SECURITY.md) for the threat model and its limits.

---

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE) for details.

