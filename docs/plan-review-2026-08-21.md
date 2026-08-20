# Review: "Next-Gen NVX Architecture — Autonomous Implementation Plan"

**Date:** 2026-08-21
**Reviewing:** `IMPLEMENTATION_PLAN.md` (repository root, untracked at the time of
review). Quoted where it matters so this stands alone.
**Verdict:** Do not execute as written. Two components are sound and separable,
one is architecturally impossible as described, and the headline component would
silently remove the two security properties 0.5.0 and 0.5.2 exist to provide.

## How to read the claims below

Measured on this machine during review, and reproducible:

- `ExtractTarGz` / `ExtractZip` are called only from `version.go` and
  `provider_bun.go` — Node and Bun **runtime** archives. nvx never downloads or
  extracts an npm package tarball.
- Job Objects with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` are already implemented
  (`sandbox_reaping_windows.go`).
- A contained process cannot create a named pipe: `CreateNamedPipeW` returns
  `ERROR_ACCESS_DENIED` for three different name shapes
  (`TestAppContainerCannotCreateNamedPipes`).
- **Synchronous** capture already works via the stdio preload shipped in 0.5.1;
  only **asynchronous** streaming still hangs.
- With no network capability granted, a contained process gets `EACCES` on direct
  TCP and `ENOTFOUND` on DNS.
- A host can reach a server inside the AppContainer through an outward tunnel
  while egress stays blocked
  (`TestReverseRelayReachesAServerInsideTheContainer`, written during this
  review).

Reasoned from mechanism, **not** measured — flagged because the plan's fate turns
on the first one:

- Restricted tokens gate access checks on securable objects (files, registry,
  named objects). Outbound sockets are not gated that way, so a restricted-token
  process has no equivalent of AppContainer's network-capability model.
- `CreateRestrictedToken`'s restricting-SID list might reproduce per-project
  identity. Unproven.

## Component 1 — Restricted Tokens + Job Objects

> "This permanently resolves the named-pipe streaming hang and dev-server inbound
> loopback block while maintaining unprivileged sandbox containment."

**It would also remove OS-enforced egress, and the plan never mentions egress.**

AppContainer is *why* the enforcement matrix can say the OS refuses direct
connections: Windows filters by package SID, and nvx grants no network capability.
Restricted tokens have no equivalent. The only unprivileged substitute is
`HTTP_PROXY`, which a package ignores by not reading it — precisely the state
`docs/enforcement-matrix.md` records as "Windows egress became enforced in 0.5.0.
It was not before, and this table claimed otherwise until 2026-08-17."

Filtering by user SID in WFP, or a per-app firewall rule, both require
administrator rights. `PRODUCT.md` constrains: "**No elevation.** ... it may not be
required for any security guarantee."

Second loss: per-project capability SIDs are an AppContainer feature. Without
them, every sandbox shares one identity again — the cross-project read/write
exposure closed in 0.5.0. Restricting SIDs may substitute; nobody has tried.

**The premise is also half-stale.** The plan leads with named-pipe hangs, but
synchronous capture has worked since 0.5.1. What remains is asynchronous
streaming. That is a much narrower prize than the plan assumes, against a much
larger cost than it states.

**The dev-server half does not need this change at all.** Two facts were already
proven in this repository — a contained process can reach the parent over AF_UNIX,
and loopback works inside the container — and together they are everything a
reverse tunnel needs. The contained side dials out and parks connections, the
parent owns the host port, and each inbound request is spliced onto a parked
tunnel. The egress relay's shape, inverted.

`sandbox_reverse_relay_probe_windows_test.go` demonstrates it end to end: a server
bound inside the container answers a request made to a host-side port. The
load-bearing assertion is that the child runs with `scopeCaps` only — no
`internetClient` — and reports `egress=BLOCKED` in the same run that serves the
request. Inbound reachability, egress guarantee intact.

That probe is not a feature: no port configuration, no lifecycle, a fixed pool of
four tunnels, and no answer for a server binding a port nvx was not told about.
It settles feasibility, nothing more.

**Remaining honest gap:** asynchronous piped stdio. The supervisor is inside the
container too, so it cannot create a named pipe either. A preload could in
principle broker pipe handles from the parent over the existing channel, but
emulating stream semantics — backpressure, EOF timing, interleaving — for
arbitrary third-party code is fragile. Worth documenting rather than trading the
sandbox primitive for.

## Component 2 — `.env` cloaking

The goal is legitimate; the design has two problems.

**Windows is missing entirely.** The component specifies Seatbelt and Landlock
changes and no Windows implementation, while `.env` leakage is named as a core
pain point in the plan's own opening paragraph. Windows is the only platform whose
enforcement is actually verified at runtime.

**A filename denylist is not a boundary.** `.env`, `.env.local` and `.git` leave
`.env.production`, `.env.staging`, `config/secrets.json` and anything else a
project happens to use. Shipping it as a guarantee — which Component 5 proposes,
"Document `.env` cloaking guarantees" — repeats the failure this project has spent
several releases correcting: a claim wider than the mechanism.

If it ships, the honest framing is "common secret filenames are hidden from
installs", listed with what it does not cover.

## Component 3 — Streaming tarball scanner

**Architecturally impossible as described.** nvx never sees a package tarball. The
plan directs changes to `ExtractTarGz` / `ExtractZip` in `download.go`; those
handle Node and Bun runtime archives. npm downloads and extracts packages itself,
inside the sandbox. Scanning them would require nvx to proxy the registry or
intercept npm — a substantial architecture change the plan does not describe.

**And it contradicts the product contract.** `PRODUCT.md` lists under *not a
target*: "anyone wanting a package manager, a dependency resolver, a lockfile
tool, or **a malware scanner**", and under anti-goals: "Certifying that a package
is safe. The checks reduce risk; containment is the backstop, and neither is a
guarantee." Parsing install hooks for `child_process.exec` and base64 `eval` is
signature-based malware scanning with a rename.

**Keep the trie.** Replacing the 33-name embedded list with a compact 50,000-name
structure is a real improvement to an existing check, and is independent of
everything above.

## Component 4 — APFS `clonefile`

Self-contained and harmless. Two notes:

- "Unit tests asserting sub-millisecond cloning" will be flaky. Assert that
  `clonefile` was used and the result is correct; wall-clock thresholds fail on
  loaded CI machines for reasons unrelated to the code.
- No evidence macOS staging is a bottleneck. The one staging cost measured in this
  cycle was a Windows cold-cache effect (~6 s once per install, ~0 thereafter).
  Worth measuring before optimising.

## Component 5 — Documentation

> "Update 'Known limitations' to remove the former restrictions on dev server
> loopback access and named pipe streaming on Windows."

**Ordering hazard.** These removals are correct only if Component 1 delivers, and
Component 1's cost is unstated. Executed in sequence by anyone not tracking the
egress regression, the result is documentation claiming more while the product
delivers less — with the enforcement matrix, the file most likely to be trusted,
updated to match the claim rather than the behaviour.

Any doc change here should be gated on a test that asserts egress is still
OS-enforced after the primitive change. The plan's verification section contains
no such gate.

## Verification plan

The four gates cover restricted tokens, `.env`, tarball scanning, and the standard
suite. None asserts:

- that egress remains OS-enforced after the token swap;
- that one project's sandbox still cannot read another's files;
- that the loopback exemption, if present, has not become load-bearing.

Those are the properties most likely to break and least likely to be noticed,
because nothing fails loudly when enforcement silently reverts to cooperative.

## Recommendation

**Proceed with:** the 50,000-name typosquat structure; APFS cloning (after
measuring that staging is worth optimising); the `.env` work, redesigned to
include Windows and scoped honestly.

**Reframe:** Component 3, around what nvx can actually observe — it holds the
registry metadata already and never touches the tarball.

**Hold Component 1** until this is answered: *after the swap, what enforces egress
and per-project identity, without administrator rights?* If there is a good
answer, this becomes a serious proposal and the dev-server and async-pipe wins are
real. If there is not, it trades the product's central security claim for a
convenience that — for dev servers — has now been shown obtainable without it.

The cheapest next step is one experiment: launch a process under a restricted
token and attempt an outbound connection. That settles the question in an
afternoon and is worth doing before either committing to or discarding the
approach.
