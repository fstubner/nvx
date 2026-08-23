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

Every macOS cell below carries a silent qualifier, stated once here rather than
repeated in each: **it describes the generated Seatbelt profile, not observed
behaviour.** No macOS row in this table has been confirmed against a running
system — see ⁵.

| Guarantee | Windows (AppContainer) | Linux (Landlock + netns + seccomp) | macOS (Seatbelt) |
|---|---|---|---|
| Host filesystem write blocked (outside workdir + guest home) | Yes, except pre-0.5.0 projects⁷ | Yes | Profile only⁵ |
| Host filesystem read restricted | Yes⁴ | Yes | No² |
| Environment secrets scrubbed | Yes | Yes | Yes |
| Egress restricted to policy allowlist | Yes³ | Yes | Profile only⁵ (loopback proxy) |
| Non-proxied raw TCP/UDP blocked at OS | Yes³ (no network capability) | Yes (loopback-only netns + seccomp) | Profile only⁵ (`deny default`, localhost permitted) |
| Non-proxied DNS blocked | Yes³ | Yes (netns) | Partial¹ |
| Any loopback service reachable | Only with a leftover exemption³ | No (loopback-only netns) | No⁶ (proxy port only) |
| Fails closed if a primitive is missing | Yes | Yes (Landlock 5.13+, iproute2 for netns) | Profile only⁵ (needs `/usr/bin/sandbox-exec`) |

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

⁵ **"Profile only" means nobody has checked.** `sandbox_seatbelt.go` emits
`(deny default)` and unit tests assert the profile's text, so nvx generates the
policy it intends to. Whether the kernel enforces it has never been verified on
macOS hardware. The one macOS check that runs in CI,
`scripts/sandbox-smoke-macos.sh`, asserts that a sandboxed `node` can write to its
own working directory and nothing else — it would pass unchanged against a build
whose sandbox blocked nothing at all, which is the same shape of gap that let the
Windows egress, piped-stdio and esbuild claims ship broken. Closing it needs a Mac
and a smoke test that asserts the denials. Until then the honest reading of the
macOS column is "intended", not "enforced".

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
profile, but the profile is still "profile only" per ⁵ — no macOS hardware has
confirmed the kernel enforces any of it.

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

The Windows AppContainer smoke tests are skipped on GitHub-hosted Windows
runners, which cannot reliably spawn AppContainer child processes. The native
provider is exercised locally and on self-hosted runners; this is a limitation
of the hosted CI environment, not of the provider.
