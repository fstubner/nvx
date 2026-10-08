---
title: Overview
description: What nvx is, what it deliberately does not do, and where to go next.
---

nvx runs `npm install` and `npx` inside an OS sandbox on Windows, macOS and
Linux, so a package cannot read your credentials or reach a host you did not
allow. You and your agent type the same commands. It is one binary, and it also
manages your Node.js and Bun versions.

**It contains what is installed.** `npm install` executes code from strangers
with your credentials within reach. nvx runs it inside the platform's own
sandbox. That is AppContainer on Windows, Landlock with a network namespace and
seccomp on Linux, and Seatbelt on macOS. The install gets a throwaway `HOME`,
scrubbed environment variables and an outbound allowlist. It can write to the
project and that home.

You do not change how you type anything. nvx puts shims on `PATH`, so
`npm install` is still `npm install`. The shims cover whoever runs the command:
you, your coding agent, your editor or an MCP client.

**It checks packages before they install,** as the next section describes.

**It manages runtimes.** Install, switch and pin Node.js and Bun per project, and
switch automatically on `cd` from a `.nvmrc`, `.node-version` or `package.json`.
Switching is scoped to the shell you run it in, so another terminal is unaffected
until it reads the same pin. The shims read the pin too, so an IDE task, a git
hook or CI runs the project's version without any shell setup.

## Install-time checks

Before an install runs, nvx checks what it is about to fetch.

- **Typosquats.** The names you chose, on the command line or as the project's
  direct dependencies, are compared with a list of popular packages. The npm
  download counts tell a lookalike apart from a real package with a similar
  name. Short names sit close to each other by chance, so for a name of four
  characters or fewer only one edit, two swapped letters or characters added
  around a popular name count. Packages that arrive as dependencies of others
  were named by their authors, so they skip this check and get the others.
- **Known vulnerabilities.** Direct installs, `npx`-style tool runs that fetch a
  package, and the packages in a lockfile are checked against the OSV database.
  `package-lock.json`, `pnpm-lock.yaml`, `yarn.lock` and `bun.lock` are all read.
  With no lockfile the checks use the versions `package.json` declares. A package
  OSV lists as malicious is refused, and `-y` does not change that.
- **Fresh releases.** A version published inside a configurable window, 24
  hours by default, is held for your approval. So is a version the registry
  gives no publish time for.

An npm install that brings in new packages runs npm twice. The first run only
resolves versions, contained, so each package can be checked before the second
run installs it. The second run installs the lockfile the first one wrote, so a
version published in between is not installed unchecked. `npm update`,
`npm dedupe` and an install nvx cannot pin to that lockfile, such as one that
names a version range, an alias or a git source, resolve again in the second
run. An `npm install` whose lockfile already matches `package.json`, and
`npm ci`, run npm once.

Each check has its own exemption list in the [policy file](/docs/policy/#reference).
None of them certifies a package, which is why containment is the backstop.

## Where to start

| If you want to | Go to |
| --- | --- |
| Install nvx | [Installation](/docs/install/) |
| Run nvx under an AI coding agent, CI or an MCP server | [Agents and CI](/docs/agents/) |
| Fix something nvx refused | [When nvx stops something](/docs/blocked/) |
| Undo a trust or an allowed host | [When nvx stops something](/docs/blocked/#undo-what-you-allowed) |
| Know what is contained, per platform | [Containment](/docs/containment/) |
| Write a policy file | [Policy](/docs/policy/) |
| Look up a command | [Commands](/docs/commands/) |
| See what nvx does not cover | [Known limitations](/docs/limitations/) |

## What it does not do

- **It is not a package manager.** It does not resolve dependencies or write
  lockfiles. npm, pnpm, yarn and bun still do that.
- **It does not contain your own code by default.** `npm run build`, `npm test`
  and `node` run uncontained at the `standard` isolation level, because that is
  code you wrote. `strict` extends containment to them, at the cost of breaking
  anything that needs unrestricted filesystem or network access.
- **It does not contain every read on macOS.** Write containment and egress
  control apply there, and reads under your home directory are denied. Reads
  elsewhere on the disk are allowed, because the dynamic linker must read system
  libraries whose locations vary by macOS version.
- **It does not contain an agent.** nvx contains the packages an agent installs.
  An agent with a shell of its own can still run what a person can. See
  [Agents and CI](/docs/agents/).

:::caution[Read the enforcement matrix before relying on any of this]
Guarantees differ by platform. Some rows are measured and others are read off a
generated profile. The
[enforcement matrix](https://github.com/fstubner/nvx/blob/main/docs/enforcement-matrix.md)
states which is which, and where the evidence for each column came from.
:::
