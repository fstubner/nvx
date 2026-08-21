# Review: "Next-Gen NVX Architecture — Autonomous Implementation Plan"

**Date:** 2026-08-21
**Reviewing:** `IMPLEMENTATION_PLAN.md` (repository root, untracked at the time of
review). Quoted where it matters so this stands alone.
**Verdict:** Do not execute as written. Two components are sound and separable,
one is architecturally impossible as described, and the headline component would
silently remove the two security properties 0.5.0 and 0.5.2 exist to provide.
That last point was the review's one unmeasured claim; it has since been measured
and holds — see *The experiment* below.

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

- Restricted tokens do not restrict the network. Measured after this review was
  first written, by the experiment it recommended
  (`TestRestrictedTokenNetworkBehaviour`) — see below.

Reasoned from mechanism, **not** measured:

- `CreateRestrictedToken`'s restricting-SID list might reproduce per-project
  identity. Unproven.

## The experiment: restricted tokens and egress

The review's closing recommendation was one experiment. It has now been run, on
this machine, in four token shapes. In each, the child dials `1.1.1.1:443`
directly — a literal address, so nothing depends on name resolution.

| Token | Outbound TCP | DNS | Read `~/.npmrc` |
|---|---|---|---|
| `DISABLE_MAX_PRIVILEGE` (the plan's proposal) | **connected** | blocked | allowed |
| `LUA_TOKEN` | **connected** | blocked | allowed |
| `WRITE_RESTRICTED` + restricting SID `S-1-5-12` | **connected** | blocked | allowed |
| restricting SID `S-1-5-12` alone | failed | failed | — |

The baseline for comparison, already in this suite: inside an AppContainer with
no network capability, the same dial gives `EACCES`.

**Three of four shapes reach the internet, including the one the plan proposes.**
The mechanism reasoning was right: an outbound socket is not a securable object,
so a token that narrows access checks has nothing to deny.

The fourth is not the exception it looks like. It did not block the connection —
it broke the process: `winapi error #10093` (Winsock never initialised) and a
panic loading `iphlpapi.dll`. A token that cannot load a system DLL cannot run
npm either. The probe classifies that case separately for exactly this reason;
counting it as "blocked" is how this experiment would have produced the opposite
answer.

**The lost DNS is worse than no containment, not partial containment.** Under
every working shape, name resolution fails while raw connectivity survives.
Legitimate tooling breaks (npm resolves registry hostnames) and an attacker does
not (hard-code an address). Enforcement that stops the honest caller and not the
dishonest one is the shape of a security claim that is not one.

**Conclusion: Component 1 cannot deliver unprivileged egress enforcement.** The
question the recommendation held it on — *what enforces egress after the swap?* —
has an answer, and it is "nothing".

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

**The async-pipe gap now has a route, and it does not need this trade either.**

This review originally called async piped stdio the one honest gap, on the
grounds that the supervisor is inside the container and so cannot create a named
pipe either. That reasoning was sound and the conclusion was wrong, because
nobody had checked the other direction. Creating a pipe instance and opening an
existing one are different access checks: creation goes against
`\Device\NamedPipe`, which nvx cannot grant per-container, while opening goes
against the pipe's own security descriptor, which the *parent* chooses.

Measured by `TestAppContainerCanConnectToAParentCreatedNamedPipe`:

| Pipe's DACL | Contained process |
|---|---|
| default security | denied |
| package SID alone | denied |
| ALL APPLICATION PACKAGES alone | denied |
| Everyone alone | denied |
| Everyone + ALL APPLICATION PACKAGES | **opens, full round trip** |
| Everyone + that container's package SID | **opens, full round trip** |

The four single-ACE rows are why this was nearly missed a second time. An
AppContainer's access check needs the DACL to grant the user identity *and* the
package identity; any one ACE alone reads as a flat denial and looks exactly like
a device-level refusal. The first version of this probe had only those rows and
would have reported a false negative.

So the shape is: **the parent creates the pipes and the container only opens
them.** No stream emulation, no brokered handle inheritance, and the last row
matters most — naming that container's package SID rather than
`ALL APPLICATION PACKAGES` keeps the pipe openable by one sandbox only, so
per-project identity survives.

Not yet established: whether node can be made to adopt such a pipe as child
stdio in practice (the preload would have to hand libuv a handle it did not
create), and the probe granted `Everyone` where production should name the
owning user's SID. Feasibility of the OS mechanism is settled; the integration
is not.

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

**Drop Component 1.** It was held on one question — *after the swap, what enforces
egress, without administrator rights?* — and the experiment above answers it:
nothing. A process under the token shape the plan proposes reaches the internet
directly. The swap would trade the product's central security claim for a
convenience that, for dev servers, has been shown obtainable without it.

The async-pipe half of the motivation stays unsolved, and is now the honest
statement of the limitation rather than a problem waiting on this plan.
