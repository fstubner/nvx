---
title: When nvx stops something
description: Each refusal nvx makes, what causes it, and the exact command or policy line that fixes it.
---

nvx refuses a command in one of a few ways. Each section below names the cause
and the fix. A refusal exits with code `77`, and prints the fix as its last
lines. Run commands in your own terminal in the project folder.

Policy lines go in `~/.nvx/policy.json`. If the file does not exist, create it
with exactly the line shown. If it does, merge the line into it. `nvx policy init
--global` writes a starting file.

If you are an automated agent, do not run these fixes yourself. Tell the person
you work for what was refused. [Agents and CI](/docs/agents/) explains why.

## A host the allowlist does not name

```text
Blocked egress: registry.example.org:443
```

A contained command tried to reach a host that is not in `default_allow` or
`allow_hosts`. nvx did not connect.

Fix: `nvx allow-host registry.example.org:443`. It adds the host to the project's
`.nvx-policy.json` and trusts that file. `--global` writes `~/.nvx/policy.json`
instead. The port defaults to 443. For one run in an environment you control, set
`NVX_TRUST_YES=true`.

A local service and a literal link-local address such as `169.254.169.254` get no
one-line command. `NVX_TRUST_YES` does not approve them. For a service on your
machine use `nvx --connect <port>` for one run, or put the host in `allow_hosts`.

## A project policy that loosens settings

```text
nvx refused to run under project policy /work/app/.nvx-policy.json. It loosens nvx's security settings, and it has not been trusted
```

A `.nvx-policy.json` in the repository widens the sandbox, for example by
adding a host or switching `isolation.network.mode` to `open`. Every command in
that project exits 77 until you trust the file. Read the file first.

Fix: `nvx trust .nvx-policy.json --hash <hash>`, exactly as the refusal prints
it. The hash makes nvx trust only the content you were shown. In a monorepo, a
root file is trusted once for every package below it.

## A tool that wants a persistent profile

```text
nvx refused to let "tool" keep a persistent profile in this project
```

A tool asked to keep its logins and settings between runs.

Fix: `nvx trust --tool <name>`.

## Install scripts

```text
Package esbuild@0.28.2 contains installation scripts
```

The package has a `preinstall`, `install` or `postinstall` script. That is code
that runs at install time.

Fix, for a package you trust:

```json
{ "install_scripts": { "trusted_packages": ["esbuild"] } }
```

If the refusal says `enforce_ignore_scripts is on`, the policy blocks every
package with such scripts, and the same line is the exception. To install with
scripts off, pass `--ignore-scripts`.

## Typosquats

```text
Package "expresss" is suspiciously close to popular package "express"
```

The name is a few edits from a popular package and has far fewer downloads. Check
the spelling first.

Fix, if the name is right:

```json
{ "typosquatting": { "trusted_packages": ["expresss"] } }
```

## Release age

A package version was published inside the cooling-off window, 24 hours by
default. A version the registry gives no publish time for is treated the same
way, because nvx cannot tell its age.

Fix, narrowest first:

1. Name an older version: `npm install pkg@1.2.3`. `npm view pkg time` lists when
   each version was published.
2. Exempt the package:

   ```json
   { "release_age": { "trusted_packages": ["pkg"] } }
   ```

   Use a scope such as `"@your-scope/*"` for a registry that sends no publish
   times, or set `release_age.enabled` to `false`.
3. To approve every check in the run, `NVX_YES=true`, or `-y` before the command.
   Do not set it for an MCP server or an agent.

## Known vulnerabilities

An OSV advisory exists for a package version in the install.

Fix, for advisories you have assessed:

```json
{ "vulnerabilities": { "allowed_advisories": ["GHSA-xxxx-yyyy-zzzz"] } }
```

To accept everything below a severity, set `vulnerabilities.min_severity` to
`low`, `moderate`, `high` or `critical`.

## A package listed as malicious

An advisory starting `MAL-` names the package. `-y`, `NVX_YES`, `--agent-mode`,
`NVX_TRUST_YES` and `min_severity` do not allow it.

Fix: only if you checked that the advisory does not apply, add its exact ID to
`vulnerabilities.allowed_advisories`. A pattern such as `"MAL-*"` does not work.

## A blocked package

```text
Blocked by security policy: Package "is-number" is blacklisted.
```

The name is in `blocked_packages`. nvx does not say which dependency pulled it in.
Run `npm ls is-number` to find out.

Fix: remove the name from `blocked_packages` in the policy file that lists it.
`nvx policy explain` shows which file set the value.

## A lookup that failed

The registry or `api.osv.dev` could not be reached, so a check could not run. No
policy setting waives a failed lookup.

Fix: retry when the service is reachable. To proceed without the lookup,
`NVX_YES=true` or `-y` approves every check in the run.

## npm could not work out what the command installs

```text
Installation aborted: npm could not resolve what this command installs
```

nvx asks npm what an install brings in, so it can check every package. That step
failed. When a `Blocked egress` line comes first, the host in it is the cause, and
`nvx allow-host` is the fix. Otherwise read npm's own error above the refusal.

## A global install

```text
nvx refused: global installs (-g) can't run inside the sandbox.
```

A global install writes outside the project and would run uncontained on every
future command.

Fix, knowing it is uncontained: `nvx --no-sandbox npm install -g <package>`. For
a tool you only need to run, `npx <package>` is contained.

## The sandbox could not start

```text
nvx could not create the sandbox to contain this command, so it did not run.
```

Most often on Linux, where the kernel refused the sandbox its user and network
namespaces. On Ubuntu 23.10 and later this is AppArmor's
`kernel.apparmor_restrict_unprivileged_userns`.

Fix: run `nvx doctor`. It names the cause and the ways forward.
`sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0` turns the
restriction off for every program on the machine. Setting
`isolation.network.mode` to `open` gives up the egress allowlist. `nvx
--no-sandbox` runs one command uncontained.

## A command that wrote where it cannot keep files

A command started in your home folder, above it, or inside nvx's own folder runs
in a temporary folder in the sandbox. If it wrote files there, nvx names them,
deletes them and exits 77.

Fix: run the command from a project folder.

## A flag that went to the wrong program

```text
-y was passed to npm, not to nvx, so it does not approve nvx's checks.
```

nvx reads its own flags only before the command.

Fix: `nvx -y npm install ...` or `NVX_YES=true npm install ...`.

## A refusal under agent mode

```text
--agent-mode is set, so nvx refuses instead of asking
```

`--agent-mode` or `NVX_AGENT_MODE=1` turns every question into a refusal, and
`-y` and `NVX_YES` do not override it. The refusal also prints the policy line for
the check that stopped.

Fix: the person adds that line, or unsets `NVX_AGENT_MODE` for their own terminal.

## Undo what you allowed

| You ran | Undo with |
| --- | --- |
| `nvx trust <file>` or `nvx trust --tool <name>` | `nvx grants reset`, in the project folder. `--all` covers every project |
| `nvx allow-host <host>` | `nvx allow-host --remove <host>`, which edits the same file |
| A line in `~/.nvx/policy.json` | Delete the line |
| `NVX_YES`, `NVX_AGENT_MODE` or `NVX_TRUST_YES` in a profile | Unset it. `nvx doctor` names the ones that are set |
| `nvx install <version>` | `nvx uninstall <version>` |

`nvx grants list` shows what is trusted for the current project. On Windows,
`nvx grants reset` also puts back the permissions of the `.env` files nvx hid.
