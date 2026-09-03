# nvx — product contract

Written 2026-08-18, by the same context that built the current containment
work. That is a conflict of interest: an acceptor should treat everything below
as a claim to check, not as ground truth. Where a statement is already backed by
a test, the test is named so it can be run rather than believed.

**Except for the next section, which came from the person who wanted the thing.**
Recorded 2026-09-01 in answer to "why did you start building this, who is it for,
what would make it a failure, and is the version manager the price of admission
or the point". Three acceptance passes had each stopped short of SHIP for the
same reason — the contract was written by the thing under review, so "does the
code do what the contract says" was circular. This is the part that is not.

## What it is for, from Felix

**Where it started.** Setting up a new Windows laptop and wanting nvm. nvm does
not run on Windows; nvm-windows is a different project and is no longer actively
maintained. The want was a better, more modern nvm that is *truly* cross-platform.

Security came second. Thinking about supply-chain attacks and how much exposure
developers still carry led to sandboxing and to checks like known-vulnerability
lookups. Then bun and deno, and then: if the runtime interface is extensible,
why stop — a polyglot runtime manager with security baked in. Scope was pulled
back deliberately to keep it small.

**Who it is for.** Developers running AI coding agents, developers who want to
stop worrying about supply-chain attacks or at least heavily mitigate them, and
anyone who installs and manages JavaScript runtimes across projects and wants a
modern implementation of that.

**What would make it a failure.** Two things: if it does not actually reduce the
risk of compromise through a supply-chain attack, and if it is not simple and
ergonomic to use. Either one alone is enough.

**The version manager was the initial whole point.**

### Where this contradicts the contract below

That last line reversed what the Purpose section said. The contract stated "the
security layer is the reason to switch; the version manager is how it earns a
place on `PATH` in the first place" — security first, version management as the
carrier. The person who wanted it says the opposite: version management first,
security added after.

**Settled 2026-09-02, by Felix:** the version manager is still the main thing,
and security is quickly becoming the second main thing. So the ordering below is
his, not the contract's, and the Purpose section has been corrected to match
rather than the other way round.

It was left standing for two weeks rather than reconciled, because which of them
is true changes what this project should do when the two conflict, and that is a
decision to take deliberately rather than by quietly editing one to match the
other. Three consequences follow from the answer:

- **Cross-platform parity is a primary goal, not a courtesy.** The origin is
  "nvm does not run on Windows". A platform whose enforcement nobody has ever
  verified is a bigger problem under this framing than under the contract's.
- **`npx` needing elevation sits next to the primary job**, not out at the edge
  of an optional security feature.
- **Ergonomics is a stated failure condition**, so overhead and friction are not
  tradeable against security depth without saying so.

Two smaller corrections to the account above, checked against the code: the
second shipped runtime is **bun**, not deno — Deno, Go and Python providers exist
on a preservation branch and were removed from the shipped set for focus (see
`docs/runtime-providers.md`). And the extensible interface described as an
ambition is real and shipped: `RuntimeProvider` in `version.go`, with
`NodeProvider` and `BunProvider` implementing it.

## Purpose

Make the default JavaScript developer workflow safer against supply-chain
attacks, without asking the developer to change how they work.

nvx manages Node.js and Bun versions like nvm or fnm, and — because it is
already on `PATH` intercepting `npm`, `npx`, `yarn`, `pnpm`, `bun` and `bunx` —
uses that position to audit what gets installed and to contain the commands that
execute untrusted code.

**The version manager is the main thing; the security layer is quickly becoming
the second main thing.** Decided 2026-09-02 — see "What it is for, from Felix"
above, which this sentence used to contradict. It read "the security layer is the
reason to switch; the version manager is how it earns a place on `PATH` in the
first place", written by the same context that built the containment work and
inclined to rate that work first.

The difference is which way a conflict resolves. Version management being primary
means that being slower or more annoying than fnm is a defect in the main job,
not a tax on an optional one, and that a platform where nvx manages runtimes
badly is a worse failure than one where it contains them narrowly. Security being
a close and rising second means it is not a bolt-on either: a containment
guarantee is not traded away for a few milliseconds without that being argued for
in writing.

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

  **This was recorded as violated on Windows for `npx`, and the evidence did not
  support it.** npm's dependency walker stats every ancestor of its `_npx`
  staging directory, and the conclusion drawn was that an AppContainer cannot read
  `C:\Users` without a grant only an Administrator can make — so contained `npx`
  needed `nvx setup`.

  Measured 2026-09-01, on a machine whose earlier elevated setup no longer applied:
  contained `npx` did fail, but not there. The EPERM was on
  `C:\Users\<user>\.nvx\sandbox_home` — inside nvx's own directory, on a path
  `nvx setup` does not grant and never would. npm walks up from the guest home,
  and that directory's traverse grant had been recorded as a failed attempt on
  2026-08-29 and was therefore not being retried; the record's thirty-day life is
  how long contained `npx` stayed broken. Deleting that one cache entry made
  `nvx npx cowsay hi` run contained and **unelevated** on the same machine, and
  again from a second, unrelated project. The fix is `grantRequiredAncestors`:
  the chain above the guest home is never skipped, and a failure there is
  reported instead of remembered.

  What survives is narrower. Granting a drive root still needs elevation, and a
  project on a volume whose root the sandbox cannot stat will still fail there —
  that much is unchanged and is what `nvx setup` is for. What is no longer
  supported is that contained `npx` requires elevation *as such*: the only
  end-to-end failure ever measured had a different cause and an unelevated fix.
  Whether a remaining case genuinely needs `nvx setup` is untested, and this
  section says so rather than guessing.

  **The paragraph above was wrong too, and for the same reason as the one it
  corrected.** Measured 2026-09-03 with the drive-root grants removed: `npm
  install` on `C:` works, contained `npx` fails with `EPERM lstat 'C:\Users'`
  from npm's own realpath, which walks every directory above the `npx` cache
  in the sandbox home. Every "npx works unelevated" run behind the paragraph
  above, and behind a commit and README entry made on 2026-09-02, happened
  while `C:\Users` carried the grant, and nobody checked that premise. The
  fix that finally holds without elevation is a preload in every contained
  node process that answers a stat for the ancestors of the sandbox's own
  working directory and home (`sandbox_walkup_shim.js`); measured working,
  same project, no grant. This constraint has now flipped three times in
  four days. The lesson recorded here is procedural: a claim that something
  works *without* a permission is only measured with that permission absent.

  It fails closed either way — `npx` refuses rather than running uncontained — so
  no security guarantee was weakened by any of it.

  Recorded here rather than quietly reworded, because a constraint edited to
  match what the code does stops being a constraint. Measured 2026-08-30, and
  three unelevated escapes were tried and failed: relocating npm's cache into the
  guest home, adding a package boundary at the guest home root, and moving
  `NVX_HOME` to another volume.

  The conclusion drawn from those three was that the walk fails wherever it
  starts, because the guest home sits under `~/.nvx` and making that walkable
  would expose nvx's own control plane. **The second half of that is wrong, and it
  is what kept the first half standing for two days.** Walking a directory and
  reading it are different rights: the grant nvx makes on `~/.nvx/sandbox_home` is
  `(X,RA)` — traverse and stat the directory itself — which lets npm's walk pass
  through while leaving the directory unlistable, so one session still cannot
  enumerate or read another's. That is the grant already asserted by
  `TestOneSandboxSessionCannotReadAnother`, which passes with it in place. The
  trade the paragraph above declined was never the trade on offer.
- **Overhead must stay invisible.** nvx sits in front of every npm invocation.
  Measured dispatch overhead is **about 75 ms on Windows**, and is not currently
  established on Linux or macOS.

  **Every figure this constraint carried before 2026-09-03 was withdrawn, and the
  reason is worse than the numbers being wrong.** `scripts/bench.py` timed the
  shim as `nvx shim node --no-sandbox -e 0`. nvx does not read its own flags from
  a wrapped command's arguments — deliberately, so that `nvx npx tsc --strict`
  gives tsc its `--strict` — so `--no-sandbox` was passed to node, which answered
  `bad option: --no-sandbox` and exited 9. The wrapped arm timed an
  argument-parsing failure against a real node run, on every platform, for every
  figure ever published: `~38 ms`, then `9–57 ms`, then `1–60 ms`, then
  `~3 ms Linux / ~4 ms macOS`. On Linux the failure is *faster* than starting
  node, so the corrected script reports a negative overhead for the old command —
  which is the tell, and which the old script printed without comment.

  Two changes make that class of error hard to repeat. The script now refuses to
  time any arm that does not exit 0 **and** print a marker from inside the
  runtime, so a harness that cannot tell success from failure can no longer
  publish one as the other. And it alternates the two arms and differences each
  pair, rather than subtracting two independently drifting medians — the old
  design produced 140.0, 147.8, 92.9 and 42.5 ms on four consecutive runs of one
  idle laptop while the raw baseline alone swung 65→142 ms.

  The Windows figure is three runs on one machine: medians 73.8, 74.2 and 77.1 ms,
  p10–p90 roughly 63–93. A fourth run on a busy machine was refused by the
  script's own spread check rather than written down. A Linux container reported
  4–10 ms with the spread swamping it, and the script declined to give a figure;
  macOS has never been measured with a working script. This constraint now states
  one number, for the one platform where a working script has produced a stable
  one.
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
