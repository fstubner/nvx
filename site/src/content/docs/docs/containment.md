---
title: Containment
description: What a contained command can and cannot reach on each platform, and what backs each claim.
---

## Zero-config sandbox

After `nvx env` / `init-shims`, **`node`, `npm`, `npx`, `yarn`, `pnpm`, `corepack`, `bun` and `bunx` are all intercepted**. The ones that execute code you did not write (package installs and `npx`-style tool runners) are sandboxed. **On Windows, `npm`, `npx`, `yarn` and `pnpm` run inside the sandbox. `bun` installs there only in projects on the drive Windows is installed on, see [Known limitations](/docs/limitations/).** Running your own code (`node server.js`, `npm run dev`) is *not* contained at the default `standard` level. `isolation.level: strict` extends containment to it. There is no separate sandbox subcommand. Run commands normally:

```bash
npm install
npm run dev
node server.js
```

Use `nvx --no-sandbox <command>` to bypass isolation for one command. The flag must come *before* the command. **All three containment flags (`--no-sandbox`, `--standard`, `--strict`) work only in that leading position.**

Written after the command, they belong to the command. nvx notices them, tells you they did not apply, and passes them through untouched. That is deliberate in both directions. A package's own arguments must not be able to turn the sandbox off around itself. nvx must not quietly reinterpret a word that belongs to another tool. `nvx tsc --strict` passes `--strict` to TypeScript, whose flag it is.

After `npm install`, run `nvx init-shims` (or any npm/yarn/pnpm shim) to refresh **project bin shims**. These route `node_modules/.bin` tools (e.g. `vite`, `eslint`) through nvx, so they use the pinned runtime and are audited. They are **not** contained at the default `standard` level. A local CLI is code your project chose to install, and nvx classifies it the same as your own code. `isolation.level: strict` contains them too.

The shims live in `~/.nvx/project-bin/<project hash>`, not inside the project, and a name that already resolves elsewhere on your `PATH` is never shimmed. Both rules exist because this directory sits ahead of System32 on your interactive `PATH`. Inside the project, a contained install could write a `git` there and have your next `git` run it uncontained. The cost is that a global tool of the same name now wins over the project-local one through nvx. `npx <tool>` still runs the local one.

## Inside the sandbox

When running in the sandbox:
* Environment secrets (e.g. `AWS_*`, `GITHUB_*`, `SSH_*`) are scrubbed. A contained command sees almost none of your environment, so a tool reading `CI` or `NODE_ENV` changes behaviour without erroring. nvx names the variables it drops, and `isolation.environment.allow` keeps the ones a project needs.
* Home and temp paths point into a guest profile under `~/.nvx`, never your real home. It is thrown away after each run. The exceptions are pnpm, which keeps one per project so its package store is there for the next install, and tools you approved as trusted.
* **Writes** go to the guest profile and the project directory. On macOS a few device files, such as `/dev/null`, are writable as well. The project's `.git` is the exception. A contained command can read it and cannot write it. Git runs outside the sandbox, so a hook or config entry left there would run as you. Everything else in the project stays writable, `package.json`, `node_modules` and lockfiles included, because an install has to write them.
* **Git hook installers** cannot set themselves up during a contained install. husky's `prepare` script, simple-git-hooks and lefthook write to `.git`. Run their setup yourself afterwards, for example `npx husky`, or run the install with `nvx --no-sandbox`.
* **Global installs** write outside the project, so `npm install -g` is refused inside the sandbox. `nvx --no-sandbox npm install -g` is an uncontained install, so treat it as one.
* **A command started in your home directory**, or above it, starts in the sandbox's home instead. Granting your home would grant everything in it, `~/.nvx` and your shell profile included. nvx says so when it happens. Run the command from a project folder.
* **A stray `package.json` above your projects** merges them into one sandbox scope. `nvx doctor` reports it when the manifest sits in your home directory or at a volume root.
* **Filesystem** (`isolation.filesystem`). Windows uses AppContainer, Linux uses Landlock with namespaces, and macOS uses Seatbelt.
* **Network** (`isolation.network.mode: proxy`). Egress goes through a loopback proxy with an allowlist. An unknown host is asked about at an interactive terminal, for that run only, and refused when nobody can answer. Only `NVX_TRUST_YES=true` approves one without asking. `-y`, `--agent-mode` and `NVX_YES` do not. A local service and a literal link-local address, such as the cloud metadata address `169.254.169.254`, are never offered. Only an `allow_hosts` entry allows one. Each host a run is allowed to reach is written to the audit log, once for each host and port in a run. nvx sets `HTTP_PROXY` and `HTTPS_PROXY`, and `NODE_USE_ENV_PROXY=1` for the Node versions that read it, so Node's `fetch`, `http` and `https` use the proxy as well. [Known limitations](/docs/limitations/) says which versions, and when a request to `127.0.0.1` goes to the proxy. On Windows the sandbox holds no network capability at all. The OS refuses direct connections, DNS does not resolve, and the only route out is nvx's proxy, reached over a UNIX socket. No elevation is required. `network.mode: open` opts out.
* **Time.** The first contained run in a project takes seconds, because that is when nvx makes and remembers the permission grants. Later ones take a few hundred milliseconds.

## Local servers and services

**A contained server needs `--expose` on Windows** to be reachable from your
machine, because Windows refuses connections into an AppContainer from outside
it. On Linux and macOS it is reachable without a flag.

**A service already running on your machine** is out of reach of a contained
tool until you allow it. Use `--connect` for one run, `allow_hosts` for a tool
that uses the proxy, or `network.mode: loopback`, which
[Policy](/docs/policy/#reference) describes.

## Non-interactive use (CI)

Security prompts (vulnerability warnings, install script confirmations, typosquatting alerts) **fail closed** when no interactive terminal is available. The operation is denied. In CI pipelines, set `NVX_YES=true` to approve these prompts explicitly. It does not approve a new egress host or a widening project policy. For direct `nvx` commands, leading `-y` / `--yes` is also supported. Package-manager flags after a shim command are forwarded to the package manager.

## Verification matrix

The three columns are not backed by equal evidence, and printing the same "Yes"
in each would imply they are. What backs each cell is stated in it. **Measured**
means someone ran the attack and watched it fail. **CI** means an automated check
on a real machine of that OS would fail if it stopped holding. **Profile only**
means the generated policy says so and nothing has tested the running system.

| Guarantee | Windows (native) | Linux (native) | macOS (native) |
|-----------|------------------|----------------|----------------|
| Host profile write blocked | Yes, measured | Yes, CI (Landlock) | Yes, CI (Seatbelt) |
| Workdir write allowed | Yes, measured | Yes, CI | Yes, CI |
| Project `.git` write blocked, read allowed | Yes, measured | Yes, CI (read-only bind mount) | Yes, CI (Seatbelt deny rule) |
| Host profile read blocked | Yes, measured | Yes, CI (Landlock allowlist) | **Partial**. CI confirms credential stores are denied and other reads are allowed |
| Egress blocked when not allowlisted | Yes, measured | Yes, CI | Yes, CI |
| Allowlisted host reachable through the proxy | Yes, measured (AppContainer + parent proxy over a UNIX socket) | Yes, CI (loopback-only netns + parent proxy over a UNIX socket) | Yes, CI (Seatbelt + loopback proxy) |
| Raw TCP/UDP bypass blocked at OS | Yes, measured (no network capability granted) | Yes, CI (netns + seccomp UDP deny) | Yes, CI (TCP and UDP. Which layer refuses TCP is untested) |
| Fail-closed if FS/network primitive missing | Yes, measured | Yes, CI (Landlock 5.13+, iproute2 for netns) | Yes, CI (refuses to run without `sandbox-exec`) |
| A contained server reachable from the host | Only via `--expose` | Yes | Yes |
| One named host service reachable from the sandbox | Via `allow_hosts` for proxy-aware clients, or `--connect` | Via `allow_hosts` for proxy-aware clients, or `--connect` except in `offline` | Via `allow_hosts` for proxy-aware clients, or `--connect` |

**What backs the Windows column.** Every "measured" above means a person ran
it on a real Windows machine before a release. The Windows containment probes
also run in CI on a hosted Windows runner. CI fails when a probe skips for any
reason other than a known host limitation.

The probes include one project's sandbox failing to read another's, a denied
secret staying hidden, and only allowlisted hosts being reachable through the
relay. Not every cell maps to a
probe CI runs. Weigh the Windows column as a person's word, a reproducible
command (`CONTRIBUTING.md`) and CI's probe run together.

**What backs the macOS column.**
`scripts/sandbox-enforcement-macos.sh` runs on a hosted macOS runner on every CI
build. It asserts the denials, not just that the command ran. It requires
`WRITE_OUTSIDE=DENIED`, `WRITE_INSIDE=ALLOWED`, `READ_OUTSIDE=ALLOWED`,
`EGRESS=DENIED`, `UDP_EGRESS=DENIED`, and an allowlisted host tunnelling through
the proxy with `CONNECT=200`. A further phase plants a `.npmrc` and an SSH key in
a throwaway home and requires the OS to refuse a contained read of each.

Every other assertion
runs with an empty allowlist, so all of them would also pass against a sandbox
that had failed to start. Requiring an allowlisted host to *succeed* is what
separates enforcement from breakage. The read weakness is asserted too, on
purpose. If the profile is ever tightened, CI fails and forces this table to be
updated with it.

One macOS cell is still not claimed. The probe's outbound TCP attempt is refused,
and nothing distinguishes a refusal at DNS from one at connect, which is a real
distinction on macOS. That cell stays open.

The [enforcement matrix](https://github.com/fstubner/nvx/blob/main/docs/enforcement-matrix.md)
has the evidence behind every cell. It lists the probe output, the CI runs, the
dates and the machines each was measured on.
