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

`audit.log` records the working directory of each entry, and the hostnames of
egress decisions. Of a command it records the name, plus its subcommand when
that word is one nvx recognises — `install`, `run`, `publish` and similar. It
records no other argument: not package names, not script paths, not your
project's own script names, not flag values. Anything nvx does not recognise is
dropped rather than guessed at.

Read an excerpt before sending it and redact what you would rather not share: a
working directory or a hostname can name a client or an unannounced product.

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
- **On macOS, enforcement is real but narrower than on Windows and Linux.** The
  profile is `(deny default)` and permits outbound traffic to localhost only, so a
  raw socket to an external host is refused by the kernel rather than merely
  discouraged. A hosted macOS runner confirms this on every CI build
  (`scripts/sandbox-enforcement-macos.sh`): a contained process is denied a write
  outside its project, is denied egress with an empty allowlist, and is still
  permitted to write its own project — the last of those being what distinguishes
  enforcement from a sandbox that has simply failed to start.

  What macOS does not do is contain reads; see the entry below. Two cells stay
  untested there and are not claimed: that an allowlisted host completes through
  the proxy, and that nvx fails closed when `sandbox-exec` is missing.

  This entry has been wrong in both directions. Until 2026-08-20 it said macOS
  egress was cooperative and a raw socket could bypass the allowlist, which
  understated the design and contradicted README's matrix — two shipped documents
  disagreeing on a security question is its own defect. Until 2026-08-23 it then
  said none of it had been verified on macOS hardware, which was true when written
  and outlived the probe that made it false.
- **On macOS, loopback access is now scoped to the mode** (fixed 2026-08-20).
  `proxy` reaches nvx's egress proxy and nothing else on 127.0.0.1; `offline`
  reaches nothing; `loopback` reaches all of it, which is what that mode is for.

  Previously every restricted mode granted `localhost:*`, so contained code could
  reach a local database, daemon port or another project's dev server with no
  allowlist entry, and `offline` was not offline. Any reachable service that
  forwards traffic would have made the allowlist meaningless. This was present
  from the sandbox's first implementation. The fix is to the generated profile.
  What a macOS runner confirms is that egress is denied with an empty allowlist,
  which does not by itself prove the per-mode loopback scoping — nothing stands up
  a loopback listener on macOS and checks which modes reach it.
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
  project — `~/.ssh`, `~/.aws`, `~/.npmrc` — stay unreachable on Windows and
  Linux. **On macOS they do not**: the Seatbelt profile allows filesystem reads
  (see `docs/enforcement-matrix.md` note 2), so macOS contains writes and egress
  but not credential reads.
- **Directory names outside the project are visible on Windows, contents are not.**
  A contained process can list your home directory, `C:\Users` and `C:\`, which is
  enough to learn which credential stores exist. The profile root carries an ACE
  Windows ships for all AppContainers, which nvx cannot revoke. Where an elevated
  `nvx setup` has run, `C:\`, `C:\Users` and the profile root also carry a
  read+execute grant nvx added itself; `nvx setup --undo` removes those.
- **`audit.log` is a record, not evidence against a local attacker.** Anything
  running as you can append to it, and that includes code nvx deliberately does
  not contain: at the default `standard` level your own code — `npm run build`,
  `node script.js` — runs uncontained, so it can write a fabricated
  `"mode":"sandboxed"` entry that `nvx audit` then displays as a genuine
  contained run. Measured; a contained process is refused (`EPERM`), an
  uncontained one is not.

  This is inherent rather than an oversight: the file has to be writable by nvx
  running as you, so it is writable by anything else running as you. Read it as
  what nvx recorded about its own runs, not as proof of what did or did not
  happen on a machine where untrusted code has already executed outside the
  sandbox.
- **Projects granted by nvx before 0.5.0 remain reachable.** Every sandbox shared
  one identity until 0.5.0 and the permissions were never revoked, so a project you
  previously used nvx in is readable and writable from any sandbox until nvx runs
  there again and cleans it. nvx keeps no record of where it has run, so it cannot
  sweep them for you. See README.md for the manual command.
- **A contained process cannot capture a child's output on Windows.** An
  AppContainer may not create a named pipe, which is how Windows implements piped
  child stdio, so a contained program that captures a subprocess's output hangs.

  **Synchronous capture is handled; streaming capture is not.** The restriction is
  on creating a pipe, not on file descriptors, so a preload loaded into every
  contained node process redirects `spawnSync`/`execSync`/`execFileSync` through
  temp files in the guest home. Their contract is "run it, give me the output at
  the end", which a file satisfies exactly — the caller never sees a stream either
  way. `npm install esbuild` works as a result; it previously hung forever.

  Asynchronous `spawn(..., { stdio: "pipe" })` is a real stream that a file cannot
  stand in for, and it is handled differently: nvx creates the pipes outside the
  container and the preload only opens them. Opening an existing pipe is a
  different access check from creating one, and it is permitted when the pipe's
  DACL names both the user nvx runs as and that container's package identity.
  Both endpoints are inside the same sandbox; nvx moves bytes between two of its
  own children, which it already parents. No capability is granted to make this
  work.

  **Another process running as the same user can open these pipes.** That is
  unavoidable rather than an oversight: a contained process's token carries the
  user's identity, so the ACE that admits the sandbox necessarily admits the
  user. Anything already running as you can read the project and the audit log
  regardless, so the pipes sit inside that existing boundary rather than outside
  it — but a second local *account* cannot reach them. This entry claimed the
  stronger "openable by one sandbox and no other" until an acceptance review
  opened one from an ordinary process; the code had in fact granted Everyone,
  which is now the user's SID.

  Writing to a contained child's stdin is not supported: `child.stdin` is `null`.
  Beyond 8 concurrent piped children in a process, output is buffered to a file
  in the guest home and delivered when the stream ends rather than as it is
  produced — available from `stdout` events or `close`, not from an `exit`
  handler.

  nvx's diagnostic hint covers installs only, on purpose: an install still running
  after two minutes is anomalous, while an `npx`-launched dev server running for
  hours is working correctly, so a timer cannot tell the second case from a hang.
  Nothing here affects containment: it changes how a contained process talks to
  its own children, not what it may reach.
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
