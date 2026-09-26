---
title: Commands
description: The nvx command surface, grouped by what you are trying to do.
---

Run `nvx help <command>` for any of these.

## Runtimes

<div data-ui-table="row-headers"></div>

| Command | What it does |
| --- | --- |
| `nvx install <version>` | Install a Node.js or Bun version. Accepts `22`, `lts`, `latest` or a range. The version is required. `nvx auto` is what reads a `.nvmrc`. |
| `nvx use <version>` | Switch this shell to a version. |
| `nvx default <version>` | Set the version new shells start on. |
| `nvx list` | Installed versions. `nvx list-remote` lists what is available. |
| `nvx uninstall <version>` | Remove an installed version. |
| `nvx auto` | Switch to the version this directory pins. |

## Running things

<div data-ui-table="row-headers"></div>

| Command | What it does |
| --- | --- |
| `nvx shim <command>` | Run a command through nvx explicitly, contained if it runs code you did not write. |
| `nvx --strict <command>` | Contain the command even when it is your own code. |
| `nvx --standard <command>` | Drop back to the default level for one run. Never uncontains an install. |
| `nvx --no-sandbox <command>` | Run uncontained, for the cases nvx refuses by design — a global install, for one. |
| `nvx --connect <port>` | Let one contained run reach one service already running on your machine. |

## Checking and policy

<div data-ui-table="row-headers"></div>

| Command | What it does |
| --- | --- |
| `nvx doctor` | Whether interception, the shell integration and containment are healthy. `--fix` repairs what it can. |
| `nvx policy init` | Write a global or project policy file. |
| `nvx audit` | What nvx recorded: runs, blocked hosts, prompts and how they were answered. |
| `nvx grants list` | This project's approved egress hosts, trusted tools, and policy pins. |
| `nvx grants reset [--all]` | Forget this project's grants, or every project's with `--all`. |
| `nvx env` | Print the shell integration snippet. |
| `nvx report` | A diagnostic bundle to attach to a bug report. |

## Policy files

A project can carry a `.nvx-policy.json`, and nvx merges it over the global one.

```json
{
  "isolation": {
    "level": "standard",
    "network": {
      "default_allow": ["registry.example.com:443"]
    }
  }
}
```

:::caution[A policy that widens the sandbox needs your approval]
A project file lives in a repository, so one line in a pull request could
otherwise hand a contained install a new destination. nvx refuses to honour a
widening policy until it is trusted for that project, and `-y`, `--agent-mode`
and `NVX_YES` deliberately do not count — an agent will answer yes to anything.
Set `NVX_TRUST_YES=true` only when you have read what you are trusting.
:::

## Full reference

The output of `nvx help` on the current `main` branch.

```text
nvx - A modern, secure, cross-platform runtime version manager

Usage:
  nvx <command> [arguments]

Runtimes: Node.js and Bun. A bare version means Node.js (nvm-compatible);
name another runtime before an '@' (e.g. bun@1.2).

Commands:
  install <[rt@]version>   Download and install a runtime version (e.g. 20, lts, bun@1.2)
  uninstall <[rt@]version> Remove an installed runtime version
  use <[rt@]version>       Switch the current terminal session to a runtime version
  default <[rt@]version>   Set the global default for a runtime (creates a link)
  list, ls                 List installed runtimes and versions
  list-remote, ls-remote   List available Node.js versions from nodejs.org
  env [--shell=<type>]     Print shell integration script (powershell, bash, zsh)
  auto [--shell=<type>]    Auto-switch runtimes from .nvmrc / .node-version /
                           .bun-version / package.json
  verify-install <pkgs>    Verify package safety before installing (called by wrappers)
  init-shims               Generate PATH shims in ~/.nvx/bin (and project bin shims in a project)
  policy init              Scaffold ~/.nvx/policy.json and/or .nvx-policy.json
  policy check             Check this project against the policy in force, with a
                           distinct exit code per failure class, for CI
  policy explain           Show each setting's effective value and where it came from
  shim <cmd> [args]        Internal shim router for package managers
  cleanup                  Reclaim disk from interrupted runs now (rarely needed;
                           every run reclaims some automatically)
  setup                    (Windows, Administrator) Grant the sandbox stat access
                           to the roots of the volumes nvx, your profile and the
                           current directory live on. Optional: installs and npx
                           do not need it; only a tool that resolves a path all
                           the way up to a drive root does, and nvx names this
                           command after such a failure. Slow on a large volume.
                           Also removes a loopback exemption an older nvx left;
                           '--all-drives' covers every fixed volume, which is
                           slow on large ones. 'setup --undo' reverses it
  doctor [--fix]           Check that nvx intercepts node/npm/npx on PATH (--fix repairs)
  grants list              Show this project's approved egress hosts, trusted tools, and policy pins
  grants reset [--all]     Forget this project's grants (or every project's, with --all)
  audit [--summary]        Review the local record of past runs and security decisions
  audit export             Export that record as json, jsonl or csv, filtered by
                           time and event, for a compliance pipeline
  report [--out=FILE]      Collect version, interception, policy and logs into one file
  import [nvm|fnm|volta]   Import Node.js versions already installed via nvm, fnm, or volta
                           (defaults to scanning all three)
  version, -v              Print version info
  help [command]           Show this list, or detail for one command

Options:
  --shell=<type>         Specify shell type: 'powershell', 'bash', 'zsh'
  --no-sandbox           Disable sandbox for this shim invocation. Must come
                         BEFORE the command; ignored if passed to it
  --standard             Force standard containment, overriding a project's
                         strict policy. Must come BEFORE the command
  --strict               Contain your own code too (not just installs/ad-hoc
                         tools). Must come BEFORE the command, like the two
                         above; after it, the flag belongs to the command
  --expose <in>[:<host>] (Windows) Publish a port a server inside the sandbox
                         listens on, so the host can reach it. The two numbers
                         must differ. Omit the host port to have one picked and
                         printed. Must come BEFORE the command
  --connect <host>[:<in>]  Let the sandbox reach ONE service already
                         running on your machine, over a tunnel nvx dials. The
                         two numbers must differ; the in-sandbox one is printed
                         and set as NVX_CONNECT_<host>. Must come BEFORE the
                         command
  --filesystem-provider=<name>  Override isolation.filesystem.provider
                         (native | docker). Passed TO the command:
                         nvx npm --filesystem-provider=...
  -y, --yes              Auto-approve all prompts
  -q, --quiet            Suppress success/info messages (errors and warnings still print)
  --verbose              Show what nvx is doing on the way: checks, session ids, permission work
  --agent-mode           Auto-approve all prompts and suppress success/info messages
                         (equivalent to -y -q; also settable via NVX_AGENT_MODE=1)

Environment:
  NVX_VERBOSE=1          Same as --verbose, for every run in this shell
  NVX_TRACE=1            Record one line per run in ~/.nvx/audit.log for
                         'nvx audit'. Off by default; a local debugging aid
  NVX_DEBUG=1            Record everything nvx prints in ~/.nvx/debug.log, for
                         'nvx report'. Off by default. These lines are rendered,
                         so they can contain paths and package names
  NVX_YES=true           Auto-approve prompts (same as -y)
  NVX_NONINTERACTIVE=1   Deny every prompt instead of asking, so a run that
                         needs approval fails rather than waits
  NVX_TRUST_YES=true     Approve trust prompts specifically -- adding an egress
                         host, trusting a tool or a project policy. -y and
                         --agent-mode deliberately do not
  NVX_HOME=<dir>         Use a different nvx home instead of ~/.nvx

Examples:
  nvx install lts
  nvx install bun@1.2
  nvx use 20.11.0
  nvx use bun@1.2
```

`policy check`, `policy explain` and `audit export` are not in v0.6.0. They are
coming in the next release.

## Exit codes

| Code | Means |
| --- | --- |
| `0` | The command worked. |
| `1` | The command failed. `nvx doctor` also exits non-zero when something needs attention. |
| `2` | A usage error, such as an unknown `--shell` value or a bad `nvx setup` flag. |
| `77` | nvx refused to run the command: a global install it will not contain, a package that failed its pre-install checks, or a sandbox it could not establish. Not in v0.6.0, which exits `1` for these. Coming in the next release. |
| `127` | The command to run was not found. |
| `129` | nvx stopped the command because the program that started it had exited. |

A contained or wrapped command passes through whatever the wrapped program
exited with. `nvx policy check` has its own codes, listed in
[docs/exit-codes.md](https://github.com/fstubner/nvx/blob/main/docs/exit-codes.md).
