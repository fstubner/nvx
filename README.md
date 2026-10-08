<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="site/public/assets/wordmark.png">
  <img src="site/public/assets/wordmark-light.png" width="360" alt="nvx">
</picture>

*nvx runs `npm install` and `npx` inside an OS sandbox on Windows, macOS and Linux, so a package cannot read your credentials or reach a host you did not allow. You and your agent type the same commands.*

[![CI](https://github.com/fstubner/nvx/actions/workflows/ci.yml/badge.svg)](https://github.com/fstubner/nvx/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

**[Documentation](https://nvx.run/docs/)** · **[Install](https://nvx.run/docs/install/)** · **[Changelog](https://nvx.run/changelog/)**

</div>

When a coding agent runs `npm install`, it executes code from strangers with your
credentials within reach. nvx puts that command inside an OS sandbox with a
throwaway `HOME` and an allowlist for anything it tries to reach over the
network. It can write only to the project and that home. It cannot read
`~/.ssh` or `~/.npmrc` either.
On macOS files outside your home directory stay readable, and the
[known limitations](https://nvx.run/docs/limitations/) say so plainly.

**You do not change how you run anything.** nvx installs shims on `PATH`, so
`npm install` is still `npm install`, contained when it runs code you did not
write. Wrappers such as srt and sfw need a prefix, and agent sandboxes cover only
the commands their own agent runs. nvx contains the install whoever runs it: you,
your agent, your editor or an MCP client.

**A prompt never widens the sandbox.** An agent that drives a terminal can answer
one, so nvx does not ask before trusting a project policy that loosens it or
reaching a host the allowlist does not name. It refuses with exit 77, prints the
`nvx trust` or `nvx allow-host` command for you to run, and tells the agent to
ask you. An agent with a shell of its own could still run that command, as
[SECURITY.md](SECURITY.md#known-limitations) explains. The package checks
(typosquats, fresh releases, install scripts) still ask at a terminal.
`--agent-mode` makes them refuse instead, and `-y` and `NVX_YES` do not override
it. The [agents page](https://nvx.run/docs/agents/) has the details.

nvx is also a Node.js and Bun version manager. The shims that intercept the
toolchain also run the version each project pins, in a terminal, an IDE task, a
git hook or CI. If you use nvm, fnm or volta today, nvx replaces them.

nvx is not related to Microsoft's NVX micro-VM project
(github.com/microsoft/nvx). On npm the package is `@fstubner/nvx`. The unscoped
`nvx` is a different project.

## Why this exists

nvx started on a fresh Windows machine and the usual version-manager headaches.
With LLMs making it practical to build exactly the tool you want, I built one
that also contains what it installs.

Coding agents run terminal commands in your workspace, and installs are where they
pick up code nobody has read. No tool can promise a package is safe, so the goal is
to limit what one can reach if the checks miss it.

## Install

```powershell
# Windows
irm https://nvx.run/install.ps1 | iex
```

```bash
# macOS / Linux
curl -fsSL https://nvx.run/install.sh | sh
```

```bash
# Or from npm, on any platform
npm install -g @fstubner/nvx
```

Prebuilt binaries, building from source and what the installer changes are in the
**[install guide](https://nvx.run/docs/install/)**.

Without the GitHub CLI (`gh`), the macOS and Linux one-liner checks only a
checksum that comes from the same release page as the binary. If you want more,
install `gh` 2.51 or newer, sign in with `gh auth login`, and check a downloaded
release asset against the build attestation. Compare the `.sha256` file beside it
too:

```bash
gh attestation verify <file> --repo fstubner/nvx --signer-workflow fstubner/nvx/.github/workflows/release.yml
```

The install guide has the steps for each platform.

## Usage

```bash
nvx install lts          # install a Node.js version
nvx install bun@1.2      # or a Bun one
nvx use 22               # switch this shell
npm install              # contained, through the shim
nvx --strict npm test    # contain your own code too
nvx doctor               # check interception and containment
```

The full reference is in **[Commands](https://nvx.run/docs/commands/)** and
**[Policy](https://nvx.run/docs/policy/)**.

## Documentation

| Page | Covers |
| --- | --- |
| [Overview](https://nvx.run/docs/) | What nvx does, and what it deliberately does not |
| [Installation](https://nvx.run/docs/install/) | Every install route, per platform |
| [Containment](https://nvx.run/docs/containment/) | What a contained command can reach, and what backs each claim |
| [Policy](https://nvx.run/docs/policy/) | Global and project policy files, and every setting |
| [Agents and CI](https://nvx.run/docs/agents/) | Exit 77, the approval switches, MCP servers and a snippet for AGENTS.md |
| [When nvx stops something](https://nvx.run/docs/blocked/) | Each refusal, its cause and the command that fixes it |
| [Known limitations](https://nvx.run/docs/limitations/) | What containment does not cover |
| [Commands](https://nvx.run/docs/commands/) | Commands, flags and environment variables |

The threat model is in [SECURITY.md](SECURITY.md), and the per-platform evidence
behind every containment claim is in
[docs/enforcement-matrix.md](docs/enforcement-matrix.md).

## Contributing

Issues and pull requests are welcome. Building from source and running the
sandbox probes are in [CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT
