---
title: Known limitations
description: What containment does not cover on each platform, and what the checks miss.
---

These are the places where nvx does not protect you or does not work. The
threat model is in [SECURITY.md](https://github.com/fstubner/nvx/blob/main/SECURITY.md),
and the evidence and measurements for each platform are in the
[enforcement matrix](https://github.com/fstubner/nvx/blob/main/docs/enforcement-matrix.md).

## Every platform

- **Your own code is not contained by default.** `npm run build`, `npm test`,
  `node` and a tool already in `node_modules/.bin` that `npx` or `bunx` starts run
  uncontained at the `standard` level, and so does a compromised dependency they
  import. A contained install can write the project files those
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
  up is still reachable when the allowlist names it, and `NVX_TRUST_YES` cannot
  approve it.
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
  every version, and so does a raw socket. Yarn 2 and later ignores them too. It
  reads `YARN_HTTP_PROXY` and `YARN_HTTPS_PROXY`, which nvx sets to the same
  address. Node 22.23.2 prints `[UNDICI-EHPA] Warning: EnvHttpProxyAgent is
  experimental` to stderr when a process with the variable set exits. nvx adds
  `--disable-warning=UNDICI-EHPA` to `NODE_OPTIONS` when the command it runs
  lives in a Node it installed and that Node reads the variable. A command found
  elsewhere on your `PATH`, such as a `yarn` you installed yourself, still prints
  it.
- **A request to `localhost`, `127.0.0.1` or `::1` goes to the proxy only when
  the policy lets the proxy reach this machine.** By default nvx lists those
  names in `NO_PROXY`, so a request to one connects directly. On Windows and on
  Linux the sandbox has a loopback of its own, so that reaches what runs in the
  sandbox and nothing on your machine. A server and a client in one sandbox
  reach each other. Measured on both with Node 22.23.2, `fetch` and `http.get`
  each returned 200 from a server in the same sandbox, by `127.0.0.1` and by
  `localhost`. A request to a service on your machine that no policy entry names
  fails. Measured with `fetch`, it failed with `ECONNREFUSED` on Linux and
  `ETIMEDOUT` on Windows, and nvx printed nothing, because the proxy never saw
  it.

  An `allow_hosts` or `default_allow` entry for one of those names, or
  `network.mode: loopback`, changes that. nvx leaves the names off `NO_PROXY`,
  and a request that follows the proxy variables goes to the proxy, which dials
  the service on your machine. A server and a client in one sandbox then reach
  each other only on a port nvx opened, with `--connect` or `--expose`. Measured
  on Windows and on Linux with Node 22.23.2 under `network.mode: loopback`,
  `fetch` to a server in the same sandbox was rejected and `http.get` received
  405. nvx lists the ports it opened in `NO_PROXY` by number, which Node reads
  and npm does not. Any
  other loopback port goes to the proxy, which refuses it unless the policy names
  it or the mode is `loopback`. A request with its own agent, and a raw socket,
  connect directly in every case. macOS shares your machine's loopback, so nvx
  always lists the names there.
- **The proxy refuses a plain `http://` request in proxy form.** It tunnels with
  CONNECT and speaks SOCKS5. A client that sends it a plain `http://` request
  gets `405 Method Not Allowed`. `https://` requests work. So does Node's `fetch`
  for both schemes, because it tunnels. Measured with Node 22.23.2, `http.get`
  to an `http://` address received 405. So did the npm that ships with it, on
  Windows, for an `http://` registry on this machine that `allow_hosts` named.
  Reach such a registry with `--connect`, and leave loopback entries out of the
  policy. npm matches `NO_PROXY` by host name and ignores the port, so with a
  loopback entry it sends a request to a `--connect` port to the proxy as well.
  Measured on Windows and on Linux, a contained `npm view` of a package on an
  `http://` registry opened with `--connect` returned the version under the
  default policy and failed with `E405` under a policy with a loopback entry.

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
- **git, pnpm 12 and the Next.js compiler cannot run contained.** Each asks
  Windows for the full path of a folder, and Windows refuses that request inside
  the sandbox. git stops with
  `Unable to read current working directory: Permission denied`, so a dependency
  from a git URL does not install contained and husky cannot set up its hooks
  during a contained install.
  pnpm 12 stops with `Access is denied. (os error 5)` as it reads its `--dir`
  argument, and `next build` with `failed to canonicalize jsc.baseUrl`. Run
  these with `nvx --no-sandbox`, or use pnpm 11, which runs contained.
- **A `pnpm` or `yarn` kept outside nvx's folders does not run contained.** That
  covers a standalone `pnpm.exe` and the global folder of another Node install,
  such as `%APPDATA%\npm`. nvx copies only Node and Bun installs for the sandbox,
  because a copy is readable by every sandbox, and the run is refused with a
  message saying so. Install the tool with `nvx --no-sandbox npm install -g pnpm`
  under a Node that nvx manages, or add its folder to
  `isolation.filesystem.allow_read_exec`, and nvx runs it where it is.
- **Native addons cannot be built from source contained.** node-gyp does not find
  Visual Studio from inside the sandbox, so a package with no prebuilt binary for
  your Node fails to install. Packages that download a prebuilt binary, such as
  better-sqlite3, sqlite3 and bcrypt, fetch it from `github.com` and
  `release-assets.githubusercontent.com`, and both need to be in
  `isolation.network.allow_hosts`. Build from source with `nvx --no-sandbox`.
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
- **Run as root, nvx cannot install every prebuilt binary contained.** A
  contained process runs as the user who started nvx, so for root it is root,
  in a user namespace that holds no other user. A tool that unpacks an archive
  as root gives each file the owner the archive records, and any other owner
  fails with `EINVAL`. sqlite3's prebuilt binary is one such archive. Run nvx as
  an ordinary user.
- **On Ubuntu 23.10 and later, the sandbox may refuse to start.** Ubuntu
  restricts the user namespaces it is built on, through
  `kernel.apparmor_restrict_unprivileged_userns`. Contained commands then fail
  with "Operation not permitted", and nvx does not run them uncontained instead.
  `nvx doctor` names this setting when it is the cause, and says whether `open`
  starts. `sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0` turns
  the restriction off for every program on the machine. Or set
  `isolation.network.mode` to `open`, which gives up the network namespace, so
  contained code shares your network and the egress allowlist is not enforced.
- **Before Linux 6.12, a contained process loses part of the terminal.** From
  Linux 6.12 the kernel keeps a contained process's signals inside the sandbox.
  Before that, nvx runs the process in a process group of its own, so that it
  cannot signal nvx or the processes beside it. It is stopped if it reads the
  terminal, so a contained `node` REPL does not answer. Ctrl-C still ends it,
  though a process that catches Ctrl-C, as Node and Go programs do, may need it
  more than once. Ctrl-Z stops nvx while the process runs on.
- **Before Linux 6.12, `network.mode: open` leaves your abstract UNIX sockets
  reachable.** An abstract socket has no path for the sandbox to hide, and
  `open` mode shares your network namespace. A contained process can connect to
  one that a program on your machine listens on, such as Xvfb. From Linux 6.12
  the kernel refuses the connection. The other modes never reach these sockets.

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
