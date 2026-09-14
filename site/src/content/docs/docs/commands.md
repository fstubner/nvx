---
title: Commands
description: The nvx command surface, grouped by what you are trying to do.
---

Run `nvx help <command>` for any of these.

## Runtimes

<div data-ui-table="row-headers"></div>

| Command | What it does |
| --- | --- |
| `nvx install <version>` | Install a Node.js or Bun version. Accepts `22`, `lts`, `latest`, a range, or whatever a `.nvmrc` says. |
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
| `nvx grants` | Filesystem grants the sandbox holds, and which project each belongs to. |
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

## Exit codes

`0` means the command worked. A non-zero code from `nvx doctor` means something
needs attention rather than that doctor itself failed, and a contained command
propagates whatever the wrapped program exited with.
