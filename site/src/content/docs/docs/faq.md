---
title: FAQ
description: How switching, networking, mixed runtimes and agent use work in practice.
---

## How does auto-swapping work alongside concurrent terminal sessions?
Traditional managers change system-wide paths or symbolic links, which can disrupt active builds running in other windows. `nvx` avoids this by configuring the paths (`PATH`, `NPM_CONFIG_PREFIX`) strictly at the **shell session level**. When you change versions in one shell (or navigate to a directory triggering auto-swap), only that shell’s environment is updated. Other concurrent processes are completely unaffected.

## How do sandboxed containers handle local servers, ports, and networking?
Web development requires running local dev servers (e.g. listening on port `3000`) and calling external backend APIs or databases:
* **Native Sandbox**: outbound TCP goes through the nvx allowlist proxy (HTTP_PROXY / SOCKS5) under `network.mode: proxy`, and host services on `localhost` are reachable via `allow_hosts`.

  **On Windows, a dev server started inside the sandbox needs `--expose` to be reachable from your browser.** The bind succeeds and the server reports itself listening, but Windows blocks connections into an AppContainer from outside it — the same restriction the egress relay exists to work around, and unaffected by the loopback exemption. This affects `nvx npx vite`, `npx serve` and anything else that serves a port. Since 0.5.5, `nvx --expose 5173:8080 npx vite` publishes it: give the port your server uses inside, then the port you want to visit (they cannot be the same number — see [Known limitations](/docs/limitations/)). `npm run dev` is uncontained at the default `standard` level anyway, and `nvx --no-sandbox npx <tool>` remains the way to opt out entirely. This entry claimed dev servers worked, with no platform qualifier, until 2026-08-20, and still said they were simply unreachable until 0.5.5 shipped the fix.
* **Docker Sandbox**: With `network.mode: open`, the container can reach host services via the standard Docker host gateway. With `offline`/`loopback` the container runs with `--network none` (no network at all). Allowlisted `proxy` mode is not supported under Docker — use the native provider when you need per-host egress control.

## What if a project needs both Node and Bun?
`nvx use node@20` and `nvx use bun@1.2` activate independently in the same shell without evicting each other from `PATH`.
* **Native Sandbox**: Other toolchains already installed on your host remain visible and run alongside nvx-managed runtimes (not sandboxed unless shimmed).
* **Docker Sandbox**: The image is chosen from the active runtime (`node:<v>` or `oven/bun:<v>`). For a multi-language stack, supply your own image via a Dockerfile or `docker-compose`.

## Does nvx handle TypeScript and bundler commands?
Yes. Since `nvx` hooks into the active runtime context, any globally or locally installed tool (`tsc`, `ts-node`, `vite`, `webpack`) executes within the selected Node.js environment automatically.

## How does automatic command wrapping protect me when using AI coding agents?
When AI coding agents (like Gemini, Claude, or Copilot) interact with your workspace, they typically run standard commands such as `npm install <package>` or `npx <command>`. Because `nvx` automatically wraps these typical binaries inside the shell session, those commands are transparently intercepted. The packages are checked against typosquatting and vulnerability (OSV) registries, and executors run inside the native sandbox, with no special configuration or wrapper commands required from the agent. This is defense-in-depth that raises the bar against common supply-chain patterns (typosquats, known-vulnerable versions, install-script execution); it reduces risk substantially but is not a guarantee against a determined or novel attacker. See [SECURITY.md](https://github.com/fstubner/nvx/blob/main/SECURITY.md) for the threat model and its limits.

## How much overhead does a shim add?

A single static Go binary with no runtime dependencies. Each wrapped command runs through one extra short-lived process; resolved binary paths are cached (keyed by `PATH`) so the shim doesn't rescan `PATH` on every call. Measured dispatch overhead: **about 75 ms on Windows**, and not currently established on Linux or macOS. Every figure this line carried before 2026-09-03 has been withdrawn: [`scripts/bench.py`](https://github.com/fstubner/nvx/blob/main/scripts/bench.py) invoked the shim as `nvx shim node --no-sandbox`, and nvx deliberately passes a wrapped command's own arguments through untouched, so `--no-sandbox` went to node, which answered `bad option` and exited 9. The wrapped half of every published measurement was timing an argument-parsing failure, not a run. The script now verifies both halves exit 0 and print a marker from inside the runtime before it times anything. The Windows figure is three runs on one machine (median 73.8, 74.2, 77.1 ms, p10–p90 roughly 63–93); a fourth run on a busy machine was refused by the script's own spread check rather than published. In a Linux container the median came out at 4–10 ms but the spread swamped it and the script declined to give a figure; macOS has no measurement at all. Still imperceptible next to the commands you actually wait on, like `npm install`, but do not quote a number this file does not.
