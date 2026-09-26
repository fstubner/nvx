---
title: Containment
description: What a contained command can and cannot reach on each platform, and what backs each claim.
---

## Zero-config sandbox

After `nvx env` / `init-shims`, **`node`, `npm`, `npx`, `yarn`, `pnpm`, `bun` and `bunx` are all intercepted**, and the ones that execute code you did not write (package installs and `npx`-style tool runners) are sandboxed. **On Windows, `npm`, `npx` and `yarn` run inside the sandbox. `pnpm` runs for a first install only, and `bun` does not run, see [Known limitations](/docs/limitations/).** Running your own code (`node server.js`, `npm run dev`) is *not* contained at the default `standard` level; `isolation.level: strict` extends containment to it. There is no separate sandbox subcommand. Run commands normally:

```bash
npm install
npm run dev
node server.js
```

Use `nvx --no-sandbox <command>` to bypass isolation for one command — the flag must come *before* the command. **All three containment flags (`--no-sandbox`, `--standard`, `--strict`) work only in that leading position.** Written after the command they belong to the command: nvx notices them, tells you they did not apply, and passes them through untouched. That is deliberate in both directions — a package's own arguments must not be able to turn the sandbox off around itself, and nvx must not quietly reinterpret a word that belongs to another tool. `--strict` was honoured in either position until 0.5.6, which meant `nvx tsc --strict` was silently sandboxed: `--strict` is TypeScript's flag, not nvx's.

After `npm install`, run `nvx init-shims` (or any npm/yarn/pnpm shim) to refresh **project bin shims**. These route `node_modules/.bin` tools (e.g. `vite`, `eslint`) through nvx, so they use the pinned runtime and are audited. They are **not** contained at the default `standard` level: a local CLI is code your project chose to install, which nvx classifies the same as your own code. `isolation.level: strict` contains them too.

The shims live in `~/.nvx/project-bin/<project hash>`, not inside the project, and a name that already resolves elsewhere on your `PATH` is never shimmed. Both rules exist because this directory sits ahead of System32 on your interactive `PATH`: inside the project, a contained install could write a `git` there and have your next `git` run it uncontained. The cost is that a global tool of the same name now wins over the project-local one through nvx — `npx <tool>` still runs the local one.

## Inside the sandbox

When running in the sandbox:
* Environment secrets (e.g. `AWS_*`, `GITHUB_*`, `SSH_*`) are scrubbed.
* Home and temp paths are virtualized to an ephemeral guest profile.
* **Filesystem** (`isolation.filesystem`): Windows AppContainer; Linux Landlock + namespaces; macOS Seatbelt.
* **Network** (`isolation.network.mode: proxy`): egress via loopback proxy with allowlist; unknown hosts prompt interactively (fail-closed in CI unless `NVX_YES=true`).

## Non-interactive use (CI)

Security prompts (vulnerability warnings, install script confirmations, typosquatting alerts) **fail closed** when no interactive terminal is available: the operation is denied rather than silently approved. In CI pipelines, set `NVX_YES=true` to approve prompts explicitly. For direct `nvx` commands, leading `-y` / `--yes` is also supported; package-manager flags after a shim command are forwarded to the package manager.

## Verification matrix

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
