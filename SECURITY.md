# Security Policy

nvx makes explicit security claims (supply-chain vetting and OS-level
sandboxing). This document states what nvx does and does **not** protect
against, and how to report vulnerabilities.

## Reporting a vulnerability

**Do not open a public issue for security reports.** Instead:

- Use GitHub's **private vulnerability reporting** ("Report a vulnerability" on
  the Security tab), or
- Email the maintainer at the address on the GitHub profile.

Please include a description, affected version (`nvx version`), platform, and a
reproduction. We aim to acknowledge within 72 hours and to ship or document a
fix (or a clear mitigation) before any public disclosure.

## Supported versions

nvx is pre-1.0 and moves fast. Only the latest tagged release receives security
fixes. Pin a specific release if you need stability.

## Threat model

### What nvx aims to protect against

- **Casual supply-chain footguns** at install time: installing a blocklisted
  package, an obvious typosquat of a popular package, a package with a known OSV
  vulnerability, or one publishing lifecycle scripts you didn't expect.
- **Ambient host exposure** when running package-manager/runtime commands:
  leaking environment secrets (`AWS_*`, `GITHUB_*`, `NPM_TOKEN`, `SSH_*`, …) and
  writing outside the working directory, via allowlist env scrubbing and OS
  sandboxes (Landlock/seccomp/namespaces on Linux, Seatbelt on macOS,
  AppContainer on Windows).
- **Unattended auto-approval**: prompts fail **closed** with no TTY, so CI/agent
  contexts don't silently approve risky operations.

### What nvx does NOT (yet) fully protect against — know these limits

- **A determined, targeted attacker inside a malicious package.** The supply-chain
  checks are largely *advisory* (a prompt you can approve) and heuristic
  (typosquatting by edit distance + download ratio has known bypasses).
- **Freshly-added transitive dependencies.** A **bare `npm install` / `npm ci`**
  (and the equivalent yarn/pnpm restore) scans the full resolved tree from the
  lockfile (`package-lock.json`, `yarn.lock`, or `pnpm-lock.yaml`); explicitly
  named installs scan the named packages. Resolving the *new* transitive tree of
  a freshly-added package before it is written is not yet implemented, and the
  whole-tree scan applies the blocklist + OSV checks (not per-package typosquat/
  signature/age).
- **Package authenticity/provenance.** nvx verifies the npm registry's ECDSA
  signature over `name@version:integrity` for explicitly-named installs (keys
  from the registry, expiry-checked, P-256-pinned; a missing signature when the
  registry publishes keys is treated as tampering). It verifies SHA-256 checksums
  of runtime downloads. It does **not** yet verify Sigstore build provenance/
  attestations, GPG-signed `SHASUMS`, or signatures for the full transitive tree.
- **Windows isolation is best-effort/experimental.** The AppContainer smoke test
  is skipped on GitHub-hosted runners; the process runs at the caller's
  integrity level (no Low-IL token). Do not rely on the Windows sandbox as a
  hard boundary yet.
- **Registry/OSV availability.** By default, if the registry or OSV database is
  unreachable, checks degrade to warnings and the install proceeds. Set
  `"fail_closed": true` in policy to abort instead.
- **Loopback services.** Sandboxed processes may reach host-local (loopback)
  services depending on platform; do not treat loopback as isolated.
- **Hardcoded `/tmp` under Linux Landlock.** Inside the Linux sandbox, `TMPDIR`
  is redirected to a writable per-session guest temp dir, but the real `/tmp` is
  not writable. Well-behaved tools honor `TMPDIR`; a tool that hardcodes `/tmp`
  will get a permission error. This is deliberate (a shared writable `/tmp`
  would weaken isolation) — use `TMPDIR`, or `--no-sandbox` for such a tool.

## Verifying the sandbox (escape assertions)

The sandbox is validated by escape-assertion integration tests
(`sandbox_escape_test.go`): a program run inside the sandbox tries to read a
secret in your real `$HOME`, write a file to the host outside its workdir, and
open a raw TCP connection to the internet — all three must fail, while the
workdir write must succeed. They need a real host with the platform primitive
available (Linux 5.13+ with unprivileged namespaces, macOS `sandbox-exec`, or
Windows AppContainer), Node.js on `PATH`, and network access.

Run them locally:

```sh
# skips if the primitive/tooling is unavailable
go test -tags integration -run Escape -v ./...

# make "unavailable" a hard failure (use on a known-capable host)
NVX_ESCAPE_STRICT=1 go test -tags integration -run Escape -v ./...
```

CI runs these per-OS in `.github/workflows/sandbox-escape.yml` (strict on macOS
hosted runners; self-skipping where hosted runners lack the primitive). Until
these pass strictly on all three platforms, treat sandbox containment as a
design goal, not a proven guarantee.

## Hardening recommendations

- Set `"fail_closed": true` and `"enforce_ignore_scripts": true` in your global
  policy for higher assurance.
- Keep `isolation.enabled: true` (the default) and prefer `network.mode: proxy`
  or `offline`.
- On Linux, run on kernel 5.13+ so Landlock is available (nvx fails closed
  otherwise).
- Do not route untrusted or agent-driven code with access to real secrets or
  production systems through nvx's Windows sandbox until self-hosted validation
  lands.
