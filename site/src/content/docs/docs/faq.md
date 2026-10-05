---
title: FAQ
description: How switching, networking, mixed runtimes and agent use work in practice.
---

## How does auto-swapping work alongside concurrent terminal sessions?
Traditional managers change system-wide paths or symbolic links, which can disrupt active builds running in other windows. `nvx` avoids this by configuring the paths (`PATH`, `NPM_CONFIG_PREFIX`) strictly at the **shell session level**. When you change versions in one shell (or navigate to a directory triggering auto-swap), only that shell’s environment is updated. Other concurrent processes are completely unaffected.

## How does the sandbox handle local servers, ports and networking?
Web development requires running local dev servers (e.g. listening on port `3000`) and calling external backend APIs or databases.
* **Native sandbox.** Outbound TCP goes through the nvx allowlist proxy (HTTP_PROXY / SOCKS5) under `network.mode: proxy`. Host services on `localhost` are reachable via `allow_hosts`.

  **On Windows, a dev server started inside the sandbox needs `--expose` to be reachable from your browser.** The bind succeeds and the server reports itself listening, but Windows blocks connections into an AppContainer from outside it. This is the same restriction the egress relay exists to work around, and the loopback exemption does not change it. It affects `nvx npx vite`, `npx serve` and anything else that serves a port.

  `nvx --expose 5173:8080 npx vite` publishes it. Give the port your server uses inside, then the port you want to visit. They cannot be the same number, because an AppContainer shares the host's network stack and both listeners would need the one port. `npm run dev` is uncontained at the default `standard` level anyway, and `nvx --no-sandbox npx <tool>` remains the way to opt out entirely.
* **Docker provider.** With `network.mode: open`, the container can reach host services via the standard Docker host gateway. With `offline` or `loopback` the container runs with `--network none` (no network at all). Allowlisted `proxy` mode is not supported under Docker. Use the native provider when you need per-host egress control.

## What if a project needs both Node and Bun?
`nvx use node@20` and `nvx use bun@1.2` activate independently in the same shell without evicting each other from `PATH`.
* **Native sandbox.** Other toolchains already installed on your host remain visible and run alongside nvx-managed runtimes (not sandboxed unless shimmed).
* **Docker provider.** The image is chosen from the active runtime (`node:<v>` or `oven/bun:<v>`). For a multi-language stack, supply your own image via a Dockerfile or `docker-compose`.

## Does nvx handle TypeScript and bundler commands?
Yes. Since `nvx` hooks into the active runtime context, any globally or locally installed tool (`tsc`, `ts-node`, `vite`, `webpack`) executes within the selected Node.js environment automatically.

## How does automatic command wrapping protect me when using AI coding agents?
When AI coding agents (like Gemini, Claude, or Copilot) interact with your workspace, they typically run standard commands such as `npm install <package>` or `npx <command>`. Because `nvx` automatically wraps these typical binaries inside the shell session, those commands are transparently intercepted. The packages are checked against typosquatting and vulnerability (OSV) registries, and executors run inside the native sandbox. The agent needs no special configuration or wrapper commands.

This is defense in depth that raises the bar against common supply-chain patterns (typosquats, known-vulnerable versions, install-script execution). It reduces risk substantially and is not a guarantee against a determined or novel attacker. See [SECURITY.md](https://github.com/fstubner/nvx/blob/main/SECURITY.md) for the threat model and its limits.

## What does nvx add to the time a command takes?

A command nvx does not contain, such as your own code at the default `standard`
level, pays only the shim's dispatch. Each wrapped command runs through one extra
short-lived process. Resolved binary paths are cached (keyed by `PATH`) so the
shim does not rescan `PATH` on every call. Measured dispatch overhead is **about
75 ms on Windows**. Three runs of
[`scripts/bench.py`](https://github.com/fstubner/nvx/blob/main/scripts/bench.py)
on one machine gave medians of 73.8, 74.2 and 77.1 ms.

The script runs with
isolation off and times `node -e 0` run directly against the same command through
the shim. It checks that both exit 0 and print a marker from inside the runtime
before it times anything.

A contained command costs more, because nvx prepares an isolated home and checks
permissions first. Measured on Windows 11, a project's first contained run took
about 2.4 s and every one after took about 390 ms. A second Windows 11 machine,
measured on 2026-08-29, gave 2.9 s first and 785 ms steady, the median of 8 runs.
Plan for a few hundred milliseconds to about a second.

The first run after nvx stages a new runtime copies the whole distribution and
has been measured at 45 s to 3 minutes. The
[enforcement matrix](https://github.com/fstubner/nvx/blob/main/docs/enforcement-matrix.md#measured-costs-and-platform-floors)
has the details. Linux and macOS have no established figure yet.
