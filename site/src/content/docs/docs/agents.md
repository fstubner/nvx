---
title: Agents and CI
description: What to do when nvx refuses a command run by a coding agent, a CI job or an MCP client.
---

nvx contains the packages a coding agent installs. It does not contain the agent.
This page covers what an agent sees when nvx stops a command, which switches to
set, and what a person has to do. Each refusal and its fix is on [When nvx stops
something](/docs/blocked/).

## What exit 77 means

Exit code `77` means nvx refused to run the command on purpose. The command did
not start, or it started and nvx refused a connection while it ran. Retrying the
same command gives the same answer.

An agent sees a message such as this one, from a host the allowlist does not name:

```text
Blocked egress: registry.example.org:443
To allow it, run this in your own terminal, in /work/app:  nvx allow-host registry.example.org:443
If you are an automated agent: do not run nvx commands or change nvx's settings to allow this yourself. Ask the person you work for to decide.
```

What the agent should do:

1. Stop. Do not retry with `-y`, `NVX_YES`, `NVX_TRUST_YES` or `nvx --no-sandbox`.
2. Tell the person which package or host was refused and quote the line nvx
   printed.
3. For a release inside the 24-hour window, pin an older version instead, as
   `pkg@1.2.3`. That keeps every check on the version it names, and nvx's message
   says an agent may do it.

The person then runs the printed command, or adds the printed policy line, and
tells the agent to try again.

Other exit codes: `1` is a command that ran and failed, `2` is a usage error such
as an unknown nvx command, and `127` is a command that was not found. A wrapped
command's own exit code passes through unchanged. [docs/exit-codes.md](https://github.com/fstubner/nvx/blob/main/docs/exit-codes.md)
has the full table.

## Why nvx never asks

A prompt is a person's decision only when nothing else can type into the
terminal. An agent harness that runs commands in a pseudo-terminal presents one,
and the model can answer. So nvx never asks about widening the sandbox. That is
trusting a project policy that loosens settings, reaching a host the allowlist
does not name, or keeping a tool's persistent profile. It refuses and prints the
command for a person to run.

The pre-install checks (typosquats, fresh releases, install scripts, known
vulnerabilities) still ask at a terminal. Set `--agent-mode` or `NVX_AGENT_MODE=1`
in the agent's environment and they refuse too.

## Which switch to use

| Switch | Use it for | Effect |
| --- | --- | --- |
| `--agent-mode`, `NVX_AGENT_MODE=1` | An agent's environment | Refuses whatever would ask and approves nothing. Wins over `-y` and `NVX_YES`, which approve no check while it is on |
| `-y`, `NVX_YES=true` | A person at a terminal | Approves the pre-install checks without asking. Never approves a package OSV lists as malicious and never widens the sandbox |
| `NVX_NONINTERACTIVE=1` | A script that must not wait | Denies every prompt. `-y` and `NVX_YES` still approve |
| `NVX_TRUST_YES=true` | One CI job whose environment you control | Approves requests to widen the sandbox without the command. Nothing else does |

When several apply to a pre-install check, the first row that applies wins.
`NVX_TRUST_YES` is separate, because it answers a different question.

`NVX_TRUST_YES` hands every widening decision to whatever sets the environment.
Never put it in an agent's settings. `nvx doctor` warns when `NVX_YES`,
`NVX_AGENT_MODE` or `NVX_TRUST_YES` is set, so run it in the environment the agent
uses.

## What a person runs

| Refusal | Command |
| --- | --- |
| A host the allowlist does not name | `nvx allow-host <host[:port]>`. It adds the host to the project's `.nvx-policy.json` and trusts that file. `--global` writes `~/.nvx/policy.json` instead |
| A project `.nvx-policy.json` that loosens settings | `nvx trust <file> --hash <hash>`, as the refusal prints it. The hash makes it trust only the content you were shown |
| A tool that wants a persistent profile | `nvx trust --tool <name>` |
| A pre-install check | The policy line the refusal prints, added to `~/.nvx/policy.json` |

Run them in the project folder, in your own terminal. `nvx allow-host --remove
<host[:port]>` takes a host back out, and `nvx grants reset` forgets what is
trusted.

## A block for AGENTS.md or CLAUDE.md

Copy this into the file your agent reads:

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

This is a request, and nvx cannot enforce it. A model can ignore it. The next
sections cover what does enforce it.

## MCP servers started with `npx -y`

An MCP client starts a server with a command such as `npx -y some-mcp-server`.
nvx contains that run like any other `npx`. Nobody is at the keyboard, so a check
that would ask is refused, and a package published in the last 24 hours counts.
The client usually shows `-32000: Connection closed`. nvx answers the client's
first request with the real reason, for example that a package version was
published inside the release-age cooling-off window, and names the package.

Fix it in this order:

1. **Pin a version you have already used.** In the client's config, write
   `"args": ["-y", "some-mcp-server@1.2.3"]`. Every check still runs on the
   version it names, and an agent may do this itself.
2. **Exempt the package.** Add it to `release_age.trusted_packages` in
   `~/.nvx/policy.json`:

   ```json
   { "release_age": { "trusted_packages": ["some-mcp-server", "@your-scope/*"] } }
   ```

   This waives only the cooling-off window for that package. A project file that
   adds an entry needs `nvx trust`, so the global file is simpler.
3. **Do not put `NVX_YES` in the server's environment.** It approves every check
   for that server, and it approves nothing where `NVX_AGENT_MODE` is also set.

A contained server cannot reach a service on your machine. Use `--connect` for
one named port, for example `nvx --connect 9222:19222 npx @playwright/mcp
--cdp-endpoint http://127.0.0.1:19222`. [SECURITY.md](https://github.com/fstubner/nvx/blob/main/SECURITY.md)
explains why the two port numbers differ.

A contained `npx` starts with an empty npm cache every run, so a server takes
longer to answer. Measured as the time until the answer to `initialize` for
`@modelcontextprotocol/server-filesystem`, the median was 8.2 s contained against
2.0 s uncontained with a warm cache on Windows, and 6.3 s against 1.0 s on Linux.
Those runs were on a busy machine. A client with a short start-up timeout may give
up first.

## Harden the agent's permissions

Nothing in nvx stops an agent with a shell from running `nvx trust`,
`nvx allow-host`, `nvx --no-sandbox` or `nvx --connect`, clearing
`NVX_AGENT_MODE`, putting `NVX_YES` or `NVX_TRUST_YES` in front of a command, or
editing `~/.nvx/policy.json`. Deny those in the agent's own permission settings.

For Claude Code, in `.claude/settings.json`:

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

These rules are untested here. They do not cover `NVX_YES=1 npm install`, a
variable put in front of a command, or an edit made through a shell command
instead of the editor tool. Codex and Cursor have their own permission formats,
and none has been tried with nvx. A deny rule matches a command's text, so treat
the list as a second lock.

## nvx contains packages, not agents

An agent sandbox contains a session. nvx contains a command: one `npm install`,
one `npx`, one postinstall script. It applies whoever runs that command. It does
not stop an agent from reading your files with its own tools, from running its own
code, or from running commands that nvx does not intercept. At the default level
`npm run build`, `npm test` and `node` run uncontained, and `isolation.level:
strict` contains them at some cost. [Known limitations](/docs/limitations/) lists
what else is left.

## Inside another sandbox

Nobody has tested nvx inside the sandbox of Claude Code, Codex, Cursor or Docker
Sandboxes. Whether nvx's own sandbox can start inside another one depends on the
parent. Linux needs unprivileged user and network namespaces, and a parent that
refuses them stops the sandbox. Seatbelt on macOS and AppContainer on Windows may
refuse to nest as well.

nvx fails closed. If the sandbox cannot start, the command does not run, nvx exits
77, and `nvx doctor` says why. It never falls back to running uncontained. If a
parent sandbox already holds the blast radius, `nvx --no-sandbox` skips nvx's
sandbox for one command and keeps the pre-install checks.

## In CI

- Add per-check policy lines to the policy file instead of setting `NVX_YES`.
- Run `nvx policy check` to gate a pipeline. It makes no network request unless
  you pass `--online`, and it exits with a distinct code per failure class.
- Run `nvx doctor` in the job to confirm the shims intercept and the sandbox
  starts.
- Set `NVX_TRUST_YES=true` only in one job whose environment you control, and only
  for a host or policy you have reviewed.
