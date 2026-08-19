# Security Policy

## Supported versions

nvx is pre-1.0 software. Security fixes are applied to the latest tagged
release and the `main` branch. Older tags do not receive backports.

| Version        | Supported          |
| -------------- | ------------------ |
| latest release | :white_check_mark: |
| `main`         | :white_check_mark: |
| older tags     | :x:                |

## Reporting a vulnerability

**Please do not open a public issue for security vulnerabilities.**

Report privately via GitHub Security Advisories:
[**Report a vulnerability**](https://github.com/fstubner/nvx/security/advisories/new).

Please include:

- the nvx version (`nvx version`) and your OS/arch,
- a description of the issue and its impact,
- reproduction steps or a proof of concept,
- any relevant policy files (`.nvx-policy.json`) or `~/.nvx/audit.log` excerpts.

We aim to acknowledge reports within 5 business days and to provide a
remediation timeline after triage. Coordinated disclosure is appreciated;
we will credit reporters who wish to be named once a fix ships.

## Threat model

nvx is designed to make the *default* developer workflow safer against
**supply-chain attacks in the JavaScript/runtime ecosystem** — malicious or
compromised packages executed via `npm`/`npx`/`yarn`/`pnpm`/`bunx` install
and run scripts. Its defenses are layered:

1. **Runtime integrity** — runtime downloads (e.g. Node.js) are verified
   against the publisher's `SHASUMS256.txt` over HTTPS before use. Archive
   extraction is protected against zip-bombs and path/symlink traversal.
2. **Supply-chain checks** — typosquatting detection, OSV vulnerability
   lookups, package release-age warnings, and install-script prompts run
   before untrusted code executes.
3. **Process isolation** — shimmed commands run inside an OS-native sandbox
   (Windows AppContainer, Linux Landlock + network namespace + seccomp,
   macOS Seatbelt) with a scrubbed environment and filesystem writes confined
   to the working directory and an ephemeral guest home.
4. **Egress control** — outbound network access is mediated by a loopback
   allowlist proxy; unknown hosts are denied or prompted (fail-closed when
   non-interactive).

**Design stance:** security-relevant failures **fail closed**. If a sandbox
primitive is unavailable or a policy cannot be parsed, nvx refuses to run the
command rather than running it unprotected.

## Known limitations

These are deliberate, documented trade-offs — not undisclosed weaknesses:

- **Same-origin checksums.** Runtime archives and their `SHASUMS256.txt` are
  fetched from the same publisher over HTTPS. This detects corruption and
  tampering in transit but is not an independent second-channel signature
  (e.g. GPG). Independent signature verification is on the roadmap.
- **Network enforcement is weakest on macOS.** On Linux a loopback-only network
  namespace plus seccomp genuinely block raw sockets and non-proxied DNS; on
  Windows the AppContainer holds no network capability, so the OS refuses direct
  connections and DNS does not resolve. On both, the egress proxy runs outside the
  containment and is reached over a UNIX socket.
- **On macOS, egress control is cooperative.** It relies on the child honoring
  the injected proxy environment variables; a process using raw sockets can
  bypass the allowlist.
- **On Windows, a loopback exemption left by a pre-0.5.0 `nvx setup` opens every
  service on 127.0.0.1** to contained code, whatever the allowlist says — local
  databases, daemon ports, other dev servers. 0.5.0 never registers one and
  removes it during `nvx setup`, but that command needs an Administrator terminal
  and is otherwise no longer required, so on an upgraded machine the exemption
  simply persists. nvx cannot remove it without elevation; it warns on every
  affected launch and `nvx doctor` reports it, both printing the removal command.
  While it is registered, treat the egress allowlist as unenforced: only *direct*
  connections to other hosts stay blocked, and any reachable loopback service that
  forwards traffic (a debugging proxy, `ssh -D`, a dev-server proxy route) makes
  egress arbitrary.
- **Windows egress was not restricted at all before 0.5.0.** Earlier versions
  granted the sandbox the `internetClient` capability *and removed the proxy
  environment variables*, so a contained process connected directly and the
  allowlist was never consulted — not even cooperatively. Measured on 2026-08-18
  against 0.4.0: a postinstall script reached `1.1.1.1:443` and
  `registry.npmjs.org:443` directly. If you are running 0.4.0 or earlier on
  Windows, treat egress as unrestricted; filesystem containment, environment
  scrubbing and the pre-install checks were unaffected. See
  `docs/enforcement-matrix.md`.
- **A `.env` inside the project is readable by a contained install.** The project
  directory has to be readable for an install to work, and `.env` lives in it.
  Environment *variables* are scrubbed; a file is a file. Secrets outside the
  project — `~/.ssh`, `~/.aws`, `~/.npmrc` — stay unreachable.
- **Directory names outside the project are visible on Windows, contents are not.**
  A contained process can list your home directory, `C:\Users` and `C:\`, which is
  enough to learn which credential stores exist. The profile root carries an ACE
  Windows ships for all AppContainers, which nvx cannot revoke. Where an elevated
  `nvx setup` has run, `C:\`, `C:\Users` and the profile root also carry a
  read+execute grant nvx added itself; `nvx setup --undo` removes those.
- **Projects granted by nvx before 0.5.0 remain reachable.** Every sandbox shared
  one identity until 0.5.0 and the permissions were never revoked, so a project you
  previously used nvx in is readable and writable from any sandbox until nvx runs
  there again and cleans it. nvx keeps no record of where it has run, so it cannot
  sweep them for you. See README.md for the manual command.
- **A contained process cannot capture a child's output on Windows.** An
  AppContainer may not create a named pipe, which is how Windows implements piped
  child stdio, so a contained program that captures a subprocess's output hangs.

  **This does affect some npm installs, and this document said otherwise until
  2026-08-19.** nvx makes npm inherit stdio for lifecycle scripts, which fixes
  npm's own piping — but not a postinstall script that captures a subprocess
  itself. `esbuild` is the widely-used example: its postinstall calls
  `execFileSync(..., { stdio: "pipe" })`, and `npm install esbuild` inside the
  sandbox hangs indefinitely with no error. Measured against `esbuild@0.28.2`;
  uncontained the same install takes 8 seconds. nvx prints a diagnostic hint when
  a contained install runs unusually long, but cannot fix the underlying
  restriction. Workaround: install that package with `--no-sandbox`.
- **Docker provider allowlist is cooperative.** Under the `docker` isolation
  provider, `network.mode: offline` is enforced via `--network none`, but
  proxy-mode allowlisting is cooperative only and therefore disabled by
  default for that provider.
- **nvx is not a malware scanner.** The supply-chain checks reduce risk from
  common attack patterns; they do not guarantee detection of a determined,
  novel attacker. Treat nvx as defense-in-depth, not a guarantee.

## Scope

In scope: sandbox escapes, policy-bypass or policy-tampering vectors,
egress-allowlist bypasses, checksum-verification bypasses, and privilege
escalation caused by nvx.

Out of scope: vulnerabilities in Node.js or other managed runtimes
themselves, in npm packages, or in the operating system's sandbox primitives.
