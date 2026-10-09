---
title: Configuration
description: The nvx policy file by example, how project and global files combine, and every setting with its default.
---

You configure nvx with a JSON policy file. `~/.nvx/policy.json` applies
everywhere. A `.nvx-policy.json` in a project applies to that folder and the
folders below it. `nvx policy init` writes a project file, and
`nvx policy init --global` writes the global one.

## Allow a host

A contained command reaches only the hosts on the allowlist. To add one, run
this in the project.

```sh
nvx allow-host registry.example.com:443
```

That writes this to the project's `.nvx-policy.json` and trusts the file.
`--global` writes `~/.nvx/policy.json` instead.

```json
{ "isolation": { "network": { "allow_hosts": ["registry.example.com:443"] } } }
```

Write each host in full. `*` stands only for a port, so `*.example.com` matches
nothing. A service on your own machine, such as a local Postgres, needs an entry
such as `"localhost:5432"`.

## Trust a project file

A project file can come from anyone who can open a pull request. When it
loosens a setting, such as adding a host, nvx refuses every command in the
project until you trust it.

```sh
nvx trust .nvx-policy.json --hash <hash>
```

Copy the line from nvx's message. The hash means nvx trusts only the content
you were shown. A file that only tightens settings needs no trust.

## Exempt a package from one check

Each check has its own list. A name in one list skips only that check.

```json
{
  "typosquatting": { "trusted_packages": ["my-internal-helper"] },
  "release_age": { "trusted_packages": ["chrome-devtools-mcp", "@your-scope/*"] },
  "install_scripts": { "trusted_packages": ["esbuild", "sharp"] },
  "vulnerabilities": { "allowed_advisories": ["GHSA-xxxx-yyyy-zzzz"] }
}
```

`install_scripts.trusted_packages` lets that package run code at install time,
so add to it with care.

## Set an organisation baseline

Put `"enforced": true` in the global policy you hand out. A project file may then
tighten any setting and may not loosen one.

```json
{
  "enforced": true,
  "blocked_packages": ["malicious-pkg-*"],
  "enforce_ignore_scripts": true,
  "install_scripts": { "trusted_packages": ["esbuild"] },
  "vulnerabilities": { "min_severity": "high" }
}
```

## How files combine

The global file applies first. Project files merge over it, and the one nearest
the working folder wins a conflict. Lists such as `blocked_packages`,
`trusted_packages` and `allow_hosts` are combined. `nvx policy explain` shows
every value in force and the file it came from.

## Every setting

The last column marks the changes a project file can make only after you run
`nvx trust` on it.

| Key | Default | What it sets | Needs `nvx trust` |
| --- | --- | --- | --- |
| `enforced` | `false` | A global policy with `true` is a baseline a project file may tighten and not loosen | |
| `blocked_packages` | none | Packages and globs nvx refuses to install | |
| `enforce_ignore_scripts` | `false` | Refuse packages with install scripts | Turning it off |
| `typosquatting.enabled` | `true` | The lookalike-name check | Turning it off |
| `typosquatting.max_distance` | `2` | How many edits count as close | Lowering it |
| `typosquatting.trusted_packages` | none | Names that are not misspellings | Adding |
| `release_age.enabled` | `true` | The cooling-off check | Turning it off |
| `release_age.min_age_hours` | `24` | Length of the window | Lowering it |
| `release_age.trusted_packages` | none | Packages that skip the window | Adding |
| `install_scripts.trusted_packages` | none | Packages whose install scripts run without asking | Adding |
| `vulnerabilities.min_severity` | unset, so every advisory blocks | Floor below which advisories are reported and do not block | Raising it |
| `vulnerabilities.allowed_advisories` | none | OSV advisory IDs you accepted | Adding |
| `isolation.enabled` | `true` | The sandbox itself | Turning it off |
| `isolation.level` | `standard` | `strict` also contains your own code | Lowering it |
| `isolation.filesystem.provider` | `native` | `native` or `docker` | Changing it |
| `isolation.filesystem.allow_read_exec` | none | Extra folders a contained process may read and run, never write | Adding |
| `isolation.network.mode` | `proxy` | `proxy`, `loopback`, `offline` or `open` | Loosening it |
| `isolation.network.default_allow` | `registry.npmjs.org:443`, `registry.yarnpkg.com:443`, `repo.yarnpkg.com:443`, `api.osv.dev:443`. A runtime adds its own hosts | Hosts a contained process may reach. A project's list replaces this one | Adding |
| `isolation.network.allow_hosts` | none | Hosts added to the list above | Adding |
| `isolation.network.prompt_unknown` | `true` | Whether `NVX_TRUST_YES` may approve an unknown host. nvx never asks | |
| `isolation.network.expose_ports` | none | Ports a contained server publishes on your loopback, as `in[:host]` | Adding |
| `isolation.network.connect_ports` | none | Services on your machine a contained process may reach, as `host[:in]` | Adding |
| `isolation.environment.allow` | none | Environment variable names a contained process keeps. A name that holds a credential by convention is refused | Adding |
| `environment.isolated_tools` | `false` | Scope global npm installs to the project | Turning it on |
| `runtime.default` | `node` | The runtime a bare command means | |
| `runtime.versions` | none | Pin runtime versions inside the sandbox, such as `"node": "20"` | |

Notes on a few of them:

- `vulnerabilities.min_severity` takes `low`, `moderate`, `high` or `critical`.
  A `MAL-` advisory always blocks.
- `isolation.network.mode` `loopback` also reaches services on your own
  `127.0.0.1`, on Windows only for tools that use the proxy. `open` turns the
  allowlist off.
- The `docker` provider refuses `proxy` mode, so it has no allowlist.
- A project's `default_allow` replaces the global list. Use `allow_hosts` to
  add to it.

## Private registries and proxies

- **Private registry.** Put it in the project's `.npmrc`, and its host in
  `allow_hosts`. A contained npm reads no other `.npmrc` and never gets your
  token. The checks run outside the sandbox and send your `_authToken` for that
  registry with their own lookups.
- **What the checks contact.** The package's registry, `api.npmjs.org`,
  `api.osv.dev`, and `cdn.jsdelivr.net` for the popular-package list. Packages
  from a private registry skip the typosquat and advisory checks, so their names
  never reach a public service.
- **No publish times.** If your registry sends none, list its scope in
  `release_age.trusted_packages`.
- **Upstream proxy.** nvx uses `HTTPS_PROXY`, `HTTP_PROXY` and `NO_PROXY`. The
  allowlist decides first, and the sandbox never sees your proxy or its
  credentials.
- **Node.js mirror.** Set `NVX_NODE_MIRROR` to its `dist` URL. nvx trusts it as
  it trusts nodejs.org.
