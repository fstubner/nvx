# nvx — product contract

Written 2026-08-18, by the same context that built the current containment
work. That is a conflict of interest: an acceptor should treat everything below
as a claim to check, not as ground truth. Where a statement is already backed by
a test, the test is named so it can be run rather than believed.

## Purpose

Make the default JavaScript developer workflow safer against supply-chain
attacks, without asking the developer to change how they work.

nvx manages Node.js and Bun versions like nvm or fnm, and — because it is
already on `PATH` intercepting `npm`, `npx`, `yarn`, `pnpm`, `bun` and `bunx` —
uses that position to audit what gets installed and to contain the commands that
execute untrusted code.

The security layer is the reason to switch; the version manager is how it earns
a place on `PATH` in the first place. If nvx is slower or more annoying than fnm
at the version-management job, the security layer never gets installed.

## Users

- **A developer on Windows, macOS or Linux** who installs npm packages daily and
  has no interest in configuring a sandbox. They get containment because it is
  the default, not because they asked for it.
- **A developer running AI coding agents** that execute terminal commands in
  their workspace. The agent installs packages without a human reading each one
  first. nvx contains those installs with no agent-side configuration.
- **Not a target:** anyone wanting a package manager, a dependency resolver, a
  lockfile tool, or a malware scanner. nvx is none of those.

## Success

A developer can complete this, on any of the three platforms, without reading
documentation:

1. Install nvx, open a new shell, `nvx install 22` and `nvx use 22`.
2. `npm install <package>` in a project. It completes normally, at a speed they
   would not think to complain about.
3. A malicious `postinstall` in that package **cannot** read `~/.ssh`, `~/.aws`
   or `~/.npmrc`; **cannot** read or write any other project on the machine; and
   **cannot** open a network connection to a host outside the policy allowlist,
   including by ignoring `HTTP_PROXY`.

   The read half of that is Windows and Linux only. macOS allows filesystem
   reads, so on macOS step 3 means write containment and egress control and not
   credential protection — a narrower product, stated here rather than left for
   a reader to discover in a footnote.

Step 3 is the product. Steps 1 and 2 are the price of admission — if either is
slow or fails on a normal machine, step 3 never happens because nvx is not
installed.

**Honesty condition, equal in weight to the above:** every containment claim
made in `README.md` or `SECURITY.md` is either backed by a test that fails when
the claim stops holding, or is listed under Known limitations. A claim that is
merely intended is a defect of the same severity as a missing control — this
product has shipped documentation describing protections it did not have, twice.

## MVP

- Install, list, switch and pin Node.js and Bun versions; auto-switch on `cd`.
- `PATH` shims that intercept the npm-family commands, with `nvx doctor` to
  diagnose interception.
- Pre-install checks: typosquat detection, OSV vulnerability lookup,
  release-age warning, install-script prompts.
- OS-native containment for installs and ad-hoc tool runners: AppContainer,
  Landlock + namespaces + seccomp, Seatbelt. Scrubbed environment, throwaway
  `HOME`, writes confined to the project.
- Egress mediated by a policy allowlist, enforced by the OS rather than by the
  contained process choosing to cooperate.
- Fail closed: if a containment primitive is unavailable, refuse to run rather
  than running unprotected.

Deferred with intent, not built:
- Containment of the developer's own code (`npm run build`, `node`) at the
  default isolation level. Opt in with `isolation.level: strict`.
- Hiding a `.env` inside the project from a contained install.
- Signature verification of runtime downloads, beyond same-origin checksums.

## Constraints

- **Zero runtime dependencies, one static binary.** No Node, Python or shell
  runtime required to run nvx itself.
- **No elevation.** Containment must work for an ordinary user account. An
  elevated `nvx setup` may add optional conveniences; it may not be required for
  any security guarantee.
- **Overhead must stay invisible.** nvx sits in front of every npm invocation;
  measured dispatch overhead is ~3 ms Linux, ~4 ms macOS, ~38 ms Windows.
- **Pre-1.0.** Breaking changes are acceptable between minor versions; silently
  weakening a documented security guarantee is not.
- **Three platforms are not equal, and the differences are published.** Windows
  containment is measured by replaying the attacks. Linux and macOS each run an
  enforcement probe on a hosted runner of that OS, asserting what must be denied
  and what must still be allowed — a sandbox that refuses everything fails them,
  which is the failure mode a denial-only check cannot see. **macOS does not
  contain reads**, and that is asserted rather than merely admitted, so tightening
  the profile fails CI and forces the documents to move with it.

  Earlier versions of this constraint were wrong in opposite directions: until
  2026-08-20 it called macOS egress "cooperative" when the profile is `(deny
  default)`; until 2026-08-23 it called macOS unverified at runtime after a macOS
  runner had begun proving otherwise; and until 2026-08-24 it listed an
  allowlisted host, UDP, and failing closed without `sandbox-exec` as untested
  after all three had started passing. What remains unclaimed is narrower again:
  which layer refuses the outbound connection the probe observes being refused.

  `docs/enforcement-matrix.md` is the authority; where it and this document
  disagree, that matrix is right and this file is stale.

## Anti-goals

- Resolving dependencies or writing lockfiles.
- Certifying that a package is safe. The checks reduce risk; containment is the
  backstop, and neither is a guarantee.
- Requiring configuration to be secure by default.
- Any security claim that cannot be demonstrated by running something.
