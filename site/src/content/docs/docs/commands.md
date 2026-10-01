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

### Which version a command runs

A command run through the shims (`node`, `npm`, `npx`, `pnpm`, `yarn`,
`corepack`, `bun`, `bunx`) picks its runtime version the same way in a
terminal, an IDE task, a git hook or CI. The first of these that applies wins:

1. Inside the sandbox, the policy's `runtime.versions` pin.
2. The version this shell has active from `nvx use` or the shell integration.
3. The version the project asks for in `.nvmrc`, `.node-version`,
   `.bun-version` or `package.json`, read from the current directory or the
   nearest one above it, up to your home directory. It has to be installed. The
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
| `nvx audit` | What nvx recorded: blocked hosts, pre-install checks that were approved or refused and how they were answered (including approvals by `-y`, `--agent-mode` and `NVX_YES`), and runs when `NVX_TRACE=1`. |
| `nvx grants` | Filesystem grants the sandbox holds, and which project each belongs to. |
| `nvx env` | Print the shell integration snippet. `--shell=<name>` picks the syntax: powershell, bash, zsh, fish or cmd. |
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

The complete `nvx help` surface, including every flag and environment variable.

```text
nvx <command> [arguments]

Commands:
  install <[rt@]version>   Install a runtime version. Bare version = Node.js
                           (e.g. 20, lts, latest); prefix a runtime with @
                           (e.g. bun@1.2, bun).
  uninstall <[rt@]version> Remove an installed runtime version
  use <[rt@]version>       Switch the current terminal session (installs if missing)
  default <[rt@]version>   Set the global default for a runtime (creates a link)
  list, ls                 List installed runtimes and versions
  list-remote, ls-remote   List Node.js versions on nodejs.org or NVX_NODE_MIRROR
  env [--shell=<type>]     Print shell integration script (powershell, bash, zsh, fish, cmd)
  auto [--shell=<type>]    Auto-switch based on .nvmrc / .node-version /
                           .bun-version / package.json engines
  import [nvm|fnm|volta]   Import Node.js versions already installed via nvm, fnm
                           or volta (defaults to scanning all three)
  init-shims               Generate PATH shims in ~/.nvx/bin (and project bin
                           shims when run inside a project)
  policy init              Create default policy files (--global, --project, --force)
  policy check             Check this project against the policy in force, with a
                           distinct exit code per failure class, for CI
                           (--format=json, --online)
  policy explain           Show each setting's effective value and where it came
                           from
  doctor [--fix]           Check that nvx intercepts node/npm/npx on PATH.
                           Diagnosis is read-only; --fix repairs a shadowed
                           persistent PATH
  grants list              Show this project's approved egress hosts, trusted
                           tools, and policy pins
  grants reset [--all]     Forget this project's grants (or every project's)
  audit [--summary]        Review the local record of security decisions, and of
                           past runs when NVX_TRACE=1 (--runs, --failures,
                           --limit=N, --all)
  audit export             Export that record as json, jsonl or csv, filtered by
                           time and event (--since, --event, --format, --out)
  report [--out=FILE]      Collect version, interception, policy and log tails
                           into one file to read and send on. Nothing is uploaded
  cleanup                  Reclaim disk from interrupted runs now (rarely needed;
                           every run reclaims some automatically)
  setup [--undo]           (Windows, Administrator) Grant the sandbox stat access
                           to the roots of the volumes nvx, your profile and the
                           current directory live on, and remove a loopback
                           exemption an older nvx left. Optional: installs and
                           `npx` do not need it. Slow on a large volume
  verify-install <pkgs>    Verify package safety before installing (called by shims)
  shim <cmd> [args]        Internal shim router (called by generated wrappers)
  version, -v              Print version info
  help [command]           Show this list, or detail for one command

Isolation flags — ALL of these go BEFORE the command, as
`nvx --no-sandbox npm ...`. Written after it they belong to the command: nvx
notices them, says they did not apply, and passes them through untouched. That
holds in both directions — no package can escape the sandbox by tacking a flag
onto npm, and nvx does not reinterpret a word that belongs to another tool:
  --no-sandbox             Run this invocation without the sandbox
  --standard               Force standard containment, overriding a project
                           policy that sets strict
  --strict                 Contain your own code too, not just installs and
                           ad-hoc tools
  --expose <in>[:<host>]   (Windows) Publish a port a server inside the sandbox
                           listens on, so the host can reach it — Windows
                           refuses connections into an AppContainer. The two
                           numbers must differ; omit the host one to have a free
                           port picked and printed. Grants no network capability
  --connect <host>[:<in>]  Let the sandbox reach one service already running on
                           your machine — the mirror of `--expose`. Give the port
                           your service uses; nvx runs a listener and dials the
                           real one itself. The two numbers must differ. Grants
                           no network capability

Passed to the wrapped command only, not before it:
  --filesystem-provider=<name>  Override isolation.filesystem.provider
                           (native | docker | sandbox-exec). `nvx npm --filesystem-provider=…`,
                           not `nvx --filesystem-provider=… npm`

Options:
  --shell=<type>           Shell syntax to emit: powershell, bash, zsh, fish, cmd
  -y, --yes                Auto-approve all prompts. Each pre-install check approved
                           this way is printed and recorded in the audit log
  -q, --quiet              Suppress success/info messages (errors and warnings
                           still print)
  --verbose                Show what nvx is doing on the way: checks, session
                           ids, permission work. Also NVX_VERBOSE=1 (env)
  NVX_TRACE=1              (env) Record one line per run in ~/.nvx/audit.log for
                           `nvx audit`. Off by default; a local debugging aid
  --agent-mode             Equivalent to -y -q; also settable with
                           NVX_AGENT_MODE=1
  NVX_NONINTERACTIVE=1     (env) Deny every prompt instead of asking, so a run
                           needing approval fails rather than waits
  NVX_TRUST_YES=true       (env) Approve trust prompts specifically — adding an
                           egress host, trusting a tool or a project policy.
                           -y and --agent-mode deliberately do not
  NVX_HOME=<dir>           (env) Use a different nvx home instead of ~/.nvx
  NVX_NODE_MIRROR=<url>    (env) Fetch Node.js from this mirror instead of
                           https://nodejs.org/dist. NVM_NODEJS_ORG_MIRROR and
                           FNM_NODE_DIST_MIRROR are read too. A mirror is
                           trusted as nodejs.org is
  HTTPS_PROXY=<url>        (env) Your own proxy. Contained connections the
                           allowlist permits go through it (or HTTP_PROXY),
                           apart from NO_PROXY hosts
  NO_COLOR=1               (env) No colour codes in output. They are also left
                           out whenever the output is not a terminal
```

## Exit codes

`0` means the command worked. A non-zero code from `nvx doctor` means something
needs attention rather than that doctor itself failed, and a contained command
propagates whatever the wrapped program exited with.
