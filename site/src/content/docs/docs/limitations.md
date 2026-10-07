---
title: Known limitations
description: What containment does not cover on each platform, and what the checks miss.
---

These are the places where nvx does not protect you or does not work. The
threat model is in [SECURITY.md](https://github.com/fstubner/nvx/blob/main/SECURITY.md),
and the evidence and measurements for each platform are in the
[enforcement matrix](https://github.com/fstubner/nvx/blob/main/docs/enforcement-matrix.md).

## Every platform

- **Your own code is not contained by default.** `npm run build`, `npm test` and
  `node` run uncontained at the `standard` level, and so does a compromised
  dependency they import. A contained install can write the project files those
  commands run, such as `package.json` scripts, `node_modules`, lockfiles, build
  config and hook folders like `.husky`. `isolation.level: strict` contains them,
  at the cost of breaking anything that needs unrestricted filesystem or network
  access.
- **A `.env` inside the project is readable by a contained install.** The project
  directory has to be readable for the install to work, and `.env` lives in it.
  Scrubbing covers environment *variables* only.
- **Only an `http://` upstream proxy is used.** An `https://` or `socks5://`
  value in `HTTPS_PROXY` is ignored with a warning, and contained connections
  are then made directly. Behind a proxy, a host nvx's own resolver cannot look
  up is still reachable when the allowlist names it, and cannot be approved at
  the prompt.
- **The Docker provider cannot do `proxy` mode.** A policy that selects
  `isolation.filesystem.provider: docker` with the default `network.mode: proxy`
  is refused. Docker runs `offline` and `loopback` with no network at all, and
  `open` unfiltered. Use the native provider for an egress allowlist.
- **Only some Node programs follow the proxy.** nvx sets `NODE_USE_ENV_PROXY=1`
  beside `HTTP_PROXY` and `HTTPS_PROXY`. Node reads it for `fetch` from 24.0.0,
  for `http` and `https` as well from 24.5.0, and for all three from 22.21.0.
  Earlier releases ignore it, so a request from one connects directly and the
  sandbox refuses it. Bun's `fetch` follows the proxy variables with no help. A
  request that carries its own agent, such as `agent: false`, ignores them on
  every version, and so does a raw socket. Measured with Node 22.23.2, each
  contained Node process prints `[UNDICI-EHPA] Warning: EnvHttpProxyAgent is
  experimental` to stderr when it exits. Node 24.14.1 and 24.21.0 print nothing.
- **A request to `127.0.0.1` from a contained Node program goes to the proxy.**
  A request that follows the proxy variables no longer reaches a server that
  your own code started in the same sandbox. The proxy refuses a local address
  the policy does not allow. Measured on Windows and on Linux with Node 22.23.2,
  a server and a client in one sandbox could no longer reach each other.
  `fetch` was rejected and `http.get` received 405. A port that nvx opened, with
  `--connect` or `--expose`, is listed in `NO_PROXY` and still connects directly.
  Give any other request its own agent, or use a raw socket, to dial inside the
  sandbox.
- **The proxy refuses a plain `http://` request in proxy form.** It tunnels with
  CONNECT and speaks SOCKS5. A client that sends it a plain `http://` request
  gets `405 Method Not Allowed`. `https://` requests work. So does Node's `fetch`
  for both schemes, because it tunnels. Measured with Node 22.23.2, `http.get`
  to an `http://` address received 405.

## Windows

- **Your home directory is listable.** Contents stay unreadable, so `~/.ssh`,
  `~/.aws` and `~/.npmrc` cannot be read, but their presence is visible. That is
  an access rule Windows ships on your profile, and nvx cannot revoke it.
- **A loopback exemption left by an older `nvx setup` opens every service on
  127.0.0.1** to contained code, whatever the allowlist says. Current versions
  never add one. nvx warns on every affected launch, and `nvx doctor` reports
  it. Treat the allowlist as unenforced until you run `nvx setup` from an
  Administrator terminal, which removes it.
- **`bun install` works contained only in projects on the drive Windows is
  installed on.** On any other drive it fails with `EBADF`, and nvx names the
  cause. Windows refuses bun's request to turn a file handle back into a path
  inside an AppContainer, and
  [oven-sh/bun#38365](https://github.com/oven-sh/bun/pull/38365) would fix it
  in bun. Use `nvx --no-sandbox bun install`, which runs it uncontained, or use
  npm, yarn or pnpm.
- **`yarn` classic fails in a project under your user profile if you have a
  `~/.yarnrc`.** yarn reads every `.yarnrc` from the project up to the drive
  root, and treats the sandbox's refusal of the one in your real home as fatal.
  Projects outside the profile are fine. Measured with yarn 1.22.19.
- **A contained process cannot create a pipe.** nvx brokers synchronous and
  streaming capture. `child_process.fork` is refused outright, and the error
  names `--no-sandbox`.
- **A background process your own code started ends with nvx if nvx is stopped
  before the command finishes.** That happens, for example, when the program that
  started nvx exits. A command that finishes on its own leaves it running.

## macOS

- **Reads outside your credential stores are not contained.** The Seatbelt
  profile has to allow filesystem reads, because the dynamic linker loads system
  libraries whose locations move between macOS versions. It denies `~/.ssh`,
  `~/.aws`, `~/.npmrc`, the other registry and cloud credential files, and your
  keychains by path. Other files can be read, including other projects and any
  credential kept somewhere the list does not name.
- **DNS lookups are not blocked.** A contained process can still query the
  system resolver directly. Connections themselves go through the allowlist.

## Linux

- **A UNIX socket inside the project can be reached.** A contained process sees
  only the directories it is granted, so host sockets such as Docker's are
  absent. A socket placed in the project directory, or in a directory added with
  `allow_read_exec`, can still be connected to.
- **On Ubuntu 23.10 and later, the sandbox may refuse to start.** Ubuntu
  restricts the user namespaces it is built on, through
  `kernel.apparmor_restrict_unprivileged_userns`. Contained commands then fail
  with "Operation not permitted", and nvx does not run them uncontained instead.
  `nvx doctor` names this setting when it is the cause, and says whether `open`
  starts. `sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0` turns
  the restriction off for every program on the machine. Or set
  `isolation.network.mode` to `open`, which gives up the network namespace, so
  contained code shares your network and the egress allowlist is not enforced.

## Checks and registries

- **Detection is best-effort.** Typosquat and vulnerability checks reduce risk
  without certifying a package. Containment is the backstop.
- **Dependencies are checked for npm installs, and not for everything.**
  `npm install`, `npm update` and `npm dedupe` check every package npm will
  install, and `npm ci` checks every entry of `package-lock.json` for this
  platform. `npx`, `npm exec`, `npm create`, `npm init`, every pnpm, yarn and
  bun command, and npm projects that use workspaces or depend on a local folder
  are checked on the packages they name, the entries of `package-lock.json`, or
  the versions `package.json` declares. The dependencies those bring in are not
  checked, and pnpm, yarn and bun lockfiles are not read.
- **Packages from git, a URL or a local path get only the blocklist.** They are
  checked against `blocked_packages` by the name they install under. The
  typosquat, advisory and release-age checks look a package up in the registry,
  and these are not in it.
- **Packages from a registry other than the public one get no typosquat or
  advisory check.** Both ask a public service about a package by name, so nvx
  does not send them a private name. A registry that proxies the public one,
  such as an Artifactory or Nexus virtual repository, counts as another
  registry. The blocklist, release-age, install-script and lockfile checks
  still run against that registry's metadata.
- **A contained npm reads only the project's `.npmrc`.** It gets a fresh home
  and none of your `npm_config_*` variables, so a registry or scope set in
  `~/.npmrc` does not apply inside the sandbox. Your `_authToken` never reaches
  the sandbox either, so a registry that needs one for downloads cannot serve a
  contained install. Put the registry in the project's `.npmrc` and its host in
  `isolation.network.allow_hosts`.
- **yarn's `.yarnrc.yml` and bun's `bunfig.toml` do not change where the checks
  look.** nvx reads registries from `.npmrc` only.
