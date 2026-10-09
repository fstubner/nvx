---
title: How it works
description: How nvx intercepts commands, what it checks, and what a contained command can reach on each platform.
---

nvx sits in front of your package manager, checks what an install is about to
fetch, and runs the install in the operating system's own sandbox. The threat
model is in [SECURITY.md](https://github.com/fstubner/nvx/blob/main/SECURITY.md),
and the evidence for each claim is in the
[enforcement matrix](https://github.com/fstubner/nvx/blob/main/docs/enforcement-matrix.md).

## Shims

`~/.nvx/bin` holds small programs named `node`, `npm`, `npx`, `pnpm`, `yarn`,
`corepack`, `bun` and `bunx`. They sit first on your `PATH`, so every one of
those commands goes through nvx. That holds whether you, a coding agent, your
editor or an MCP client types it.

## What gets contained

nvx contains code you did not write:

- installs and updates, such as `npm install`, `npm ci`, `pnpm install`,
  `yarn` and `bun install`
- tool runs that fetch a package, such as `npx create-vite` or `bunx cowsay`

Your own code runs uncontained at the default `standard` level. That covers
`npm run`, `npm test`, `node server.js`, and a tool already in
`node_modules/.bin`, such as `npx vitest`. Set `isolation.level` to `strict` to
contain those too. `nvx --strict <command>` does it for one run.

## The checks

Before an install runs, nvx checks the packages it will bring in.

- **Known advisories.** Each version from the public npm registry is looked up
  in the OSV database. A package OSV lists as malicious is refused.
- **Fresh releases.** A version published in the last 24 hours waits for your
  approval.
- **Lookalike names.** A package you named, on the command line or in
  `package.json`, that is a near miss of a popular one waits for your approval.
- **Install scripts.** A package that runs code at install time waits for your
  approval.
- **Your blocklist.** Names in `blocked_packages` are refused.

nvx reads `package-lock.json`, `pnpm-lock.yaml`, `yarn.lock` and `bun.lock`.
At a terminal nvx asks, and with nobody to answer it refuses. The checks lower
the risk and cannot prove a package safe, so the sandbox is the backstop.

## The sandbox

| | Windows | Linux | macOS |
| --- | --- | --- | --- |
| Built on | AppContainer | Landlock, namespaces and seccomp | Seatbelt |
| Reads in your home folder | the project and nvx's runtimes | the project and nvx's runtimes | the project and nvx's runtimes |
| Reads elsewhere on disk | restricted | restricted | allowed |
| Can write | the project and a throwaway home | the project and a throwaway home | the project and a throwaway home |
| Network | only through nvx's allowlist | only through nvx's allowlist | only through nvx's allowlist |

If the sandbox cannot start, nvx does not run the command.

## The network allowlist

A contained command reaches the internet only through a proxy that nvx runs.
The proxy lets through the hosts on the allowlist and refuses the rest. By
default that is the npm registry, Yarn's download hosts, and the OSV advisory
API. Bun adds GitHub's download hosts. To add a host, see
[Configuration](/docs/policy/#allow-a-host).

A contained server is not reachable from your machine by default. On Windows and
Linux, `nvx --expose 5173:8080 npx vite` publishes the sandbox's port 5173 at
`http://127.0.0.1:8080`. To let a contained command reach a service on your
machine, use `nvx --connect <port>` for one run.

## What is hidden

- **Your home folder.** The command gets a home folder of its own, so `~/.ssh`,
  `~/.aws` and `~/.npmrc` are out of reach.
- **Your environment.** Variables such as `AWS_*`, `GITHUB_*` and `SSH_*` are
  dropped, and so is almost everything else. `isolation.environment.allow` keeps
  the ones a project needs.
- **`.env` files.** The project's `.env` and `.env.*` cannot be read, except
  templates such as `.env.example`.
- **`.git`.** The command can read it and cannot write it, so an install cannot
  plant a git hook.

A command started in your home folder, or above it, runs in a temporary folder
instead. Run commands from a project folder.
