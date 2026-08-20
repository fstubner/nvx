# Running MCP servers contained — Design

**Date:** 2026-08-20
**Status:** Proposed. Nothing here is implemented; README and SECURITY.md must not
claim MCP support until it is.

## Context

An MCP server is third-party code that runs unsupervised, for hours, with the
developer's full permissions. It is the same threat shape as a postinstall script
— the case nvx exists for — except long-lived and started automatically. The
machine this was designed on runs 29 of them.

nvx already names this user in `PRODUCT.md`: *"a developer running AI coding
agents that execute terminal commands in their workspace."* MCP servers are that
surface, and nothing contains them today.

There is prior history. `2026-07-20-daemon-capable-sandbox-design.md` opens with
nvx melting this machine when MCP servers routed through its shim: ~70s in sandbox
setup, killed by the client's connection timeout, orphaned process trees
accumulating across respawns. Parts 3 of that spec (stdio, Job Object reaping,
latency) shipped and the acute failure is gone. Part 1 (scope containment to
project directories) did not ship, and this design deliberately does not want it —
see Conflicts.

## What was measured

All figures Windows, 2026-08-20, against `@upstash/context7-mcp` (2,552 files in
`node_modules`), driving the real JSON-RPC handshake rather than checking the
process was alive.

**It already works.** A real MCP server runs contained under nvx today: handshake
completes, `tools/list` returns its 2 tools.

| | |
|---|---|
| nvx AppContainer setup, warm | **373 ms** (SID 23, filesystem prep 195, command staging 101, network+supervisor 54) |
| Contained trivial command, total | 532 ms vs 155 ms uncontained |
| Module load, uncontained warm | 430–620 ms |
| **Module load, contained warm** | **456–483 ms** |
| Module load, contained, first run on freshly-installed files | 5,828 ms |
| Same files, second run | 461 ms |
| Memory | nvx parent 15.9 MB + supervisor 8.5 MB, against node's 33.5 MB |

**Steady-state containment costs ~370 ms and ~24 MB. It does not cost 8 seconds.**

### Two corrections, recorded because they nearly drove the wrong design

An earlier pass in this investigation reported the contained module load as 8,137 ms
against 1 ms uncontained. Both numbers were wrong:

- The 1 ms "uncontained" baseline was a caught exception. The test used a
  Unix-style path inside `node -e`; node threw, a `try/catch` swallowed it, and the
  timer measured the throw.
- The 8,137 ms was real but **cold** — measured immediately after `npm install`, so
  it timed 2,552 files being read for the first time by a sandboxed process
  (Defender scanning fresh files, cold filesystem and ACL caches). It reproduces
  exactly on a fresh install and disappears on the second run.

The corrected multiplier is ~1×. On the strength of the wrong figure the plan was
about to be a shared-proxy daemon to amortise per-launch overhead; that would have
saved ~16 MB per server and addressed nothing, because the time was never in nvx's
processes. Measuring first is what caught it.

`--preserve-symlinks`, which nvx already injects, is separately worth 10× on this
package (6,050 ms plain vs 529 ms with the flags, uncontained). That existing
choice is doing real work.

## Non-goals

- **Automatic detection of MCP servers.** They are launched with absolute paths
  (`C:\Users\<user>\AppData\Roaming\nvm\<ver>\node.exe …`), so nvx's PATH
  interception never sees them. Nothing passive can contain them, and a heuristic
  that tried would be guessing at process identity. Containment is opt-in by
  config, which is also the honest shape: the user decides which servers to
  contain.
- **A shared proxy daemon.** Ruled out by measurement above, not by argument.
- **Containing servers that bind a port or drive a browser.** Structural, see below.
- **Changing the default profile.** The existing one already works.

## The two structural constraints

**1. A contained server's port is unreachable from the host.** Windows refuses
connections *into* an AppContainer from outside it — the same restriction the
egress relay exists to work around, and unaffected by the loopback exemption
(measured 2026-08-20, now in README Known limitations). Any MCP server that serves
a port, drives a local browser over CDP, or is reached by anything other than its
own stdio cannot be contained on Windows. On this machine that is Playwright and
chrome-devtools.

This is not a bug with a fix. It is the boundary, and the design says so rather
than shipping something that half-works.

**2. Launches bypass nvx entirely.** See non-goals. The client config must name
nvx explicitly.

## Design

### Scope

Contained: MCP servers that speak **stdio only** and act as **network clients**.
Not contained, and documented as unsupported: servers that bind a port or drive a
local browser.

### Invocation

Opt-in per server, by changing the MCP client's config to route through nvx:

```json
{ "command": "nvx", "args": ["npx", "-y", "@scope/some-mcp-server"] }
```

No new nvx subcommand. `npx` is already classified as an ad-hoc tool runner and
already contained, so this needs no new containment path — which is why the
measurement above works today with no code changes.

### Profile

**Reuse the existing default**: writes confined to the working directory and a
throwaway home, egress restricted to the policy allowlist, environment scrubbed.
Verified sufficient for a real stdio-and-network server.

This follows from `PRODUCT.md`'s anti-goal, *"requiring configuration to be secure
by default"* — a deny-all profile needing per-server allowlisting before anything
works is configuration-first, and would be abandoned. A server needing broader
access uses the existing policy mechanism rather than a new one.

### What still needs building

1. ~~A cold-start note.~~ **Done 2026-08-20.** README's Known limitations now
   records the one-off cost and the warm-up advice, written as the general
   property it is (any first contained run after an install) rather than as an
   MCP claim, since MCP support is not yet claimed anywhere.
2. ~~A test.~~ **Done 2026-08-20.** `TestContainedMcpServerCompletesHandshake`
   drives a real handshake against a contained minimal server through the real
   binary, and asserts containment was applied. It deliberately does not install
   from npm: a test needing the network to prove a local property fails for
   reasons unrelated to the property.

   One negative result came with it, and was then investigated -- see below.
3. **Docs.** README and SECURITY.md gain the supported/unsupported split above.
   Not before the rest lands.

## The F46 stdio guard, investigated

Trying to prove the new test caught the F46 regression showed it did not, and the
investigation found the gap is older and wider than the new test.

**Neither existing test guards nvx's actual use of the fix.**

- `TestPipedStdioReachesChildOnlyWhenBothFlagsSet` proves the Win32 semantics --
  `STARTF_USESTDHANDLES` and `bInheritHandles` are both required -- but builds its
  own `STARTUPINFO` from test parameters via `spawnWithStdout`. It never calls
  production code, so it cannot fail if nvx stops setting the flags.
- `TestPipedStdioReachesRealAppContainerChild` does drive the real launcher but
  cannot detect their absence.

**On this host the machinery is not load-bearing.** With the flags disabled, and
then with `prepareInheritableStdio` short-circuited entirely, a contained child
still received piped stdio -- verified in proxy mode (supervisor in the path) and
open mode (nvx launching the child directly), with `AppContainer isolation active`
confirmed in both, and the marker arriving through a real pipe each time.

Two consequences, one of them a user-visible defect:

1. The fallback branch warned *"will not receive stdio"*. That prediction was
   measurably wrong here, so it now says *"may not"*. It is kept as a warning
   rather than deleted: F46 was a real measured failure where every MCP server
   broke, so the honest statement is that it might break, not that it has.
2. `TestPrepareInheritableStdioReportsSuccessInANormalEnvironment` now guards the
   part that *is* guardable -- that nvx still tries. It fails if the decision
   function is deleted or short-circuited, which is the realistic regression and
   exactly what was simulated to expose this gap.

**Not removed.** The machinery being inert on one Windows 11 build is not evidence
it is inert everywhere, and F46 came from a measured failure. Removing it on this
evidence would be trading a real guard for a small simplification.

**Still open:** why a contained child receives piped stdio with `bInheritHandles`
false and no `STARTF_USESTDHANDLES`. Console inheritance is the likely candidate
and is unconfirmed; establishing it would say whether the machinery is dead weight
on modern Windows or load-bearing in configurations not reproduced here.

## Conflicts with the daemon spec

`2026-07-20-daemon-capable-sandbox-design.md` Part 1 scopes containment to project
directories, so a command launched from an arbitrary cwd is not contained. MCP
clients launch from arbitrary cwds — that is precisely how the original incident
happened.

**These cancel.** Part 1 makes MCP servers cheap by not containing them; this
design contains them on purpose. If Part 1 is implemented it needs an explicit
exception for a command invoked as `nvx …` rather than through a PATH shim: an
explicit invocation is a request for containment, not an accident of PATH.

Part 1's original justification also weakened once its own root causes were fixed:
it existed to stop MCP servers hanging in sandbox setup, and setup is now 373 ms.

## Open questions

- **Does this hold for heavier servers?** Measured on one 2,552-file server.
  Playwright and chrome-devtools are far larger — but both are excluded by
  constraint 1 anyway, so the question is whether a *large stdio-only* server
  exists and behaves.
- **Cold-start against real client timeouts.** ~6 s one-off is fine against a 60 s
  timeout and marginal against 10 s. Unmeasured against any actual client.
- **macOS and Linux.** Everything here is Windows. The port-binding constraint is
  an AppContainer property and may not apply to Landlock or Seatbelt, which would
  make the supported slice *wider* there. Unverified, and per the enforcement
  matrix macOS is unverified at runtime generally.
