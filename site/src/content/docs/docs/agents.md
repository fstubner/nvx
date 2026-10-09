---
title: AI agents and MCP
description: Run coding agents, MCP servers and CI jobs under nvx, and what nvx does and does not cover for them.
---

Your coding agent types `npm install` and `npx` like you do, so the packages it
installs are checked and contained with no extra setup.

## What nvx covers

nvx contains the packages an agent installs. It does not contain the agent.
Each `npm install`, `npx` and postinstall script runs in the sandbox, whoever
started it. The agent can still read your files with its own tools and run
commands nvx does not contain, such as `npm test` and `node`.

## Agent mode

Set this in the agent's environment.

```sh
NVX_AGENT_MODE=1
```

At a terminal nvx asks before some installs, and an agent could answer for you.
In agent mode nvx asks nothing. It refuses, exits 77, and prints the policy line
a person can add. `-y` and `NVX_YES` approve nothing while it is on.

Never put `NVX_YES` or `NVX_TRUST_YES` in an agent's settings. `nvx doctor`
warns when either is set.

## A snippet for AGENTS.md or CLAUDE.md

```markdown
## nvx

nvx guards npm installs and tool runs in this repository. A command that exits
77 was refused on purpose.

- Do not retry with `-y`, `NVX_YES`, `NVX_TRUST_YES` or `nvx --no-sandbox`.
- Do not edit `~/.nvx/policy.json` or `.nvx-policy.json`.
- Do not run `nvx trust` or `nvx allow-host`.
- Tell me which package or host was refused and the line nvx printed.
- For a release inside the 24-hour window, pin an older version
  (`npm install pkg@1.2.3`).
```

A model can ignore this. To enforce it, deny those commands in the agent's own
permissions. For Claude Code, in `.claude/settings.json`:

```json
{
  "permissions": {
    "deny": [
      "Bash(nvx trust:*)",
      "Bash(nvx allow-host:*)",
      "Bash(nvx --no-sandbox:*)",
      "Edit(~/.nvx/**)",
      "Edit(.nvx-policy.json)"
    ]
  }
}
```

These rules are untested with nvx, and they do not catch `NVX_YES=1 npm install`
or an edit made through the shell. Codex and Cursor have their own formats.

## MCP servers started with npx -y

nvx contains an MCP server started with `npx -y some-mcp-server` like any other
`npx`. Nobody is there to answer, so a check that would ask is refused. The
client usually shows `-32000: Connection closed`, and nvx sends the real reason
as its answer to the client's first request. The usual cause is a release from
the last 24 hours. Fix it in this order.

1. **Pin a version you have used before** in the client's config.

   ```json
   "args": ["-y", "some-mcp-server@1.2.3"]
   ```

2. **Exempt the package** in `~/.nvx/policy.json`.

   ```json
   { "release_age": { "trusted_packages": ["some-mcp-server"] } }
   ```

Do not put `NVX_YES` in the server's environment.

A contained server cannot reach a service on your machine. Open one port with
`--connect`, for example
`nvx --connect 9222:19222 npx @playwright/mcp --cdp-endpoint http://127.0.0.1:19222`.

A contained `npx` starts with an empty npm cache, so a server is slower to
answer. For `@modelcontextprotocol/server-filesystem`, the median time to
answer `initialize` was 8.2 s contained against 2.0 s uncontained with a warm cache on Windows,
and 6.3 s against 1.0 s on Linux, measured on a busy machine. A client with a short start-up timeout may
give up first.

## Inside another sandbox

Nobody has tested nvx inside the sandboxes of Claude Code, Codex, Cursor or
Docker Sandboxes. On Linux a parent that refuses unprivileged namespaces stops
nvx's sandbox, and Seatbelt and AppContainer may refuse to nest. nvx then exits
77 and never runs the command uncontained. If the parent sandbox already
contains the session, `nvx --no-sandbox` skips nvx's sandbox and keeps the
checks.

## In CI

- Put per-check policy lines in the policy file rather than setting `NVX_YES`.
- Run `nvx policy check` to gate a pipeline. It exits with a different code for
  each kind of failure, listed in
  [docs/exit-codes.md](https://github.com/fstubner/nvx/blob/main/docs/exit-codes.md).
- Run `nvx doctor` in the job to confirm the sandbox starts.
- Set `NVX_TRUST_YES=true` only in a job whose environment you control, and only
  for a host or policy you have reviewed.
