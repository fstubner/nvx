---
title: Get started
description: Install nvx, run your first contained npm install, and check that everything works.
---

nvx runs `npm install` and `npx` inside an OS sandbox on Windows, macOS and
Linux. A package you install cannot read your credentials or reach a host you
did not allow. You keep typing the same commands. nvx also manages your Node.js
and Bun versions.

## 1. Install nvx

On Windows, in PowerShell:

```powershell
irm https://nvx.run/install.ps1 | iex
```

On macOS or Linux:

```sh
curl -fsSL https://nvx.run/install.sh | sh
```

On Linux the sandbox needs kernel 5.13 or later with Landlock, unprivileged
user namespaces, and the `ip` command from iproute2. [Install, upgrade and
uninstall](/docs/install/) has the other routes and what the installer changes.

## 2. Open a new terminal

The installer puts `~/.nvx/bin` at the front of your `PATH`. A terminal that was
already open does not see that change.

## 3. Run an install

In a project folder, install something the way you always do.

```text
> npm install sample-package
ℹ Running in native sandbox: npm install sample-package@1.0.1

added 1 package, and audited 2 packages in 2s

found 0 vulnerabilities
```

Before the install you see, nvx runs npm once in the sandbox to work out which
packages it brings in, and checks each of them. The install then gets exactly
what was checked.

If you have no Node.js yet, `npm` tells you to run `nvx install lts` first.

nvx asks before it installs a package that looks like a misspelling of a popular
one, was published in the last 24 hours, has a known advisory, or runs install
scripts. Some things it refuses outright and prints the fix.
[When something is blocked](/docs/blocked/) covers each message.

## 4. Check your setup

```sh
nvx doctor
```

It checks that `node`, `npm` and `npx` go through nvx and that the sandbox
starts.

```text
  [OK]   shim dir is on PATH at position 0, with no raw-runtime dir ahead of it
  [OK]   the sandbox starts (AppContainer launch succeeded)
```

It exits non-zero when something needs your attention. `nvx doctor --fix`
repairs what it safely can.

## 5. Optional: let nvx manage Node.js

```sh
nvx install 22
```

The first version you install becomes the default. nvx then switches version
when you `cd` into a project that pins one. See [Node.js and Bun
versions](/docs/versions/).

## Next

- Using a coding agent or an MCP server? Read [AI agents and MCP](/docs/agents/).
- Need a private registry or another host? See [Configuration](/docs/policy/).
- Want to know what the sandbox does? See [How it works](/docs/containment/)
  and [Limitations](/docs/limitations/).
