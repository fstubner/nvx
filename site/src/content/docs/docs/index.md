---
title: Overview
description: What nvx is, what it deliberately does not do, and where to go next.
---

nvx does two jobs with one binary.

**It manages runtimes.** Install, switch and pin Node.js and Bun per project, and
switch automatically on `cd` from a `.nvmrc`, `.node-version` or `package.json`.
Switching is scoped to the shell you run it in, so another terminal is unaffected
until it reads the same pin.

**It contains what those runtimes install.** `npm install` executes code from
strangers with your credentials within reach. nvx runs it inside the platform's
own sandbox — AppContainer on Windows, Landlock with a network namespace and
seccomp on Linux, Seatbelt on macOS — with a throwaway `HOME`, writes confined to
the project, scrubbed environment variables, and an outbound allowlist.

You do not change how you type anything. nvx puts shims on `PATH`, so
`npm install` is still `npm install`.

## Where to start

| If you want to | Go to |
| --- | --- |
| Install nvx | [Installation](/docs/install/) |
| Look up a command | [Commands](/docs/commands/) |

## What it does not do

- **It is not a package manager.** It does not resolve dependencies or write
  lockfiles; npm, pnpm, yarn and bun still do that.
- **It does not contain your own code by default.** `npm run build`, `npm test`
  and `node` run uncontained at the `standard` isolation level, because that is
  code you wrote. `strict` extends containment to them, at the cost of breaking
  anything that needs unrestricted filesystem or network access.
- **It does not contain reads on macOS.** Write containment and egress control
  apply there; credential reads do not, because the dynamic linker must read
  system libraries whose locations vary by macOS version.

:::caution[Read the enforcement matrix before relying on any of this]
Guarantees differ by platform, and some rows are measured while others are read
off a generated profile. The
[enforcement matrix](https://github.com/fstubner/nvx/blob/main/docs/enforcement-matrix.md)
states which is which, and where the evidence for each column came from.
:::
