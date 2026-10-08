# nvx product contract

Written 2026-08-18, by the same context that built the current containment
work. That is a conflict of interest, so an acceptor should treat everything below
as a claim to check, not as ground truth. Where a statement is already backed by
a test, the text names the test so a reader can run it instead of believing it.

**Except for the next section, which came from the person who wanted the thing.**
Recorded 2026-09-01 in answer to "why did you start building this, who is it for,
what would make it a failure, and is the version manager the price of admission
or the point". Three acceptance passes had each stopped short of SHIP for the
same reason. The thing under review wrote the contract, so "does the
code do what the contract says" was circular. This is the part that is not.

## What it is for, from Felix

**Where it started.** Setting up a new Windows laptop and wanting nvm. nvm does
not run on Windows. nvm-windows is a different project, and Felix recorded it on
2026-09-01 as no longer actively maintained. That stopped being true the next day.
nvm-windows 2.0.0 was released on 2026-09-02 and has had releases since.
The want was a better, more modern nvm that is *truly* cross-platform.

Security came second. Thinking about supply-chain attacks and how much exposure
developers still carry led to sandboxing and to checks like known-vulnerability
lookups. Then bun and deno. If the runtime interface is extensible there is no reason
to stop, so the idea became a polyglot runtime manager with security baked in.
Scope was pulled back deliberately to keep it small.

**Who it is for.** Developers running AI coding agents, and developers who want to
stop worrying about supply-chain attacks or at least heavily mitigate them. It is
also for anyone who installs and manages JavaScript runtimes across projects and
wants a modern implementation of that.

**What would make it a failure.** It fails if it does not actually reduce the
risk of compromise through a supply-chain attack. It also fails if it is not
simple and ergonomic to use. Either one alone is enough.

**The version manager was the initial whole point.**

### Where this contradicts the contract below

That last line reversed what the Purpose section said. The contract said "the
security layer is the reason to switch; the version manager is how it earns a
place on `PATH` in the first place". That put security first, with version
management as the carrier. The person who wanted it says the opposite, with
version management first and security added after.

**Settled 2026-09-02, by Felix.** The version manager is still the main thing,
and security is quickly becoming the second main thing. So the ordering below is
his, not the contract's, and the Purpose section now follows it instead of the
other way round.

**Settled again 2026-10-07, by Felix: sandbox first.** The owner chose to lead
with the sandbox. nvx's pitch, on the README, the site, the npm package and in
the docs, is one sentence: "nvx runs npm install and npx inside an OS sandbox on
Windows, macOS and Linux, so a package cannot read your credentials or reach a
host you did not allow. You and your agent type the same commands." The version
manager is the second sentence. This reverses the 2026-09-02 ordering. Where the
paragraphs below still say the version manager is the main thing, this decision
wins. The consequences listed after them hold under either order.

It stood unreconciled for two weeks. Which of them is true changes what this
project should do when the two conflict, so reconciling them is a decision to
take deliberately. Quietly editing one to match the other would skip that
decision. Three consequences follow from the answer:

- **Cross-platform parity is a primary goal, not a courtesy.** The origin is
  "nvm does not run on Windows". A platform whose enforcement nobody has ever
  verified is a bigger problem under this framing than under the contract's.
- **`npx` needing elevation sits next to the primary job**, not out at the edge
  of an optional security feature.
- **Ergonomics is a stated failure condition**, so overhead and friction are not
  tradeable against security depth without saying so.

The code check turns up two smaller corrections to the account above. The
second shipped runtime is **bun**, not deno. The shipped set dropped the Deno, Go
and Python providers for focus (see `docs/runtime-providers.md`).
And the extensible interface described as an ambition is real and shipped:
`RuntimeProvider` in `version.go`, with `NodeProvider` and `BunProvider` implementing it.

## Purpose

Make the default JavaScript developer workflow safer against supply-chain
attacks, without asking the developer to change how they work.

nvx runs `npm install` and `npx` inside an OS sandbox on Windows, macOS and
Linux, so a package cannot read your credentials or reach a host you did not
allow. You and your agent type the same commands. It does that from its position
on `PATH`, intercepting `npm`, `npx`, `yarn`, `pnpm`, `bun` and `bunx`, where it
also audits what gets installed. It also manages Node.js and Bun versions like nvm
or fnm.

**The sandbox comes first and the version manager second.** Decided 2026-10-07,
reversing the 2026-09-02 decision that put the version manager first. See "What it
is for, from Felix" above. The version manager is still required, because the
shims that intercept the toolchain are also what run each project's pinned
version.

The difference is which way a conflict resolves. With the sandbox first, a
containment guarantee is not traded away for convenience in the version manager.
Being slower or more annoying than fnm is still a defect, and a platform where nvx
manages runtimes badly is still a failure. Neither is argued away without saying
so in writing.

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

1. Install nvx, open a new shell and run `nvx install lts`. The first version
   installed becomes the default.
2. `npm install <package>` in a project. It completes normally, at a speed they
   would not think to complain about.
3. A malicious `postinstall` in that package **cannot** read `~/.ssh`, `~/.aws`
   or `~/.npmrc`. It **cannot** read or write any other project on the machine.
   It **cannot** open a network connection to a host outside the policy allowlist,
   including by ignoring `HTTP_PROXY`.

   On macOS the read half covers the home directory. The profile denies reads
   there apart from the project, nvx's runtimes and the directories a policy
   names, and allows reads elsewhere on the disk, so a project kept outside the
   home stays readable. That is a narrower product, and this document says so
   instead of leaving a reader to discover it in a footnote.

Step 3 is the product. Steps 1 and 2 are the price of admission. If either is
slow or fails on a normal machine, step 3 never happens because nvx is not
installed.

**Honesty condition, equal in weight to the above.** Every containment claim
made in `README.md` or `SECURITY.md` either has a test that fails when the claim
stops holding or appears under Known limitations. A claim that is merely
intended is a defect of the same severity as a missing control. This product has
shipped documentation describing protections it did not have, twice.

## MVP

- Install, list, switch and pin Node.js and Bun versions, and auto-switch on `cd`.
- `PATH` shims that intercept the npm-family commands, with `nvx doctor` to
  diagnose interception.
- Pre-install checks: typosquat detection, OSV vulnerability lookup,
  release-age warning, install-script prompts.
- OS-native containment for installs and ad-hoc tool runners: AppContainer,
  Landlock + namespaces + seccomp, Seatbelt. Scrubbed environment, throwaway
  `HOME`, writes confined to the project.
- Egress mediated by a policy allowlist, enforced by the OS instead of by the
  contained process choosing to cooperate.
- Fail closed: if a containment primitive is unavailable, refuse to run rather
  than running unprotected.

Deferred with intent, not built:
- Containment of the developer's own code (`npm run build`, `node`) at the
  default isolation level. Opt in with `isolation.level: strict`.
- Signature verification of runtime downloads, beyond same-origin checksums.

## Constraints

- **Zero runtime dependencies, one static binary.** No Node, Python or shell
  runtime required to run nvx itself.
- **No elevation.** Containment must work for an ordinary user account. No
  security guarantee may require it. `nvx setup` is elevated and only removes
  what older versions left.

  **This was recorded as violated on Windows for `npx`, and the evidence did not
  support it.** npm's dependency walker stats every ancestor of its `_npx`
  staging directory. The conclusion drawn was that an AppContainer cannot read
  `C:\Users` without a grant only an Administrator can make, so contained `npx`
  needed `nvx setup`.

  Measured 2026-09-01, on a machine whose earlier elevated setup no longer applied,
  contained `npx` did fail, but not there. The EPERM was on
  `C:\Users\<user>\.nvx\sandbox_home`, inside nvx's own directory, on a path
  `nvx setup` does not grant and never would. npm walks up from the guest home.
  That directory's traverse grant had been recorded as a failed attempt on
  2026-08-29 and was therefore not being retried. The record's thirty-day life is
  how long contained `npx` stayed broken.

  Deleting that one cache entry made
  `nvx npx cowsay hi` run contained and **unelevated** on the same machine, and
  again from a second, unrelated project. The fix is `grantRequiredAncestors`.
  It never skips the chain above the guest home, and it reports a failure there
  instead of remembering it.

  What survives is narrower. Granting a drive root still needs elevation, and a
  project on a volume whose root the sandbox cannot stat will still fail there.
  That much is unchanged and is what `nvx setup` is for. What is no longer
  supported is that contained `npx` requires elevation *as such*. The only
  end-to-end failure ever measured had a different cause and an unelevated fix.
  Whether a remaining case genuinely needs `nvx setup` is untested, and this
  section says so instead of guessing.

  **The paragraph above was wrong too, and for the same reason as the one it
  corrected.** Measured 2026-09-03 with the drive-root grants removed, `npm
  install` on `C:` works. Contained `npx` fails with `EPERM lstat 'C:\Users'`
  from npm's own realpath, which walks every directory above the `npx` cache
  in the sandbox home. Every "npx works unelevated" run behind the paragraph
  above, and behind a commit and README entry made on 2026-09-02, happened
  while `C:\Users` carried the grant. Nobody checked that premise.

  The fix that finally holds without elevation is a preload in every contained
  node process. It answers a stat for the ancestors of the sandbox's own
  working directory and home (`sandbox_walkup_shim.js`). Measured working,
  same project, no grant.

  Measured 2026-10-06 with every drive-root and Users grant removed, contained
  `npx`, `pnpm` and `bun` 1.4.2 install on `C:`. Nothing measured needs the grant,
  so `nvx setup` no longer adds it. It removes what older versions left.

  This constraint has now flipped three times in
  four days. The lesson recorded here is procedural. A claim that something
  works *without* a permission is only measured with that permission absent.

  It fails closed either way, because `npx` refuses to run uncontained. None of it
  weakened a security guarantee.

  Recorded here instead of quietly reworded, because a constraint edited to
  match what the code does stops being a constraint. Measured 2026-08-30, and
  three unelevated escapes failed when tried. The escapes were relocating npm's
  cache into the guest home, adding a package boundary at the guest home root,
  and moving `NVX_HOME` to another volume.

  The conclusion drawn from those three was that the walk fails wherever it
  starts, because the guest home sits under `~/.nvx`. Making that walkable
  would expose nvx's own control plane. **The second half of that is wrong, and it
  is what kept the first half standing for two days.**

  Walking a directory and
  reading it are different rights. The grant nvx makes on `~/.nvx/sandbox_home` is
  `(X,RA)`, meaning traverse and stat the directory itself. That lets npm's walk
  pass through while leaving the directory unlistable, so one session still cannot
  enumerate or read another's. That is the grant already asserted by
  `TestOneSandboxSessionCannotReadAnother`, which passes with it in place. The
  trade the paragraph above declined was never the trade on offer.
- **Overhead must stay invisible.** nvx sits in front of every npm invocation.
  Measured on Windows, `node --version` through the shim takes a median of
  0.101 s against 0.052 s for node alone (21 interleaved runs, 2026-10-08, after
  the housekeeping sweep moved off the end of the command). On Linux the shim
  call measured 0.018 s. macOS has not been measured.

  **Every figure this constraint carried before 2026-09-03 was withdrawn, and the
  reason is worse than the numbers being wrong.** `scripts/bench.py` timed the
  shim as `nvx shim node --no-sandbox -e 0`. nvx does not read its own flags from
  a wrapped command's arguments, deliberately, so that `nvx npx tsc --strict`
  gives tsc its `--strict`. So `--no-sandbox` reached node, which answered
  `bad option: --no-sandbox` and exited 9.

  The wrapped arm timed an
  argument-parsing failure against a real node run, on every platform, for every
  figure ever published: `~38 ms`, then `9–57 ms`, then `1–60 ms`, then
  `~3 ms Linux / ~4 ms macOS`. On Linux the failure is *faster* than starting
  node, so the corrected script reports a negative overhead for the old command.
  That is the tell, and the old script printed it without comment.

  Two changes make that class of error hard to repeat. The script now refuses to
  time any arm that does not exit 0 **and** print a marker from inside the
  runtime. So a harness that cannot tell success from failure can no longer
  publish one as the other. And it alternates the two arms and differences each
  pair, instead of subtracting two independently drifting medians. The old
  design produced 140.0, 147.8, 92.9 and 42.5 ms on four consecutive runs of one
  idle laptop. In those runs the raw baseline alone swung 65→142 ms.

  The figure this constraint stated until 2026-10-08 was about 75 ms on Windows,
  three runs on one machine with medians of 73.8, 74.2 and 77.1 ms. The shim has
  changed since, and the figure above replaces it. The script's own spread check
  refuses a run on a busy machine instead of writing it down. macOS has never been
  measured with a working script.
- **Pre-1.0.** Breaking changes are acceptable between minor versions. Silently
  weakening a documented security guarantee is not.
- **Three platforms are not equal, and the differences are published.** Replaying
  the attacks measures Windows containment. Linux and macOS each run an
  enforcement probe on a hosted runner of that OS. The probe asserts what the
  sandbox must deny and what it must still allow. A sandbox that refuses
  everything fails them, which is the failure mode a denial-only check cannot see.

  **macOS contains reads only under the home directory.** Its probe requires a
  read in the home outside the project to be refused, and the project and the
  runtime to still read.

  Earlier versions of this constraint were wrong in opposite directions. Until
  2026-08-20 it called macOS egress "cooperative" when the profile is `(deny
  default)`. Until 2026-08-23 it called macOS unverified at runtime after a macOS
  runner had begun proving otherwise. Until 2026-08-24 it listed an
  allowlisted host, UDP, and failing closed without `sandbox-exec` as untested
  after all three had started passing. Until 2026-10-06 it left open which
  layer refuses the outbound connection the probe observes being refused. The
  probe now shows a lookup refused at the resolver and a connect to an address
  refused by the kernel.

  `docs/enforcement-matrix.md` is the authority. Where it and this document
  disagree, that matrix is right and this file is stale.

## Where this sits next to an agent sandbox

Docker Sandboxes (launched 2026-01-30) runs a coding agent inside a microVM.
It mounts only the project workspace, the agent cannot reach the host Docker
daemon, and allow and deny lists govern network access. It launched for macOS and
Windows, and Ubuntu 24.04 and later is supported now. It is driven through an
`sbx` CLI. Other agent harnesses are converging on the same shape, and several
now sandbox their own agent's commands, on Windows too.

This is a neighbouring layer, not a competitor, and the distinction is
worth being precise about because it decides what nvx is still for.

**An agent sandbox contains a session. nvx contains a command.** The VM boundary
holds for as long as the agent runs, around everything it does. nvx draws its boundary
around one `npm install`, one `npx`, one postinstall script. It applies
whether or not an agent runs the command, and a developer typing the command
themselves gets the same treatment.

**Inside a microVM, most of nvx's containment is redundant, and that is fine to
say.** The blast radius may already be a disposable VM with no host filesystem and
no host credentials. In that case "a postinstall cannot read `~/.aws`" is a
guarantee something else is already making. A reader who runs their agent that way should
know which parts of this product still earn their place.

### What survives inside an agent sandbox

- **The supply-chain checks.** Isolation says nothing about what was installed. A
  microVM will not tell anyone that a version was published eleven hours ago,
  or that a name is one edit from a popular package. It will not say that an
  advisory exists against the package, or that the package runs an install
  script. Those are decisions about *whether to
  install*, and no boundary answers them.
- **The project is mounted, and the project is the deliverable.** A compromised
  package that writes a backdoor into the workspace has done its work inside the
  VM. That change gets committed and shipped, and the VM being disposable does
  not undo it.
- **Egress.** Whatever the sandbox legitimately holds (private source, a
  resolved secret, a customer record from a dev database) can leave over any
  connection its policy permits. This is why agent sandboxes are adding
  network policy at all. It is also the guarantee nvx enforces per host, at the
  OS, for the command that most wants to phone home.
- **Runtime management.** Version pinning and switching are not security features
  and do not stop being needed.

### The argument that there is nothing left to steal

Secret references, such as 1Password's `op://` and its equivalents, keep
anything sensitive off disk and resolve a secret at the moment of use. Pair a
microVM with them and the case for any of this gets much weaker. That combination
removes the two largest categories outright, credential theft at rest and damage
to the host.

It is a good argument and it is worth stating where it stops.

A secret reference protects a secret **at rest, not in use**. `op run` resolves
`op://vault/item/field` into the environment of a process. From that moment
the plaintext is inside the sandbox, readable by anything else running there,
including the postinstall script that arrived thirty seconds earlier. The
reference moves the exposure from "always" to "while the command runs". That is
a real reduction and is not the same as nothing to steal.

And the things that remain are the things that were reachable on purpose. They
are the source, the resolved secret, the database the developer connected the
sandbox to. `--connect` and `network.mode: loopback` exist because people need
that reach. Docker lists host service access as future work for the same reason. Every such
grant is a hole someone asked for, and the sandbox holding it is the one running
the untrusted code.

**So the honest position is that nvx's filesystem containment is close to
redundant for a developer inside a well-configured agent sandbox with no
standing credentials. Its value is the supply-chain checks, egress control per
host, and the audit trail of what it allowed.** That is a smaller product than the one
this document describes elsewhere, and it is the right one to describe for that
reader. Claiming the full set of guarantees matters equally in both settings
would be the kind of overstatement the honesty condition above exists to prevent.

## Anti-goals

- Resolving dependencies or writing lockfiles.
- Certifying that a package is safe. The checks reduce risk. Containment is the
  backstop, and neither is a guarantee.
- Needing configuration before it is secure.
- Any security claim that cannot be demonstrated by running something.
