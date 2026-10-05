---
title: Known limitations
description: What containment does not cover on each platform, what the checks miss, and behaviour to expect.
---

These are the limits that change what you should expect from nvx. The threat
model is in [SECURITY.md](https://github.com/fstubner/nvx/blob/main/SECURITY.md),
and the evidence for each platform is in the
[enforcement matrix](https://github.com/fstubner/nvx/blob/main/docs/enforcement-matrix.md).

## Every platform

- **Your own code is not contained by default.** `npm run build`, `npm test` and
  `node` run uncontained at the `standard` level, so a compromised dependency your
  own code imports is not sandboxed. A contained install can therefore influence a
  later uncontained command, because the project's own files are writable by
  design. Those include `package.json` and its scripts, `node_modules`, lockfiles, build config,
  and hook folders kept in the project such as `.husky`. The one exception is
  `.git`, which contained runs can read and cannot write.
  `isolation.level: strict` contains those commands, at the cost of breaking
  anything that needs unrestricted filesystem or network access.
- **A `.env` inside the project is readable by a contained install.** The project
  directory has to be readable for the install to work, and `.env` lives in it.
  Scrubbing covers environment *variables* only, so the file stays readable.
- **Only an `http://` upstream proxy is used.** An `https://` or `socks5://`
  value in `HTTPS_PROXY` is ignored with a warning, and contained connections
  are then made directly. Behind a proxy, a host nvx's own resolver cannot look
  up is still reachable when the allowlist names it. Such a host cannot be
  approved at the prompt.
- **The Docker provider cannot do `proxy` mode.** A policy that selects
  `isolation.filesystem.provider: docker` with the default `network.mode: proxy`
  is refused. Docker runs `offline` and `loopback` with no network at all, and
  `open` unfiltered. Use the native provider for an egress allowlist.

## Windows

- **Your home directory is listable.** Contents stay unreadable, so `~/.ssh`,
  `~/.aws` and `~/.npmrc` cannot be read, but their presence is visible. That is
  an access rule Windows ships on your profile, and nvx cannot revoke it.
- **A loopback exemption left by an older `nvx setup` opens every service on
  127.0.0.1** to contained code, whatever the allowlist says. Current versions
  never add one. Removing it needs an Administrator terminal, so on an upgraded
  machine it stays until you run `nvx setup` there. nvx warns on every affected
  launch, and `nvx doctor` reports it with the removal command. Treat the
  allowlist as unenforced while it is registered.
- **`bun install` works contained only in projects on the drive Windows is
  installed on.** On any other drive it fails with `EBADF`, and nvx names the
  cause. Use `nvx --no-sandbox bun install`, which runs it uncontained, or use
  npm, yarn or pnpm. Windows refuses bun's request to turn a file handle back
  into a path inside an AppContainer.
  [oven-sh/bun#38365](https://github.com/oven-sh/bun/pull/38365) would fix it
  in bun.
- **`yarn` classic fails in a project under your user profile if you have a
  `~/.yarnrc`.** yarn reads every `.yarnrc` on the way up from the project to the
  drive root. The sandbox refuses the one in your real home, and yarn treats that
  refusal as fatal. Projects outside the profile are fine. Measured with yarn
  1.22.19.
- **A contained server needs `--expose` to be reachable from your machine.**
  Windows refuses connections into an AppContainer from outside it.
- **A contained process cannot create a pipe.** nvx brokers synchronous and
  streaming capture. `child_process.fork` is refused outright, and the error
  names `--no-sandbox`.
- **A background process your own code started ends with nvx if nvx is stopped
  before the command finishes.** That happens, for example, when the program that
  started nvx exits. A command that finishes on its own leaves it running.

## macOS

- **Reads outside your credential stores are not contained.** The Seatbelt
  profile has to allow filesystem reads, because the dynamic linker loads system
  libraries whose locations move between macOS versions. It denies the credential
  stores by path, so `~/.ssh`, `~/.aws`, `~/.npmrc`, the other registry and cloud
  credential files, and your keychains cannot be read. Other files can, including
  other projects and any credential kept somewhere the list does not name.
- **The system temp folders are writable.** Besides the project and its
  throwaway home, the profile allows writes under `/dev`, `/private/tmp`,
  `/private/var/tmp` and `/private/var/folders`, which holds your `$TMPDIR`. A
  contained install can leave or change files there that your own programs later
  read.
- **DNS lookups are not blocked.** A contained process can still query the
  system resolver directly. Connections themselves go through the allowlist.

## Linux

- **A UNIX socket inside the project can be reached.** A contained process sees
  only the directories it is granted, so host sockets such as Docker's are
  absent. A socket placed in the project directory, or in a directory added with
  `allow_read_exec`, can still be connected to.
- **On Ubuntu 23.10 and later, the sandbox may refuse to start.** Ubuntu
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

## Checks and registries

- **Detection is best-effort.** Typosquat and vulnerability checks reduce risk
  without certifying a package. Containment is the backstop.
- **Dependencies are checked for npm installs, and not for everything.** For
  `npm install`, `npm update` and `npm dedupe`, nvx asks npm which packages it
  will install and checks all of them. `npm ci` checks every entry of
  `package-lock.json` for this platform. The other commands are checked on the
  packages they name, the entries of `package-lock.json`, or the versions
  `package.json` declares. The dependencies those bring in are not checked.
  That is `npx`, `npm exec`, `npm create` and `npm init`, and every pnpm, yarn
  and bun command. The same goes for npm projects that use workspaces or depend
  on a local folder.
  pnpm, yarn and bun lockfiles are not read.
- **Packages from git, a URL or a local path get only the blocklist.** They are
  checked against `blocked_packages` by the name they install under. The
  typosquat, advisory and release-age checks look a package up in the registry,
  and these are not in it.
- **Packages from a registry other than the public one get no typosquat or
  advisory check.** Both ask a public service about a package by name, so nvx
  does not send them a private name. A registry that proxies the public one,
  such as an Artifactory or Nexus virtual repository, counts as another
  registry. These two checks do not run for anything it serves. The
  blocklist, release-age, install-script and lockfile checks still run against
  that registry's metadata. Each run that skips them says so once.
- **A contained npm reads only the project's `.npmrc`.** It gets a fresh home
  and none of your `npm_config_*` variables. So a registry or scope set in
  `~/.npmrc` does not apply inside the sandbox. nvx checks those packages
  against the registry the contained npm will actually use. Put the registry in
  the project's `.npmrc` and its host in `isolation.network.allow_hosts`. Your
  `_authToken` never reaches the sandbox either, so a registry that needs one
  for downloads cannot serve a contained install. nvx reads registries from
  `.npmrc` only, so yarn's `.yarnrc.yml` and bun's `bunfig.toml` settings do not
  change where the checks look.

## Behaviour to expect

- **The first contained run in a project takes seconds.** Later ones take a few
  hundred milliseconds.
- **An npm install that brings in new packages runs npm twice.** The first run
  only resolves versions, contained, so each package can be checked before the
  second run installs it. An `npm install` whose lockfile already matches
  `package.json`, and `npm ci`, run npm once.
- **A package published in the last 24 hours is held** for your approval. An
  MCP server launched by an editor cannot prompt, so it fails to start.
- **A contained command sees almost none of your environment.** A tool reading
  `CI` or `NODE_ENV` changes behaviour without erroring. nvx names the variables
  it drops, and `isolation.environment.allow` keeps the ones a project needs.
- **`npm install -g` is refused** inside the sandbox, because a global install
  writes outside the project. `nvx --no-sandbox npm install -g` is an uncontained
  install, so treat it as one.
- **Git hook installers cannot set themselves up during a contained install.**
  husky's `prepare` script, simple-git-hooks and lefthook write to `.git`, which
  a contained install cannot write. Run their setup yourself afterwards, for
  example `npx husky`, or run the install with `nvx --no-sandbox`.
- **A contained tool cannot reach a service you are already running until you
  allow it.** Use `--connect` for one run, `allow_hosts` for a tool that uses the
  proxy, or `network.mode: loopback`, which [Policy](/docs/policy/#reference)
  describes.
- **A contained command started in your home directory, or above it, starts in
  the sandbox's home instead.** Granting your home would grant everything in it,
  `~/.nvx` and your shell profile included. nvx says so when it happens. Run the
  command from a project folder.
- **A stray `package.json` above your projects merges them into one sandbox
  scope.** `nvx doctor` reports it when the manifest sits in your home directory
  or at a volume root.
- **A long `NVX_HOME` can stop contained runs.** The sandbox reaches nvx through
  sockets under `NVX_HOME`, and a socket path must be shorter than 108 bytes. On
  Linux a run that needs one refuses and names the longest `NVX_HOME` that works.
  On Windows nvx moves the sockets to the sandbox's own folder in
  `%LOCALAPPDATA%\Packages`, and refuses only when that path is too long as well.
