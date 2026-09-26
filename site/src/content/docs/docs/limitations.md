---
title: Known limitations
description: What containment does not cover, and the behaviour that surprises people.
---

A security tool that overstates its reach is worse than one that is narrow and
honest, so the limits that change what you should expect are listed here. The
threat model behind them is in [SECURITY.md](https://github.com/fstubner/nvx/blob/main/SECURITY.md), and the per-platform
evidence each claim rests on -- probe output, dates, the machines it was measured
on -- is in [docs/enforcement-matrix.md](https://github.com/fstubner/nvx/blob/main/docs/enforcement-matrix.md).

## What containment does not cover

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

## What surprises people

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
