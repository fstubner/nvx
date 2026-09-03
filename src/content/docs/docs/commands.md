---
title: Commands
description: Every Example command and flag, with the output formats each one prints.
---

A sample reference page, showing the table treatments and callouts this docs shell provides.

## Commands

The marker above a table turns its first column into row headers — useful when
the left column names the thing each row is about. It is a `div` with a data
attribute, and it applies to the table immediately after it.

<div data-ui-table="row-headers"></div>

| Command | What it does |
| --- | --- |
| `run` | Does the one thing, and prints the result. |
| `check` | Validates the configuration without doing anything. |
| `version` | Prints the version, the build date and the target triple. |

Without the marker, a table renders with a plain header row:

| Flag | Default | Meaning |
| --- | --- | --- |
| `--json` | off | Print machine-readable output on stdout. |
| `--quiet` | off | Suppress progress; errors still go to stderr. |
| `--timeout <s>` | 30 | Give up after this many seconds. |

On a narrow screen a wide table scrolls sideways inside its own frame rather
than pushing the page out of shape. Nothing is needed in the Markdown for
that; the shell wraps every table as the page loads.

## Output

Human-readable by default, machine-readable on request:

```sh
example run --json | jq '.items[]'
```

:::note
Callouts come from Starlight. `note`, `tip`, `caution` and `danger` are
available, and each takes an optional title after the type.
:::

:::caution[Exit codes are part of the contract]
`0` means it worked, `1` means it ran and found a problem, and `2` means the
arguments were wrong. A script can tell those apart; changing them is a
breaking change.
:::
