---
title: Commands
description: The nvx command surface, grouped by what you are trying to do.
---

Run `nvx help <command>` for any of these.

## Runtimes

<div data-ui-table="row-headers"></div>

| Command | What it does |
| --- | --- |
| `nvx install <version>` | Install a Node.js or Bun version. Accepts `22`, `lts`, `latest`, a range, or whatever a `.nvmrc` says. With no version it installs what the project's `.nvmrc`, `.node-version` or `package.json` asks for. The first version you install becomes the default. |
| `nvx use <version>` | Switch this shell to a version. With no version it switches to the one the project asks for. Aliases such as `lts/*`, `lts/iron`, `node` and `stable` work in a `.nvmrc` and on the command line. |
| `nvx default <version>` | Set the version new shells start on. Only the shim directory needs to be on `PATH`. |
| `nvx list` | Installed versions. `nvx list-remote` lists what is available. |
| `nvx uninstall <version>` | Remove an installed version. |
| `nvx auto` | Switch to the version this directory pins. |
| `nvx import [nvm\|fnm\|volta]` | Install the Node.js versions you already have in nvm, fnm or volta. nvx reads which versions they hold and downloads its own verified copy of each. With no argument it looks in all three. |

### Which version a command runs

The shims are `node`, `npm`, `npx`, `pnpm`, `yarn`, `corepack`, `bun` and
`bunx`. A command run through them picks its runtime version the same way in a
terminal, an IDE task, a git hook or CI. The first of these that applies wins:

1. Inside the sandbox, the policy's `runtime.versions` pin.
2. The version this shell has active from `nvx use` or the shell integration.
3. The version the project asks for in `.nvmrc`, `.node-version`,
   `.bun-version` or `package.json`. The shim looks for it in the current
   directory or the nearest one above it, up to your home directory. It has to be installed. The
   shim never downloads anything. When the version is missing it runs the
   default and prints the `nvx install` command that adds it.
4. The global default from `nvx default`.

### Shells

`nvx use` and the switch on `cd` work in PowerShell, bash, zsh and fish. Each
loads the integration from `nvx env --shell=<shell>`. The installers add that
line to your profile. For fish it goes in `~/.config/fish/conf.d/nvx.fish`.
`nvx use` picks the syntax of the shell it was run from. `--shell=powershell`,
`--shell=bash`, `--shell=zsh`, `--shell=fish` and `--shell=cmd` name one
outright.

cmd.exe cannot evaluate a program's output, so it has no integration and no
profile to put one in. The shims still run each project's pinned version there,
and `nvx default <version>` sets the version new windows start on. To switch
one window by hand, run this at the prompt:

```bat
FOR /f "tokens=*" %i IN ('nvx use 22 --shell=cmd') DO %i
```

In a `.bat` file write `%%i` for `%i`. `nvx env --shell=cmd` prints the line that
puts the shim directory first on `PATH`, and it is read the same way:

```bat
FOR /f "tokens=*" %i IN ('nvx env --shell=cmd') DO %i
```

Tools installed with `npm install -g` go in the active Node.js version's
`npm_global` folder. A shell without the integration does not have that folder on
`PATH`, so it does not find them.

## Running things

<div data-ui-table="row-headers"></div>

| Command | What it does |
| --- | --- |
| `nvx <command>` | Run a wrapped command through nvx explicitly, for example `nvx npm install` or `nvx --no-sandbox npx wrangler login`. `<command>` is `node`, `npm`, `npx`, `pnpm`, `yarn`, `corepack`, `bun` or `bunx`. It is contained if it runs code you did not write. |
| `nvx --strict <command>` | Contain the command even when it is your own code. |
| `nvx --standard <command>` | Drop back to the default level for one run. Never uncontains an install. |
| `nvx --no-sandbox <command>` | Run uncontained, for the cases nvx refuses by design, such as a global install. |
| `nvx --connect <host-port>[:<in-sandbox-port>]` | Let one contained run reach one service already running on your machine. |
| `nvx --expose <in-sandbox-port>[:<host-port>]` | Windows and Linux. Publish a port a contained server listens on, so your browser can reach it. |
| `nvx init-shims` | Write the shims in `~/.nvx/bin`, and the project bin shims when run inside a project. |

## Checking and policy

<div data-ui-table="row-headers"></div>

| Command | What it does |
| --- | --- |
| `nvx doctor` | Whether interception, the shell integration and containment are healthy. `--fix` repairs what it can. |
| `nvx policy init` | Write a project `.nvx-policy.json` that sets nothing yet, with empty `blocked_packages` and `allow_hosts` to add to. `--global` writes `~/.nvx/policy.json` with the defaults instead. |
| `nvx policy check` | Check this project against the policy in force, for CI. It never prompts, and exits with a distinct code per kind of failure. It makes no network request unless you pass `--online`. `--format=json` prints the verdict as data. |
| `nvx policy explain` | Show each setting's effective value and which file it came from. |
| `nvx audit` | What nvx recorded: the hosts a contained run was allowed to reach and the ones it was refused, pre-install checks that were approved or refused and how they were answered, and runs when `NVX_TRACE=1`. Approvals by `-y` and `NVX_YES` are recorded too, and so are refusals under `--agent-mode`. |
| `nvx audit export` | Export that record as json, jsonl or csv, filtered with `--since` and `--event`, to a file with `--out`. |
| `nvx trust` | Trust this project's policy file that loosens nvx's settings, wherever it applies. nvx never asks about that. It refuses the command and prints this line for you to run, with `--hash` so it trusts only the content you were shown. `nvx trust <file>` trusts one file, and `nvx trust --tool <name>` lets a tool keep a persistent profile in the project. |
| `nvx allow-host <host[:port]>` | Let contained commands reach a host. nvx adds it to `allow_hosts` in this project's `.nvx-policy.json` and trusts that file, or with `--global` adds it to `~/.nvx/policy.json`. The port defaults to 443. nvx prints this command when it refuses a host. `nvx allow-host --remove <host[:port]>` undoes it, in the same file. |
| `nvx grants list` | This project's recorded grants: trusted tools, trusted project policy files, directories granted read and execute access for `allow_read_exec`, and egress hosts recorded by older versions. |
| `nvx grants reset` | Forget this project's grants, or every project's with `--all`. Read and execute permissions nvx granted are withdrawn. On Windows it also puts back the permissions of the `.env` files nvx hid. |
| `nvx env` | Print the shell integration snippet. `--shell=<name>` picks the syntax: powershell, bash, zsh, fish or cmd. |
| `nvx report` | A diagnostic bundle to attach to a bug report. Nothing is uploaded. |
| `nvx cleanup` | Reclaim disk from interrupted runs now. Every run reclaims some automatically, so this is rarely needed. |
| `nvx setup` | Windows only, from an Administrator terminal. Removes what older versions of nvx left: sandbox access to drive roots and the `Users` folder, the loopback exemption, and the permission protection older versions switched off on `C:\Users` and your profile folder. nvx adds none of these now, so run it once after upgrading from an older version. With nothing to remove it says so and exits 0. `--undo` and `--all-drives` are accepted and change nothing. |

## Approvals

These switches answer nvx's questions or turn protection off. When more than
one applies to a pre-install check, the first row that applies wins.

| Switch | Who it is for | What it does |
| --- | --- | --- |
| `--agent-mode`, `NVX_AGENT_MODE=1` | A coding agent's environment | Refuses whatever would ask, exits 77, and approves nothing. It beats `-y` and `NVX_YES`, which approve no check while it is on |
| `-y`, `--yes`, `NVX_YES=true` | A person | Approves the pre-install checks without asking. It never approves a package OSV lists as malicious and never widens the sandbox. `-y` works only before the command |
| `NVX_NONINTERACTIVE=1` | A script that must not wait | Denies every prompt instead of asking. `-y` and `NVX_YES` still approve |
| `NVX_TRUST_YES=true` | One CI job you control | Approves requests to widen the sandbox: an untrusted loosening policy, an unknown host, a persistent tool profile. Nothing else approves these |
| `--no-sandbox` | A person | Runs one command uncontained. It goes before the command |

`nvx doctor` warns when `NVX_YES`, `NVX_AGENT_MODE` or `NVX_TRUST_YES` is set.
[Agents and CI](/docs/agents/) says which to use where.

## Policy files

A project can carry a `.nvx-policy.json`, and nvx merges it over the global one.
Hosts in a project's `allow_hosts` are added to the global allowlist.

```json
{
  "isolation": {
    "level": "standard",
    "network": {
      "allow_hosts": ["registry.example.com:443"]
    }
  }
}
```

Use `allow_hosts` to add a host. A project's `default_allow` replaces the
global list instead of adding to it, which drops the registry and OSV hosts
unless you list them again.

:::caution[A policy that widens the sandbox needs you to trust it]
A project file lives in a repository. One line in a pull request could
otherwise hand a contained install a new destination. nvx does not run a
command under a widening policy until it is trusted for that project, and it
never asks about it at a prompt. A coding agent that drives a terminal could
answer a prompt itself. Instead nvx refuses with exit 77, lists what the file
loosens, and prints `nvx trust <file> --hash <hash>` for you to run in your own
terminal. The hash makes it trust only the content you were shown.
`-y`, `--agent-mode` and `NVX_YES` do not trust anything.
`NVX_TRUST_YES=true` approves it without the command, for every run started
from that environment. Setting it hands the decision to whatever sets the
environment, so set it only in a place you control, such as one CI job.
:::

## Full reference

This is what `nvx help` prints, including every flag and environment
variable.

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

`NVX_HOME` has a length limit. The sandbox reaches nvx through sockets under
it, and a socket path must be shorter than 108 bytes. On Linux a run that needs
one refuses and names the longest `NVX_HOME` that works. On Windows nvx moves
the sockets to the sandbox's own folder in `%LOCALAPPDATA%\Packages`, and
refuses only when that path is too long as well.

## Exit codes

`0` means the command worked. A non-zero code from `nvx doctor` means something
needs attention. It does not mean doctor itself failed. A contained command
propagates whatever the wrapped program exited with. `77` means nvx refused to
run the command, for example a package that failed its pre-install checks, a
project policy nobody has trusted, or a question it would have asked under
`--agent-mode`. A contained command that fails after nvx refused a host the
allowlist does not name exits `77` too. `127` means a command was not found, and
`2` is a usage error such as an unknown nvx command.
`nvx policy check` has a code of its own for each kind of failure, and
[docs/exit-codes.md](https://github.com/fstubner/nvx/blob/main/docs/exit-codes.md)
lists them all.
