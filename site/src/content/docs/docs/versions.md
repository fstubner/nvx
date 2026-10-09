---
title: Node.js and Bun versions
description: Install, switch and pin Node.js and Bun per project with nvx.
---

nvx installs Node.js and Bun, switches between them per project, and keeps each
terminal on its own version. It does the same job as nvm, fnm or volta.

## Install and switch

```sh
nvx install 22          # or lts, latest, 20.11.0
nvx install bun@1.2
nvx use 20              # this terminal only
nvx default 22          # what new terminals start on
nvx list                # what is installed
nvx uninstall 20
```

The first version you install becomes the default. `nvx use` changes only the
terminal you run it in. Other terminals keep their version.

## Pin a project

nvx reads the version a project asks for from `.nvmrc`, `.node-version`,
`.bun-version`, or `engines` and `volta` in `package.json`. Aliases such as
`lts/*`, `lts/iron` and `node` work.

With the shell integration loaded, nvx switches when you `cd` into the project.
If that version is missing, it asks whether to install it. Without the
integration, in an IDE task, a git hook or CI, the shims still run the pinned
version. They never download anything. When the pinned version is missing they
run the default and print the `nvx install` command that adds it.

## Which version runs

The first match wins.

1. Inside the sandbox, `runtime.versions` from your policy.
2. The version this terminal chose with `nvx use`.
3. The project's pin, from the current folder or the nearest one above it.
4. The default from `nvx default`.

## Move from nvm, fnm or volta

```sh
nvx import
```

It finds the Node.js versions those tools hold and downloads nvx's own copy of
each.

## Shells

The integration works in PowerShell, bash, zsh and fish. cmd.exe has no
integration, but the shims still run each project's pinned version there. To
switch one cmd window by hand, run:

```bat
FOR /f "tokens=*" %i IN ('nvx use 22 --shell=cmd') DO %i
```

## What to know

- Each download is checked against the `SHASUMS256.txt` that its release
  publishes. That catches a damaged file. The checksum comes from the same
  place as the file, so it cannot catch a compromised publisher.
- nvx downloads glibc builds, so Alpine and other musl systems are refused.
- Bun comes from GitHub. Node.js can come from a mirror you set in
  `NVX_NODE_MIRROR`. See [Configuration](/docs/policy/#private-registries-and-proxies).
- nvx does not read `devEngines.runtime`. It warns when that field asks for a
  different Node.js.
- Tools installed with `npm install -g` live with the active Node.js version, so
  switching versions switches tool sets too.
