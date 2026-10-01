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
- **Dependencies are checked for npm installs, and not for everything.** For
  `npm install`, `npm update` and `npm dedupe`, nvx asks npm which packages it
  will install and checks all of them. `npm ci` checks every entry of
  `package-lock.json` for this platform. The other commands are checked on the packages they name,
  the entries of `package-lock.json`, or the versions `package.json` declares,
  and the dependencies those bring in are not checked. That is `npx`, `npm
  exec`, `npm create` and `npm init`, every pnpm, yarn and bun command, and npm
  projects that use workspaces or depend on a local folder. pnpm, yarn and bun
  lockfiles are not read.
- **Packages from git, a URL or a local path get only the blocklist.** They are
  checked against `blocked_packages` by the name they install under. The
  typosquat, advisory and release-age checks look a package up in the registry,
  and these are not in it.
- **Packages from a registry other than the public one get no typosquat or
  advisory check.** Both ask a public service about a package by name, so nvx
  does not send them a private name. A registry that proxies the public one,
  such as an Artifactory or Nexus virtual repository, counts as another
  registry, so these two checks do not run for anything it serves. The
  blocklist, release-age, install-script and lockfile checks still run against
  that registry's metadata. Each run that skips them says so once.
- **A contained npm reads only the project's `.npmrc`.** It gets a fresh home
  and none of your `npm_config_*` variables, so a registry or scope set in
  `~/.npmrc` does not apply inside the sandbox, and nvx checks those packages
  against the registry the contained npm will actually use. Put the registry in
  the project's `.npmrc` and its host in `isolation.network.allow_hosts`. Your
  `_authToken` never reaches the sandbox either, so a registry that needs one
  for downloads cannot serve a contained install. nvx reads registries from `.npmrc`
  only, so yarn's `.yarnrc.yml` and bun's `bunfig.toml` settings do not change
  where the checks look.
- **Only an `http://` upstream proxy is used.** An `https://` or `socks5://`
  value in `HTTPS_PROXY` is ignored with a warning, and contained connections
  are then made directly. Behind a proxy, a host nvx's own resolver cannot look
  up is still reachable when the allowlist names it, but it cannot be approved
  at the prompt.
- **On Windows, a loopback exemption left by an `nvx setup` older than 0.5.0
  opens every service on 127.0.0.1** to contained code, whatever the allowlist
  says. Newer versions never add one. Removing it needs an Administrator
  terminal, so on an upgraded machine it stays until you run `nvx setup` there.
  nvx warns on every affected launch and `nvx doctor` reports it with the
  removal command. Treat the allowlist as unenforced while it is registered.
- **The Docker provider cannot do `proxy` mode.** A policy that selects
  `isolation.filesystem.provider: docker` with the default `network.mode: proxy`
  is refused. Docker runs `offline` and `loopback` with no network at all, and
  `open` unfiltered. Use the native provider for an egress allowlist.

## What surprises people

- **An npm install that brings in new packages runs npm twice.** The first run
  only resolves versions, contained, so that each package can be checked before
  the second run installs it. An `npm install` whose lockfile already matches
  `package.json` and `npm ci` run npm once.

- **On Ubuntu 23.10 and later, the Linux sandbox may refuse to start.** Ubuntu
  restricts the user namespaces the sandbox is built on, through the setting
  `kernel.apparmor_restrict_unprivileged_userns`. Contained commands then fail
  with "Operation not permitted", and nvx does not run them uncontained instead.
  `nvx doctor` starts a contained process and names this setting when it is the
  cause. You have two ways forward. `sudo sysctl -w
  kernel.apparmor_restrict_unprivileged_userns=0` turns the restriction off for
  every program on the machine. Or set `isolation.network.mode` to `open`, which
  gives up the network namespace, so contained code shares your network and the
  egress allowlist is not enforced. `nvx doctor` says whether `open` starts on
  your machine.
- **A contained command started in your home directory, or above it, starts in
  the sandbox's home instead.** The working directory is writable inside the
  sandbox, and granting your home would grant everything in it, `~/.nvx` and
  your shell profile included. nvx says so when it happens. Run the command from
  a project folder to work on files there.
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
- **On Windows, a background process your own code started ends with nvx if nvx
  is stopped before the command finishes**, for example when the program that
  started nvx exits. A command that finishes on its own leaves it running.
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
