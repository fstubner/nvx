---
title: Policy
description: The global and project policy files, how they merge, and every setting they accept.
---

Corporate policies can be defined globally in `~/.nvx/policy.json` and customized per-project via `.nvx-policy.json`.

Policies cascade. The global policy applies everywhere, and project policy files merge over it as you get closer to the working directory. The nearest policy wins on conflicting settings, and blocklists, trusted packages and `allow_hosts` are combined. A global policy that sets `"enforced": true` is a baseline. A project file may tighten it, and nvx refuses a project file that loosens it.

An example global policy:

```json
{
  "blocked_packages": ["rimraf", "malicious-pkg-*"],
  "enforce_ignore_scripts": false,
  "typosquatting": {
    "enabled": true,
    "max_distance": 2,
    "trusted_packages": ["my-internal-helper"]
  },
  "release_age": {
    "enabled": true,
    "min_age_hours": 24,
    "trusted_packages": ["chrome-devtools-mcp", "@upstash/*"]
  },
  "install_scripts": {
    "trusted_packages": ["esbuild", "sharp"]
  },
  "vulnerabilities": {
    "allowed_advisories": ["GHSA-xxxx-yyyy-zzzz"],
    "min_severity": "high"
  },
  "runtime": {
    "default": "node",
    "versions": { "node": "20" }
  },
  "isolation": {
    "enabled": true,
    "filesystem": {
      "provider": "native"
    },
    "network": {
      "mode": "proxy",
      "default_allow": ["registry.npmjs.org:443", "registry.yarnpkg.com:443", "api.osv.dev:443"],
      "allow_hosts": ["localhost:5432"],
      "prompt_unknown": true
    }
  },
  "environment": {
    "isolated_tools": false
  }
}
```

**Prompt behaviour is fixed.** At an interactive terminal nvx asks. With nobody
to answer, it refuses. Some prompts widen nvx's trust boundary, such as one for an
unknown egress host or a project policy that loosens the global one. These
ignore `-y`, `--agent-mode` and `NVX_YES`. Only `NVX_TRUST_YES=true` approves those
without asking (see [Commands](/docs/commands/#policy-files)).

The keys
`prompts.interactive`, `prompts.non_interactive` and `prompts.network_unknown`
are accepted and ignored, so setting one changes nothing, even to something
stricter. `isolation.filesystem.mode` is not a setting, and a policy naming it
gets an unknown-key warning.

## Reference
* **`enforce_ignore_scripts`**: When `true`, nvx refuses to install a package that has hook scripts (`preinstall`/`postinstall`/`install`). Supply chain attacks often use these to download and execute arbitrary binaries on the host machine. A command that already turns scripts off is not refused, because the package manager runs none of them. nvx counts only the settings each package manager reads. For every one of them that is `--ignore-scripts` on the command line. For npm and pnpm it is also `npm_config_ignore_scripts=true` in the environment or `ignore-scripts=true` in the project `.npmrc`. A run inside the sandbox gets no `npm_config_*` variables, so the environment counts only outside it. yarn reads neither of those. Yarn 2 and later also turn scripts off with `--mode=skip-build` or `enableScripts: false` in `.yarnrc.yml`. nvx counts them when `packageManager` in `package.json` names Yarn 2 or later, or `.yarnrc.yml` sets `yarnPath`, because Yarn 1 ignores both. `enableScripts: false` does not count when `YARN_ENABLE_SCRIPTS` is set to anything but `false`, or a `dependenciesMeta` entry in `package.json` sets `built: true`, because Yarn runs those scripts anyway. `--ignore-scripts=false` does not count. The same holds for the install-script prompt, which is not asked when scripts are off. Otherwise the refusal comes before the package manager starts. Name the package in `install_scripts.trusted_packages` to let it through.
* **Per-check exemptions.** Every install-time check applies to every package
  until a policy names an exception, and each list waives only its own check.
  Naming a package in one never affects another. Adding an entry to any of them is
  a loosening, so a project file doing it needs approval.
  - **`typosquatting.trusted_packages`**: this name is not a misspelling of a
    popular one. Names and globs.
  - **`release_age.trusted_packages`**: skip the cooling-off window for this
    package. Use it for a package that publishes often and is started
    non-interactively, such as an MCP server. An MCP server launched by an
    editor cannot prompt, so a release inside the window stops it starting.
    A version the registry gives no publish time for is asked about the same
    way, because nvx cannot tell its age. If your registry sends no publish
    times, list its packages here by scope, such as `"@your-scope/*"`, or set
    `release_age.enabled` to `false`.
  - **`install_scripts.trusted_packages`**: run this package's install scripts
    without asking, and past `enforce_ignore_scripts`. That is how "block
    install scripts except for these" is written. `esbuild`, `sharp` and
    Playwright fetch a platform binary in theirs. This is the sharpest of these
    exemptions, because it is arbitrary code at install time. Every run that uses
    one says which package it let through.
  - **`vulnerabilities.allowed_advisories`**: accept an OSV advisory you have
    assessed, by ID. The exemption is per advisory, so a finding published after
    your assessment still stops the install. A package OSV lists as malicious,
    with an advisory ID starting `MAL-`, is refused without a prompt. Only an
    entry naming that ID lets it through. A pattern such as `"MAL-*"` does not,
    and neither do `-y`, `--agent-mode`, `NVX_YES` or `NVX_TRUST_YES`.
  - **`vulnerabilities.min_severity`**: `low`, `moderate` (or `medium`), `high` or
    `critical`. Advisories below the floor are reported and do not stop the
    install. Unset by default, which stops on every advisory. An advisory nvx
    could not rate stops the install at every floor. The rating comes from a
    network lookup, and a failed lookup must never be why a finding slipped under
    the line. An unrecognised value is no floor at all, and is reported at load
    time. The floor never applies to a `MAL-` advisory.
* **What the audit log holds for these checks.** Every check above that would
  have prompted is written to `~/.nvx/audit.log`. That holds whether a person answered it,
  `-y`, `--agent-mode` or `NVX_YES` approved it without asking, or nobody was there
  and it was refused. `nvx audit` shows these as `check_approved` and
  `check_refused`, with the check, the package and who answered. They are written
  whatever `NVX_TRACE` says, and an approval nobody was asked about also prints one
  line to stderr. A refusal prints the policy line that settles that one check,
  and names `-y` and `NVX_YES` last, because they approve every check in the run.
* **`isolation.filesystem.provider`**: Where the process runs (filesystem + process boundary). See the [enforcement matrix](https://github.com/fstubner/nvx/blob/main/docs/enforcement-matrix.md) for exact guarantees.
  - `native` (default) uses AppContainer (Windows), Landlock + namespaces (Linux) or Seatbelt (macOS). Zero-config, fail-closed.
  - `docker`: runs in a Docker container, hardened, with `offline` and `loopback` enforced via `--network none`. Requires Docker running. Does not carry `--connect`, and says so when asked. The relay needs a process of nvx's inside the sandbox, and this provider launches the target command as the container's only process.

  Any other name is an error and stops the run. `wsl`, `wslc` and
  `systemd-nspawn` are not providers.
* **`isolation.network.mode`**: How egress is governed.
  - `proxy` (default) uses a parent-process HTTP CONNECT + SOCKS5 proxy with a policy allowlist. Injects `HTTP_PROXY` / `HTTPS_PROXY`.
  - `open`: no egress filtering.
  - `offline`: no network at all.
  - `loopback`: `proxy` mode with one addition. The services on your own
    127.0.0.1 are reachable at their own addresses, over any TCP protocol.
    Allowlisted remote hosts stay reachable through the proxy. On Windows the
    loopback reach covers proxy-aware tools' HTTP and HTTPS traffic only, since
    it comes from nvx's proxy. Selecting it in a project policy is a loosening and
    needs approval.
* **`isolation.network.default_allow`** and **`isolation.network.allow_hosts`**: The hosts a contained process may reach, each written as `host:port`. A host with no port allows every port on it, which is the same as `host:*`. `default_allow` ships with the hosts an install needs, and a project's list replaces it. `allow_hosts` adds to it. A name is matched exactly. `*` stands for a port and for nothing else, so an entry such as `*.example.com` matches no host, not even `example.com`. nvx warns when it loads one. List each host in full, for example `registry.example.com:443`. `~/.nvx/audit.log` records each host a run reaches through these lists, once for each host and port in a run, with the list that allowed it. The prompt never offers a local service or a literal link-local address, such as the cloud metadata address `169.254.169.254`, so only an entry in one of these lists allows one. An entry for `localhost`, `127.0.0.1` or `::1`, like `network.mode: loopback`, makes a client that follows the proxy variables send requests to those addresses to the proxy, which dials the service on your machine. With neither, nvx lists those names in `NO_PROXY`, so a request to one connects directly and reaches only what runs in the sandbox. [Known limitations](/docs/limitations/) has the details.
* **`runtime.versions`**: Pin runtime versions used inside the sandbox (e.g. `"node": "20"`). Inside the sandbox this pin comes before the project's `.nvmrc` or other version file, which comes before the global default. See [which version a command runs](/docs/commands/#which-version-a-command-runs).
* **`environment.isolated_tools`**: When `true`, globally installed npm packages (`npm install -g`) are scoped to the project, in `<project>/.nvx/npm_global`. They are not shared through the active Node version. This lets different projects pin different versions of CLI tools (e.g. `vercel`, `eslint`) without conflicts. Takes effect on the next `nvx use` or directory auto-switch. That directory goes on your PATH, so a project file that turns this on counts as a loosening. It needs the same approval as an egress host.

To override the filesystem provider per shim, run `npm --filesystem-provider=docker install`.

## Corporate networks

**Private registries.** The pre-install checks look each package up on the
registry npm will fetch it from. For a contained install that is what the
project's `.npmrc` says in `registry=` and `@scope:registry=`, because a
contained npm reads nothing else. For a run outside the sandbox,
`npm_config_registry` and `npm_config_@scope:registry` come first, then the
project's `.npmrc`, then `~/.npmrc` (or the file `npm_config_userconfig` names).
The contained npm also needs the registry's host in
`isolation.network.allow_hosts`.

If the registry needs a token to read package metadata, nvx sends the
`//host/:_authToken=` value from your `.npmrc` with its own request. That request
runs outside the sandbox. The token is not put in the sandbox's environment or
written to any log.

`${VAR}` in `.npmrc` is filled in from nvx's environment, as
npm does. Only `_authToken` is read. Without a token, a 401 or 403 counts as a
lookup that failed. nvx asks before going on, and refuses when nobody can answer.

The release-age check reads each version's publish time from the registry's
`time` field. A registry that leaves it out gets every package asked about,
and refused when nobody can answer. List that registry's packages in
`release_age.trusted_packages`, or set `release_age.enabled` to `false`.

**Which hosts the checks contact.** nvx makes these requests itself.

| Host | When | What it is sent |
| --- | --- | --- |
| The package's registry | Every registry package checked | The package name, and your token for that registry if `.npmrc` has one |
| `api.npmjs.org` | The typosquat check, for a public-registry package you chose whose name is close to a popular one | Both names |
| `api.osv.dev` | The advisory scan, for public-registry packages | Each name and version |
| `cdn.jsdelivr.net` | Refreshing the popular-package list, once the cached copy is 7 days old | Nothing about your project |

The typosquat check runs on the names you chose. Those are the packages named
on the command line or, with none named, the dependencies in `package.json`. A
dependency that came in with one of them was named by its author and is not
looked up. Each name is looked up once per run.

`api.npmjs.org` and `api.osv.dev` are asked only about packages from
`registry.npmjs.org`. A package from any other registry skips the typosquat and
advisory checks, and the run prints one line saying so. `~/.nvx/audit.log`
records it as `check_skipped` with `check` set to `public_registry_checks`.

**Upstream proxy.** nvx uses one when its own environment sets `HTTPS_PROXY`,
or `HTTP_PROXY` without it. The egress proxy sends each connection the
allowlist permits through that proxy as a CONNECT tunnel. A user and password in the URL
become its `Proxy-Authorization` header. Hosts in `NO_PROXY` and loopback
destinations are dialled directly.

The allowlist decides first, in nvx, so a
host it refuses is never sent to your proxy. The contained process sees only
nvx's proxy, never yours or its credentials. nvx's own requests, such as the
checks above and runtime downloads, use your proxy as any Go program does.

**Node.js mirror.** `NVX_NODE_MIRROR` replaces `https://nodejs.org/dist` for the
release index, the archives and `SHASUMS256.txt`. `NVM_NODEJS_ORG_MIRROR` and
`FNM_NODE_DIST_MIRROR` are read too, in that order after it. The checksums come
from the mirror, so nvx trusts a mirror as it trusts nodejs.org. Use one you
control, over `https://`. Bun is still downloaded from GitHub.
