# Isolation enforcement matrix

nvx's guarantees differ by platform and by isolation provider. This page states
plainly what is enforced at the OS boundary versus what depends on the child
process cooperating. The design rule is **fail closed**: if a primitive is
missing or a mode cannot be enforced, nvx refuses to run rather than downgrading
silently.

## Providers

- **native** (default, zero-config): Windows AppContainer, Linux Landlock +
  network namespace + seccomp, macOS Seatbelt (`sandbox-exec`).
- **docker**: runs the command in a container. Supported and hardened, but
  requires Docker to be installed and running.
- **experimental** (`wsl`, `wslc`, `systemd-nspawn`): present but require
  `NVX_EXPERIMENTAL=1`. Not covered by the guarantees below.

## Native provider

Cells marked **profile only** describe the generated Seatbelt profile rather than
observed behaviour — see ⁵ for which macOS rows are now confirmed against a
running system and which are not.

| Guarantee | Windows (AppContainer) | Linux (Landlock + netns + seccomp) | macOS (Seatbelt) |
|---|---|---|---|
| Host filesystem write blocked (outside workdir + guest home) | Yes, except pre-0.5.0 projects⁷ | Yes⁸ | Yes⁵ |
| Host filesystem read restricted | Yes⁴ | Yes⁸ | No² (confirmed⁵) |
| Environment secrets scrubbed | Yes | Yes | Yes |
| Egress blocked when the allowlist does not cover the host | Yes³ | Yes⁸ | Yes⁵ |
| Allowlisted host reachable through the proxy | Yes³ | Yes⁸ | Yes⁵ |
| Non-proxied raw TCP/UDP blocked at OS | Yes³ (no network capability) | Yes (loopback-only netns + seccomp) | Yes⁵ (TCP and UDP; UDP refused at bind) |
| Non-proxied DNS blocked | Yes³ | Yes (netns) | Partial¹ |
| Any loopback service reachable | Only with a leftover exemption³ | No (loopback-only netns) | No⁶ (proxy port only) |
| One named host service reachable | Only via `--connect`⁹ | No | No |
| A contained server reachable from the host | Only via `--expose`⁹ | Yes (shared stack, no inbound block) | Yes |
| Fails closed if a primitive is missing | Yes | Yes (Landlock 5.13+, iproute2 for netns) | Yes⁵ (refuses to run without `/usr/bin/sandbox-exec`) |

² On macOS the Seatbelt profile allows filesystem reads. The dynamic linker must
read system libraries and the dyld shared cache, whose locations vary by macOS
version (e.g. the Cryptexes firmlink on Apple Silicon) and cannot be enumerated
reliably; a strict read allowlist breaks process launch. Write containment and
egress control remain enforced, and environment secrets are scrubbed with `$HOME`
redirected to an ephemeral guest profile.

Writes are contained, with named exceptions: the profile grants write access to
`/dev`, `/private/tmp`, `/private/var/tmp` and `/private/var/folders` so a
contained process has somewhere to put temporary files. "Writes cannot leave the
project" is therefore shorthand -- system temp trees are writable, and on macOS
`$TMPDIR` lives under `/private/var/folders`. Found while writing
`scripts/sandbox-enforcement-macos.sh`, whose first version put its
must-not-be-writable fixture in `mktemp -d` and duly reported an escape that was
the profile working as designed.

**That redirection does not protect a file from being read, and this note used to
say it did** -- it claimed "the sensitive material is still protected", which is
true of writes and false of reads. `$HOME` decides where `~` expands to; it does
not stop anything opening `/Users/<you>/.ssh/id_rsa` by absolute path, and a
postinstall script looking for credentials does not need `~` to find them. On
macOS, credential *reads* are not contained. Say so rather than reasoning around
it: the write and egress guarantees are real and the read guarantee is absent,
which is a narrower product than the same sentence describes on Windows and
Linux.

¹ On macOS, egress is gated by the loopback proxy and OS network rules. Linux
additionally removes all non-loopback interfaces (network namespace), so DNS to
external resolvers cannot leave; on macOS a determined process could still
attempt DNS via the OS resolver. This is the main per-OS difference and is why
Linux has the strongest network story.

⁵ **The macOS column is confirmed on macOS hardware, with one stated exception.**
`scripts/sandbox-enforcement-macos.sh` runs on a hosted macOS runner on every CI
build and asserts the denials rather than only that the command ran. A contained
process reports, and CI requires:

```
WRITE_OUTSIDE=DENIED   WRITE_INSIDE=ALLOWED   READ_OUTSIDE=ALLOWED
EGRESS=DENIED          UDP_EGRESS=DENIED      CONNECT=200 (allowlisted host)
```

Two of those are load-bearing in a way the others are not. `WRITE_INSIDE` and
`CONNECT=200` are the positive controls: every denial above them would also be
satisfied by a sandbox that had failed to start, and requiring something to
*succeed* is the only thing that tells enforcement from breakage. `CONNECT=200`
is the one that closed the largest gap here — until 2026-08-24 the whole script
ran with an empty allowlist and could only ever observe refusals.

`READ_OUTSIDE=ALLOWED` pins the documented weakness in ² deliberately. If the
profile is ever tightened this fails, which forces README, SECURITY.md,
PRODUCT.md and this page to be updated in the same change rather than quietly
going wrong in the flattering direction.

`UDP_EGRESS=DENIED` is refused at **bind**, not at send: sending on an unbound
UDP socket makes the runtime bind one implicitly and Seatbelt rejects that with
EPERM on 0.0.0.0. That is stronger than the send-level refusal expected, and it
arrives as an error event rather than a callback error — unhandled, it killed the
probe before it wrote a report, which is how the first version of that check
failed on a real runner instead of recording a pass.

Failing closed without `/usr/bin/sandbox-exec` is asserted by a unit test rather
than by the script, because it needs that file to be absent and it cannot be
removed from the machine under test. `seatbeltExecPath` is a variable so the test
can move it; it asserts both that nvx reports failure and that the command left no
trace, since reporting failure while having run the thing uncontained is the
outcome that would actually matter. The script separately checks `sandbox-exec`
exists and fails loudly if a future runner image drops it, which is a different
claim.

**Still unclaimed:** `EGRESS=DENIED` does not say which layer refused. The probe's
request is a direct one — Node's classic `https` API ignores `HTTPS_PROXY`, so it
never reaches the proxy — and it does not complete, but a DNS failure and a
refused TCP connect are the same observation from inside. Per ¹, macOS is the
platform where that distinction is real, so it is left open rather than assumed
favourably.

This footnote read "nobody has checked" until 2026-08-23, and before that the
only macOS check in CI was `scripts/sandbox-smoke-macos.sh`, which asserted that a
sandboxed `node` could write its own working directory and nothing else — it would
have passed unchanged against a build whose sandbox blocked nothing, the same
shape of gap that let the Windows egress, piped-stdio and esbuild claims ship
broken.

⁶ **Fixed 2026-08-20; it used to be every loopback service.** Every restricted
mode emitted `(allow network-outbound (remote tcp "localhost:*"))`, so contained
code could reach a local database, a daemon's TCP port or another project's dev
server with no `allow_hosts` entry — and because any of those that forwards
traffic (a debugging proxy, `ssh -D`, a dev-server proxy route) turns into
unrestricted egress, the allowlist stopped meaning anything. The per-port rules
underneath were dead code the wildcard had already subsumed. It had been that way
since the sandbox was first implemented.

Loopback is now granted per mode: `proxy` reaches the proxy's own ports and
nothing else, `offline` gets no network rule at all (it previously reached all of
loopback, so "offline" was not offline), and `loopback` keeps the wildcard because
that is the entire meaning of that mode. `proxy` with no known proxy port grants
nothing rather than falling back to the wildcard. Pinned by
`TestSeatbeltGrantsLoopbackOnlyWhereTheModeMeansIt`, which was confirmed to fail
against the previous behaviour.

Note what this does and does not change: it removes a real hole in the generated
profile. A macOS runner now confirms that egress is denied with an empty allowlist
(⁵), which is not the same as confirming the per-mode loopback scoping — nothing
stands up a loopback listener on macOS and checks which modes can reach it, so
this particular row stays "profile only" in the sense that matters to it.

⁷ **A directory nvx used before 0.5.0 is still writable by every sandbox on the
machine.** Up to 0.5.0 every sandbox ran as one shared package identity and the
`(OI)(CI)(M)` grants it wrote were never revoked, so a contained install in
project A can write into project B. `removeStaleAppContainerGrant` clears them,
but only for the working directory of the session currently running -- a project
cleans itself the next time you use nvx there, and a project you never revisit
stays exposed indefinitely.

README has disclosed this under Known limitations since 0.5.0; this row said an
unqualified "Yes" until 2026-08-20, which an acceptance pass caught by writing
into the nvx repository itself from a sandbox scoped to a different project. That
repository carried 19 such grants at the time.

It is now observable rather than only documented: `nvx doctor` reports leftover
grants on the project it is run in, counts them against health so the command
exits non-zero, and removes them under `--fix`. nvx keeps no record of where it
has run, so it cannot sweep the machine -- the check answers for the directory
you are standing in, which is the one about to matter. Deliberately not a
launch-path warning: the launch path already removes them from the working
directory before running anything, so by then there is nothing left to report.
Pinned by `TestStaleProjectGrantsAreFoundReportedAndFixed`, with a companion test
asserting the scan ignores per-project capability SIDs -- removing one of those
would revoke the running sandbox's own access.

⁴ **Windows containment became per-project in 0.5.0. Before that it was
per-machine.**

The AppContainer profile is stable by design — `platformLaunchNative` uses
`stableSandboxProfile` "so its SID is a durable target for `nvx setup` grants" —
so every session on the machine runs as the same package identity.
`prepareAppContainerFilesystem` granted that identity `(OI)(CI)(M)` on the working
directory and the guest home, and nothing ever revoked those ACEs. The two facts
composed: a grant added while installing in project A was still present, and still
satisfied by the same SID, when nvx later ran in project B. Measured 2026-08-18
with a contained child — it read *and wrote* a second project's files, read a
concurrent session's guest home, and read a `tool_home` profile's credential, the
store a trusted tool is granted persistence for.

The writable roots are now granted to a **capability SID derived from the project**
instead. A Windows token carries capability SIDs alongside the package SID, an ACE
naming one is honoured for file access, and a process holding a different
capability is denied — all three measured before the change was built (see
`sandbox_capability_sid_probe_windows_test.go`). So the package SID stays stable,
`nvx setup`'s drive-root grants keep working, and the per-project identity carries
the isolation.

Deriving from the project rather than the session is what makes it affordable. The
same project derives the same SID every run, so the `icacls` write happens once and
`appContainerHasGrant` skips it thereafter. A per-session identity would pay that
write on every launch and leave a dead ACE on the user's project directory after
each one. Upgrading installs are handled too: a stale package-SID ACE on a path now
governed by a capability is removed the first time nvx runs there, otherwise every
already-granted project would keep the old behaviour.

Ancestor grants are traverse and read-attributes only, not read. `(RX)` would
include list-folder, which let a contained process enumerate the names in a
granted parent -- enough to see which credential stores exist even with their
contents denied. Note what this does NOT cover: `%USERPROFILE%` itself is
listable from any AppContainer because Windows ships an ACE for
ALL APPLICATION PACKAGES on it. nvx does not grant that and cannot revoke it --
deny ACEs were measured not to override it -- so a contained process can see the
names of the directories in your home, though not their contents.

Two things this deliberately does not separate. Sessions in the *same* project
share one capability, because a project's own tool credentials are in its own trust
domain. And ancestor directories keep a shared this-folder-only RX grant for
traverse, which lets a sandbox walk *through* a parent without reading what else is
inside it.

³ **Windows egress became enforced in 0.5.0. It was not before, and this table
claimed otherwise until 2026-08-17.**

The old behaviour: an AppContainer cannot reach a loopback listener outside itself
without a loopback exemption, which only an elevated `nvx setup` could add. Absent
that, `windowsSandboxNetwork` granted the `internetClient` capability and
`stripProxyEnv` removed the proxy variables, so the contained process connected
directly and the allowlist was never consulted — not even cooperatively. Measured
on 2026-08-18 against the 0.4.0 build: a postinstall script reached both
`1.1.1.1:443` and `registry.npmjs.org:443` directly.

The current behaviour: no network capability is granted at all, so the OS refuses
direct connections and DNS does not resolve. The parent's egress proxy is exposed
on an AF_UNIX socket — a filesystem object, so the AppContainer network
restriction does not cover it — and `nvx __appcontainer-exec`, a supervisor
running inside the container, re-exposes it as loopback TCP for tools that only
understand `host:port`. Intra-container loopback needs no exemption. `HTTP_PROXY`
points at the relay, but honouring it is no longer the target's choice: it is the
only route out. The same postinstall script now reports `EACCES` and `ENOTFOUND`
for both hosts while `npm install` completes normally. An independent acceptance
pass found the second half of that sentence unbacked and, at the time, false: a
contained process cannot create a named pipe, Windows builds piped child stdio
out of named pipes, and npm pipes lifecycle-script output by default, so any
install of a script-bearing dependency hung inside libuv before the child
existed. nvx now runs lifecycle scripts with inherited stdio, and
`scripts/sandbox-smoke.ps1` installs a dependency with a postinstall and fails
if it does not complete -- the check whose absence let the claim ship.

That check was still too weak, and a later pass proved it. Its fixture's
postinstall only writes a file; it never captures a subprocess, so it cannot fail
on the case that remains broken. Inherited stdio fixes npm's own piping and
nothing more: a postinstall that captures its OWN child still blocks, because the
restriction is on the contained process creating the pipe, not on npm. Measured
2026-08-19 against `esbuild@0.28.2`, whose postinstall calls
`execFileSync(..., {stdio:"pipe"})`: no completion after 13 minutes contained, 8
seconds uncontained.

The restriction itself cannot be lifted, and that was verified rather than assumed:
`TestAppContainerCannotCreateNamedPipes` calls `CreateNamedPipeW` inside a real
AppContainer and gets `ERROR_ACCESS_DENIED` (5) for three different name shapes,
while all three succeed outside. It is the NPFS device refusing, not a name
collision, so no choice of name routes around it and the only way to grant it
would be loosening `\Device\NamedPipe` machine-wide for every AppContainer on the
host, including real UWP apps. That is not a trade worth making.

**Creating is refused; opening is not, and that distinction is the fix.** Nobody
tested the second question until 2026-08-22, having reasoned from the first for
months. `TestAppContainerCanConnectToAParentCreatedNamedPipe` shows a contained
process opening a pipe the parent made and completing a round trip — but only
when the DACL names the user AND that container's package identity. All four
single-ACE cases deny, which reads exactly like a device-level refusal and is why
an earlier version of that probe nearly recorded the opposite conclusion.
`TestContainedChildCanGiveAHostPipeToItsOwnChild` adds the remaining step: the
contained process can hand that opened handle to its own child as that child's
stdout.

So nvx creates the pipes and contained code only opens them. Granting the
specific container's package SID rather than ALL APPLICATION PACKAGES keeps the
pipe closed to other sandboxes, so per-project identity survives. Nothing is
granted to the container to make this work, and no capability changes.

The user half of that DACL is this user's SID, and it read `WD` -- Everyone --
until an acceptance review enumerated the pipes from an ordinary process and
opened one. So a *different local account* cannot reach these pipes, and another
process running as the same user can: the contained token carries the user's
identity, so the ACE that admits the sandbox admits the user too. That is a
property of the mechanism, not a gap to close, and SECURITY.md states it.
`TestContainedProcessCanStreamAChildsOutput` drives the whole path through the
real binary — 500 lines streamed, stdout and stderr separate, exit code
propagated — because the fix spans a Go broker, an environment variable and a
JavaScript preload, and no unit test on one of those notices the others drifting.

The symptom is a different question, and it is fixed for the case that matters.
File descriptors are not restricted, so `sandbox_stdio_shim.js` -- preloaded into
every contained node process via `NODE_OPTIONS --require` -- routes the
synchronous capture APIs through temp files in the guest home. `npm install
esbuild` now completes in seconds and the resulting binary works. Async
`spawn(..., {stdio:"pipe"})` is a genuine stream a file cannot substitute for and
still hangs; that remains under Known limitations, with the two-minute hint
(`sandbox_hang_hint_windows.go`) naming it. The smoke fixture's postinstall now
captures a subprocess and asserts the captured text, so the case that shipped
broken is the case it tests -- verified by disabling the preload and watching the
smoke hang.

`network.mode: open` is the documented opt-out and is the only mode that grants a
network capability. Setup no longer registers a loopback exemption, and removes an
existing one, because the relay makes it an access grant with no remaining
purpose; `nvx setup` is now only about drive-root stat access.

**A leftover exemption defeats the loopback half of this, and 0.5.0 shipped
without saying so.** Everything above rests on Windows refusing an AppContainer's
connections to loopback addresses outside its own package. An exemption registered
by a pre-0.5.0 elevated `nvx setup` removes that refusal for every destination on
127.0.0.1 — a local database, a daemon's TCP port, another project's dev server —
regardless of `allow_hosts`. Measured on 2026-08-19 by an independent acceptance
pass, which read a host listener from inside a contained process while
`1.1.1.1:443` was refused in the same run.

This page said "egress to other hosts is unaffected" for one day, and that was
wrong. A second pass disproved it: with a CONNECT proxy listening on 127.0.0.1 —
the role played on a real dev machine by mitmproxy, Charles, Burp, a corporate
agent, `ssh -D`, or a dev server's proxy route — a contained process completed a
TLS handshake and a full HTTP exchange with an external host, in the same run
where `1.1.1.1:443` was refused directly. What remains true is narrower and worth
stating exactly: the AppContainer holds no network capability, so *direct*
connections and DNS still fail. That is not the same as the allowlist holding.
Any reachable loopback service that forwards traffic makes egress arbitrary, so
while an exemption is registered the allowlist should be treated as unenforced
rather than as covering everything except loopback.

Removing it needs elevation, so nvx cannot do it on a normal launch. It now
detects the exemption and warns on every affected contained launch, and `nvx
doctor` reports it and exits non-zero. Both print the exact `CheckNetIsolation
LoopbackExempt -d` command. Note what the unit test
`TestLoopbackIsNotAutomaticallyAllowed` covers and does not: it exercises the
proxy's allow decision, which cannot see an OS-level exemption, so no test failed
while the guarantee did. The check is now pinned by
`TestExemptMachineIsWarnedAbout`, which asserts against the machine's real
exemption list rather than a model of it.

⁸ **The Linux rows were green in CI for months without being tested, and that is
worth knowing when reading them.** `scripts/sandbox-enforcement-linux.sh` now runs
unprivileged on a hosted Ubuntu runner and requires:

```
WRITE_OUTSIDE=DENIED  WRITE_INSIDE=ALLOWED  READ_OUTSIDE=DENIED  READ_INSIDE=ALLOWED  EGRESS=DENIED
```

`scripts/sandbox-smoke-egress.sh` adds the direction a denial-only check cannot
reach: it sends CONNECT to nvx's proxy and reads the status, getting 403 for a host
outside the allowlist and 200 for the same host once allowlisted. Reading the
status rather than an exit code is what makes those distinguishable — a machine
with no outbound DNS previously produced the same failure as a refused host, and
that is precisely how phase 1 used to "pass" without the allowlist being consulted.

Until 2026-08-23 none of this ran. Every Linux script gated itself on `unshare -n`,
which an ordinary user is refused, while nvx pairs the network namespace with a
user namespace specifically so it works unprivileged — so all three skipped on
every machine including the runner, and reported success. Underneath, the sandbox
could not start a process at all: the target was given a nested user namespace
whose uid/gid mapping is written through `/proc`, which its own Landlock ruleset
does not grant. Both smoke scripts were also launching their probes uncontained.
The rows above were not wrong about the design; nothing was checking them.

⁹ **Two things about Windows containment that surprise people, both measured.**

**An AppContainer shares the host's network stack.** It is not a Linux network
namespace: a port bound inside the container is occupied on the host too, and
vice versa. What Windows blocks is *connections into* the container, not the
port's existence. So a contained server is unreachable while still holding the
port, and `--expose` cannot publish a port under the same number it uses inside
-- the parent's listener wins the race and the contained server dies with
`EADDRINUSE`. Measured on Windows 11 with 51733 on both sides.

`--expose` therefore maps `inside:host` with two different numbers. It grants no
network capability: the contained side dials outward over AF_UNIX and the parent
splices inbound requests onto those tunnels, so egress stays exactly as
restricted. `TestExposedPortIsReachableFromTheHost` asserts both halves in one
run -- the host reaches the contained server, and the contained process still
cannot reach the internet.

`--connect` is the same machinery pointed the other way, and the same two-number
rule applies for the same reason. nvx runs the listener inside the sandbox and
dials `127.0.0.1:<host>` itself from outside, so the contained side chooses when
to connect and never where -- one port, for one run, closed when the command
exits. `TestAContainedDialReachesTheHostServiceItWasGranted` drives a real
connection end to end through both halves.

**"For one run" is enforced, not implied.** An AppContainer's loopback is not
private: Windows permits it within a package, and every nvx sandbox shares one
package identity, so the in-sandbox listener is reachable from every other nvx
sandbox running concurrently. Measured 2026-08-28 -- a sandbox in an unrelated
project with no grant of its own read the granted service, while the same probe
could reach neither the real port nor an unrelated one. Note the shape: this is
the hazard the egress relay already defends against with a per-session proxy
credential (see EgressProxy.token, and the acceptance pass of 2026-08-19 that
found a sibling borrowing another project's allowlist). A credential works there
because HTTP has somewhere to put one; a tunnel carrying an arbitrary protocol
does not, so the peer is identified instead. Every process a run launches is in
that run's Job Object, so the parent resolves the connection to a process and
refuses anything outside it -- in the parent, because `GetExtendedTcpTable` is
ACCESS_DENIED inside an AppContainer. Unverifiable peers are refused, not
admitted.

That is what makes it defensible where the pre-0.5.0 loopback exemption was not:
`CheckNetIsolation LoopbackExempt` was machine-wide, permanent, opened *every*
service on 127.0.0.1 to the sandbox, and needed elevation to revoke. This grants
nothing at the OS level at all.

**A blocked write can report success.** A contained process writing to the user
profile root gets no error, reads its own file back, and stats it -- while the
host has no such file at that path. Windows redirects the write into a
per-container view rather than refusing it. Measured 2026-08-24, with the
contained process still running at the time of the host check, so this is not
cleanup racing the observation.

Containment holds either way, but it means an in-sandbox return value is not
evidence on its own, in either direction. Every probe here checks the host's
disk as well as what the contained process reported, which is why
`scripts/sandbox-enforcement-windows.ps1` asserts the forbidden path is absent
rather than trusting `WRITE_OUTSIDE=DENIED`.

## Docker provider

| Guarantee | Behavior |
|---|---|
| Host filesystem | Only the working directory is bind-mounted (`/app`); the rest of the host is not visible. |
| Environment secrets | Scrubbed before entering the container. |
| Hardening | `--cap-drop=ALL`, `--security-opt=no-new-privileges`, `--pids-limit`, `tmpfs /tmp`. |
| `network.mode: offline` / `loopback` | **Enforced** via `--network none` (no interfaces at all). |
| `network.mode: proxy` | **Not enforced** — the allowlist would be cooperative only, so proxy mode is disallowed under Docker. Use the native provider for allowlisted egress. |
| Docker not installed / not running | Fails closed with a clear error before anything launches. |

## CI note

Linux and macOS each run an enforcement probe on a hosted runner of that OS (⁵,
⁸). Windows is the exception: hosted Windows runners refuse to create
AppContainer children — `CreateProcess` returns "Access is denied" for every
executable, including `cmd.exe` — so anything that launches a real contained
process skips there. That is a limitation of the hosted environment, not of the
provider, and it is the reason the Windows cells say "measured" rather than "CI".

What still runs on the hosted Windows runner is most of the suite: ACL
derivation, capability SIDs, profile generation and the syscall wrappers. The
skips are dominated by the ones that need a live contained child — which is to
say, exactly the ones that would prove containment. The rest are environmental
(no staged runtime, a directory that inherits nothing) or deliberate.

**For the current numbers, read the run rather than this page.** Every CI run
prints them in its job summary and in the log as one greppable line:

```
NVX_PROBE_COUNTS pass=… skip=… fail=…
```

followed by the distinct skip reasons. `gh run view <id> --log | Select-String
NVX_PROBE_COUNTS` gets it, and the job summary shows it without opening a log.

This page used to quote a count instead, and it rotted twice: it said "441 pass,
21 skip" describing a run that was 442 and 35, and the later correction could not
be checked by a reviewer at all, because a developer machine *runs* the probes
that a hosted runner skips. A number nobody can reproduce is a number that goes
quietly wrong. The skip reasons are the signal worth reading; the totals are just
how you notice they changed.

`scripts/sandbox-enforcement-windows.ps1` closes that by hand. It asserts the
same five outcomes as the Linux probe (writes and reads denied outside, both
allowed inside, egress denied with an empty allowlist) and is run on a real
Windows machine before a release; see CONTRIBUTING.md. It is wired into CI as
well, where it detects the runner's limitation and skips, so that if a future
image can host an AppContainer it begins asserting without anyone remembering to
enable it.

Two things it deliberately does not cover. Egress denial there is
direct-connection only: the AppContainer holds no network capability, so the
refusal does not depend on the allowlist, and a machine carrying a leftover
pre-0.5.0 loopback exemption can still have egress forwarded through a loopback
service (³, and `nvx doctor` is the check for it — the script prints a warning
when it detects one). And the smoke script's own host-write check writes through
the sandbox's redirected `%USERPROFILE%`, so it passes whenever redirection
works; the enforcement probe uses absolute paths resolved outside the sandbox
for that reason.

One Linux test skips unprivileged and is re-run as root in the same build:
Ubuntu 24.04's AppArmor hardening allows an unprivileged user namespace to be
created but refuses `CAP_NET_ADMIN` inside it, so loopback cannot be brought up
and the egress test has nothing to measure. It probes for that capability rather
than for the namespace, because the two answers differ and only the first one
decides whether the test can run. CI relaxes
`kernel.apparmor_restrict_unprivileged_userns` before the smoke and enforcement
steps, which is why those do run unprivileged there.
