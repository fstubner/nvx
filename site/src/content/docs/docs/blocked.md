---
title: When something is blocked
description: Each message nvx prints when it stops something, what it means, and the one command or policy line that fixes it.
---

Find the message you saw below. Each section says what it means, how to allow
it, and how to undo that later.

Run fixes in your own terminal, in the project folder. Policy lines go in
`~/.nvx/policy.json`. Create the file with the line shown, or merge the line
into the file you have. `nvx policy init --global` writes a starting file.

The package checks below ask first when you are at a terminal. With nobody to
answer, as in CI, nvx prints `Non-interactive environment: denying prompt` and
refuses.

If you are a coding agent, do not run these fixes. Tell the person you work for
what was blocked and quote nvx's message.

## Exit code 77

Every refusal exits with `77`. The command did not run, or nvx refused a
connection while it ran. Running it again gives the same answer. The fix is in
the last lines nvx printed.

## Blocked egress: host:443

```text
⚠ Blocked egress: example.org:443
ℹ To allow it, run this in your own terminal, in C:\work\app:  nvx allow-host example.org:443
```

A contained command tried to reach a host that is not on the allowlist. nvx did
not connect.

- **Allow it:** `nvx allow-host example.org:443`. This adds the host to the
  project's `.nvx-policy.json` and trusts that file. Add `--global` to write
  `~/.nvx/policy.json` instead.
- **Undo:** `nvx allow-host --remove example.org:443`.

A service on your own machine, or an address such as `169.254.169.254`, has no
one-line fix. Use `nvx --connect <port>` for one run, or name it in
`allow_hosts`. See [Configuration](/docs/policy/).

## Refused to run under project policy

```text
nvx refused to run under project policy /work/app/.nvx-policy.json. It loosens nvx's security settings, and it has not been trusted
```

The repository's `.nvx-policy.json` widens the sandbox, for example by adding a
host. Every command in the project exits 77 until you trust it. Read the file
first.

- **Allow it:** run the `nvx trust .nvx-policy.json --hash <hash>` line the
  message prints. The hash means nvx trusts only the content you saw.
- **Undo:** `nvx grants reset`.

A tool that asks to keep a persistent profile is refused the same way. Allow it
with `nvx trust --tool <name>`.

## Published only N hours ago

```text
Package pkg@1.2.3 was published only 3.0 hours ago (on ...). Supply chain compromises are often caught within 24 hours. Proceed?
```

The version is newer than the cooling-off window, 24 hours by default. A
version the registry gives no publish time for is treated the same way.

- **Allow it, narrowest first:** install an older version, `npm install
  pkg@1.2.2` (`npm view pkg time` lists publish dates). Or exempt the package.

  ```json
  { "release_age": { "trusted_packages": ["pkg"] } }
  ```

- **Undo:** delete the line.

## Package is suspiciously close to popular package

The name is a near miss of a popular package and has far fewer downloads. Check
the spelling first.

- **Allow it:** `{ "typosquatting": { "trusted_packages": ["expresss"] } }`
- **Undo:** delete the line.

## Vulnerability Scan Alert, or OSV lists it as malicious

An OSV advisory covers a version in the install. A `MAL-` advisory means the
package is malicious. It is refused without asking, and `-y` does not change
that.

- **Allow an advisory you have assessed:**
  `{ "vulnerabilities": { "allowed_advisories": ["GHSA-xxxx-yyyy-zzzz"] } }`.
  A `MAL-` advisory needs its exact ID. A pattern such as `"MAL-*"` does not work.
- **Allow everything below a severity:** set `vulnerabilities.min_severity` to
  `low`, `moderate`, `high` or `critical`.
- **Undo:** delete the line.

## Package contains installation scripts

The package runs code at install time. The scripts would still run inside the
sandbox.

- **Allow it:** `{ "install_scripts": { "trusted_packages": ["esbuild"] } }`.
  To install without scripts, pass `--ignore-scripts`.
- **Undo:** delete the line.

## Blocked by security policy: blacklisted

The name is in `blocked_packages`. `npm ls <name>` shows which dependency pulled
it in. `nvx policy explain` shows which policy file set it. Remove the name
there to allow it.

## A lookup that failed

The registry or `api.osv.dev` could not be reached, so a check could not run.
Retry when it is reachable. `nvx -y` approves every check in that one run.

## The sandbox could not start

```text
nvx could not create the sandbox to contain this command, so it did not run.
```

Most often on Linux, where the kernel refused the sandbox its namespaces. On
Ubuntu 23.10 and later the cause is AppArmor.

- **Fix:** run `nvx doctor`. It names the cause and the ways forward.
- **For one command:** `nvx --no-sandbox <command>` runs it uncontained.

## A command outside nvx's folders

```text
AppContainer executable access failed: C:\Users\you\AppData\Local\pnpm\pnpm.exe is not in a Node or Bun install
```

On Windows, a `pnpm` or `yarn` installed outside nvx cannot run in the sandbox.

- **Fix:** install it under a Node.js nvx manages, with
  `nvx --no-sandbox npm install -g pnpm@11`. Or add the folder the message names to
  `isolation.filesystem.allow_read_exec`.
- **Undo:** remove the folder from the policy. `nvx grants reset` withdraws
  the permission straight away.

## Wrote files to a temporary folder

```text
npm wrote package.json to its working folder, which was a temporary folder inside the sandbox. nvx has deleted it.
```

You ran the command from your home folder, a folder above it, or nvx's own
folder. The sandbox may not write there. Run it from a project folder.

## Global installs (-g) can't run inside the sandbox

A global install writes outside the project. Use
`nvx --no-sandbox npm install -g <package>`, knowing it runs uncontained. For a
tool you only want to run, `npx <package>` is contained.

## -y was passed to npm, not to nvx

nvx reads its own flags only before the command. Write `nvx -y npm install ...`.

## --agent-mode is set, so nvx refuses instead of asking

Agent mode turns every question into a refusal, and `-y` does not override it.
Add the policy line the message prints. See [AI agents and MCP](/docs/agents/).

## Everything you allowed

`nvx grants list` shows what is trusted for the current project.
`nvx grants reset --all` forgets it for every project.
