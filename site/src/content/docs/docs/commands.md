---
title: Commands
description: Every nvx command, flag and environment variable, the approval switches, and exit codes.
---

Every nvx command, flag and environment variable is in the [full
reference](#full-reference) below. Run `nvx help <command>` for detail on one.

nvx reads its own flags only before the command. In `nvx -y npm install`, `-y`
is nvx's. In `npm install -y`, it is npm's.

## Approval switches

When more than one applies to a pre-install check, the first row that applies
wins.

| Switch | For | What it does |
| --- | --- | --- |
| `--agent-mode`, `NVX_AGENT_MODE=1` | A coding agent | Refuses whatever would ask and approves nothing. `-y` and `NVX_YES` approve no check while it is on |
| `-y`, `--yes`, `NVX_YES=true` | A person | Approves the pre-install checks without asking. Never approves a package OSV lists as malicious, and never widens the sandbox |
| `NVX_NONINTERACTIVE=1` | A script that must not wait | Denies every prompt. `-y` and `NVX_YES` still approve |
| `NVX_TRUST_YES=true` | One CI job you control | Approves requests to widen the sandbox: an untrusted loosening policy, an unknown host, a persistent tool profile. Nothing else approves these |

## Exit codes

| Code | Means |
| --- | --- |
| `0` | The command worked |
| `1` | The command failed |
| `2` | A usage error, such as an unknown nvx command |
| `77` | nvx refused. See [When something is blocked](/docs/blocked/) |
| `127` | The command to run was not found |

A contained command passes its own exit code through. `nvx policy check` has a
code for each kind of failure, listed in
[docs/exit-codes.md](https://github.com/fstubner/nvx/blob/main/docs/exit-codes.md).

## Full reference

This is what `nvx help` prints.

```text
nvx - Runs npm install and npx in an OS sandbox, and manages Node.js and Bun versions

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
  list-remote, ls-remote   List Node.js versions on nodejs.org or NVX_NODE_MIRROR
  env [--shell=<type>]     Print shell integration script (powershell, bash, zsh, fish, cmd)
  auto [--shell=<type>]    Auto-switch runtimes from .nvmrc / .node-version /
                           .bun-version / package.json (engines, volta)
  verify-install <pkgs>    Verify package safety before installing (called by wrappers)
  init-shims               Generate PATH shims in ~/.nvx/bin (and project bin shims in a project)
  policy init              Scaffold ~/.nvx/policy.json and/or .nvx-policy.json
  policy check             Check this project against the policy in force, with a
                           distinct exit code per failure class, for CI
  policy explain           Show each setting's effective value and where it came from
  shim <cmd> [args]        Internal shim router for package managers
  cleanup                  Reclaim disk from interrupted runs now (rarely needed;
                           every run reclaims some automatically)
  setup                    (Windows, Administrator) Remove what older nvx versions
                           left: drive-root and Users-folder access for the
                           sandbox, the loopback exemption, and lost permission
                           protection on C:\Users and your profile. nvx no
                           longer adds any of these. With nothing to fix it
                           says so.
  doctor [--fix]           Check that nvx intercepts node/npm/npx on PATH (--fix repairs)
  trust [<policy-file>]    Trust this project's policy file that loosens settings.
                           nvx refuses to run under it until you do, and never asks
  trust --tool <name>      Let a tool keep a persistent profile in this project
  allow-host <host[:port]> Let contained commands reach a host. Adds it to this
                           project's policy file and trusts that file, or to
                           ~/.nvx/policy.json with --global. --remove undoes it
  grants list              Show this project's egress hosts (from older nvx), trusted tools, and policy pins
  grants reset [--all]     Forget this project's grants (or every project's, with --all);
                           on Windows, put back the permissions of hidden .env files
  audit [--summary]        Review the local record of past runs and security decisions
  audit export             Export that record as json, jsonl or csv, filtered by
                           time and event, for a compliance pipeline
  report [--out=FILE]      Collect version, interception, policy and logs into one file
  import [nvm|fnm|volta]   Import Node.js versions already installed via nvm, fnm, or volta
                           (defaults to scanning all three)
  version, -v              Print version info
  help [command]           Show this list, or detail for one command

Options:
  --shell=<type>         Specify shell type: 'powershell', 'bash', 'zsh', 'fish', 'cmd'
  --no-sandbox           Disable sandbox for this shim invocation. Must come
                         BEFORE the command; ignored if passed to it
  --standard             Force standard containment, overriding a project's
                         strict policy. Must come BEFORE the command
  --strict               Contain your own code too (not just installs/ad-hoc
                         tools). Must come BEFORE the command, like the two
                         above; after it, the flag belongs to the command
  --expose <in>[:<host>] (Windows, Linux) Publish a port a server inside the
                         sandbox listens on, so the host can reach it. The two
                         numbers must differ. Omit the host port to have one
                         picked and printed. Must come BEFORE the command
  --connect <host>[:<in>]  Let the sandbox reach ONE service already
                         running on your machine, over a tunnel nvx dials. The
                         two numbers must differ; the in-sandbox one is printed
                         and set as NVX_CONNECT_<host>. Must come BEFORE the
                         command
  --filesystem-provider=<name>  Override isolation.filesystem.provider
                         (native | docker | sandbox-exec). Passed TO the command:
                         nvx npm --filesystem-provider=...
  -y, --yes              Approve the pre-install checks without asking. Must come
                         BEFORE the command. After it, it is the command's own
                         flag. Never widens the sandbox. Ignored in agent mode
  -q, --quiet            Suppress success/info messages (errors and warnings still print)
  --verbose              Show what nvx is doing on the way: checks, session ids, permission work
  --agent-mode           Never ask. Refuse whatever would need an answer, say why
                         and what a person can do, and exit 77. Also hides
                         success/info messages, like -q. Approves nothing, and
                         -y and NVX_YES approve no check while it is on. Must
                         come BEFORE the command, or set NVX_AGENT_MODE=1

Environment:
  NVX_VERBOSE=1          Same as --verbose, for every run in this shell
  NVX_TRACE=1            Record one line per run in ~/.nvx/audit.log for
                         'nvx audit'. Off by default; a local debugging aid
  NVX_DEBUG=1            Record everything nvx prints in ~/.nvx/debug.log, for
                         'nvx report'. Off by default. These lines are rendered,
                         so they can contain paths and package names
  NVX_YES=true           Same as -y, for everything started from this
                         environment, through the shims too
  NVX_AGENT_MODE=1       Same as --agent-mode
  NVX_NONINTERACTIVE=1   Deny every prompt instead of asking, so a run that
                         needs approval fails rather than waits
  NVX_TRUST_YES=true     Approve every request to widen the sandbox: a project
                         policy that loosens settings, a host the allowlist does
                         not name, a persistent tool profile. nvx never asks
                         about these, so this hands the decision to whatever
                         sets the environment. -y and --agent-mode do not
  NVX_HOME=<dir>         Use a different nvx home instead of ~/.nvx
  NVX_NODE_MIRROR=<url>  Fetch Node.js from this mirror instead of
                         https://nodejs.org/dist. NVM_NODEJS_ORG_MIRROR and
                         FNM_NODE_DIST_MIRROR are read too. A mirror is
                         trusted as nodejs.org is
  HTTPS_PROXY=<url>      Your own proxy. Contained connections the allowlist
                         permits go through it (or HTTP_PROXY), apart from
                         NO_PROXY hosts
  NO_COLOR=1           No colour codes in output. They are also left out
                         whenever the output is not a terminal

Examples:
  nvx install lts
  nvx install bun@1.2
  nvx use 20.11.0
  nvx use bun@1.2
```

`NVX_HOME` has a length limit, because the sandbox reaches nvx through sockets
under it. On Linux, a run whose `NVX_HOME` is too long refuses and names the
longest one that works.
