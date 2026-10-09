---
title: Limitations
description: What nvx does not cover today, by platform and by tool, with a workaround where one exists.
---

Where nvx does not protect you or does not work today. The detail is in
[SECURITY.md](https://github.com/fstubner/nvx/blob/main/SECURITY.md#known-limitations).

## Every platform

- **Your own code is not contained by default.** `npm run`, `npm test`, `node`
  and tools in `node_modules/.bin` run uncontained, and an install can change
  the files they run. Set `isolation.level` to `strict`.
- **An agent with its own shell can do what you can.** It can run `nvx trust`
  or `nvx --no-sandbox`. Deny those in the agent's permissions, as in
  [AI agents and MCP](/docs/agents/).
- **A `.env` created during a run can be read for a few milliseconds.**
  `.envrc` and names such as `production.env` are never hidden.
- **Browsers that puppeteer or Playwright download are deleted with the
  sandbox.** Run `nvx --no-sandbox npx playwright install`.
- **A folder outside the project is out of reach**, so `npm install ../shared`
  fails with `EPERM`. Add the folder to `isolation.filesystem.allow_read_exec`.
- **Some Node programs ignore the proxy and are refused.** That covers Node 20,
  Node 22 before 22.21.0, requests with their own agent, and raw sockets.
- **A plain `http://` request through the proxy gets `405`.** `https://` works.
  Reach a local `http://` registry with `--connect`.
- **The Docker provider has no allowlist.** It refuses `proxy` mode. Use the
  native provider.
- **nvx does not read `devEngines.runtime`.** Pin the version in `.nvmrc` or
  `engines`.

## Checks

- **Some commands check only what they name.** `npx`, `npm create`,
  `pnpm add <pkg>`, `bun.lockb` projects and npm workspaces check the named
  packages or the lockfile, and not what those bring in.
- **Packages from git, a URL or a path get only the blocklist.**
- **Private registries get no typosquat or advisory check.** That includes
  Artifactory or Nexus proxies of the public registry.
- **A contained npm reads only the project's `.npmrc` and gets no token**, so a
  registry that needs a token for downloads cannot serve a contained install.
- **`.yarnrc.yml` and `bunfig.toml` registries are not read.** The checks use
  `.npmrc`.

## Windows

- **Your home folder is listable.** Names are visible and contents are not.
- **`bun install` works contained only on the drive Windows is installed on.**
  Elsewhere it fails with `EBADF`. Use `nvx --no-sandbox bun install`, or npm.
- **git, pnpm 12 and `next build` cannot run contained.** So a git dependency
  and husky's setup fail during a contained install. Use `nvx --no-sandbox`, or
  `npm install -g pnpm@11`.
- **pnpm 9 to 11 crash when an install has a package with install scripts.**
  Use `nvx --no-sandbox pnpm install`, or npm.
- **A `pnpm` or `yarn` installed outside nvx is refused.** Install it under a
  Node.js nvx manages, with `nvx --no-sandbox npm install -g pnpm@11`.
- **`curl.exe` fails with `CRYPT_E_REVOCATION_OFFLINE`.** Use
  `curl --ssl-no-revoke`.
- **Native addons cannot build from source.** Prebuilt binaries need
  `github.com` and `release-assets.githubusercontent.com` in `allow_hosts`. To
  build, use `nvx --no-sandbox`.
- **`child_process.fork` is refused** inside the sandbox.
- **A loopback exemption from an old `nvx setup` opens every local service.**
  `nvx doctor` reports it. Run `nvx setup` as Administrator to remove it.

## macOS

- **Files outside your home folder stay readable**, because the dynamic linker
  needs system libraries whose locations change between macOS versions.
- **A Node.js that another tool installed in your home cannot run contained.**
  Add its folder to `allow_read_exec`.
- **Whether a contained server can listen has not been measured.** `--expose`
  does nothing on macOS.

## Linux

- **On Ubuntu 23.10 and later the sandbox may not start.** `nvx doctor` names
  the AppArmor setting and the ways forward.
- **A UNIX socket inside the project can be reached.**
- **Run as root, some prebuilt binaries fail with `EINVAL`.** Run nvx as an
  ordinary user.
- **Before kernel 6.12, a contained REPL does not answer**, and Ctrl-C may be
  needed more than once.
- **Before kernel 6.12, `network.mode: open` leaves abstract UNIX sockets
  reachable.** The other modes do not.
