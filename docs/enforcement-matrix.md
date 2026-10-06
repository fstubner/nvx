# Isolation enforcement matrix

nvx's guarantees differ by platform and by isolation provider. This page states
plainly what the OS boundary enforces versus what depends on the child
process cooperating. The design rule is **fail closed**. If a primitive is
missing or nvx cannot enforce a mode, nvx refuses to run instead of downgrading
silently.

## Providers

- **native** (default, zero-config) uses Windows AppContainer, Linux Landlock +
  network namespace + seccomp, and macOS Seatbelt (`sandbox-exec`).
- **docker** runs the command in a container. Supported and hardened, but
  needs Docker installed and running.
nvx removed `wsl`, `wslc` and `systemd-nspawn`. No test or runner ever
exercised them. They took no network context (so they silently ignored
`network.mode: offline`). Nspawn required
root and left root-owned files in the project. A name that is not in the list
above stops the run.

## Native provider

Cells marked **profile only** describe the generated Seatbelt profile instead of
observed behaviour. See ⁵ for which macOS rows a running system now confirms
and which it does not.

**Where the evidence for each column comes from.**
`scripts/sandbox-enforcement-windows.ps1` asserts Windows, and so do the
`NVX_PROBE=1` tests run by hand on a real machine before a release. They also
run on a hosted Windows runner in CI now (see the CI note below).

A run on real Linux confirmed Linux on
2026-09-01 (WSL2, Ubuntu 24.04, kernel 6.18) instead of relying on CI's word.
`sandbox-enforcement-linux.sh` reported `WRITE_OUTSIDE=DENIED WRITE_INSIDE=ALLOWED
READ_OUTSIDE=DENIED READ_INSIDE=ALLOWED EGRESS=DENIED`. A check confirmed
`unshare -Urn` usable first, so the egress line ran as an assertion and was not skipped. The
cross-compiled test binary ran 308 tests including the Landlock and seccomp ones
that cannot build on Windows.

macOS enforcement is still CI-only. Nobody working
on this has a Mac, and that is why ⁵ separates what CI measured there from
what the generated profile says. Tests check the Seatbelt profile's CONTENT
everywhere. Seven profile tests run on Linux and Windows too, because generating
the text is platform-independent. So the tests verify what the macOS profile SAYS
and do not verify whether the kernel honours it.

| Guarantee | Windows (AppContainer) | Linux (Landlock + netns + seccomp) | macOS (Seatbelt) |
|---|---|---|---|
| Host filesystem write blocked (outside workdir + guest home) | Yes⁷ | Yes⁸ | Yes⁵ |
| Host filesystem read restricted | Yes⁴ | Yes⁸ | Partial²: the home directory denied outside what a run needs, other paths readable⁵ |
| Project `.git` read-only, rest of project writable | Yes¹⁴ | Yes¹⁴ | Yes¹⁴ |
| Environment secrets scrubbed | Yes | Yes | Yes |
| Egress blocked when the allowlist does not cover the host | Yes³ | Yes⁸ | Yes⁵ |
| Allowlisted host reachable through the proxy | Yes³ | Yes⁸ | Yes⁵ |
| Non-proxied raw TCP/UDP blocked at OS | Yes³ (no network capability) | Yes (loopback-only netns + seccomp) | Yes⁵ (TCP and UDP; UDP refused at bind) |
| Non-proxied DNS blocked | Yes³ | Yes (netns) | Partial¹ |
| Any loopback service reachable | No, unless the policy lists it¹¹, or `network.mode: loopback`¹³ | No, unless the policy lists it, or `network.mode: loopback`¹³ | No⁶ (proxy port only), or `network.mode: loopback`¹³ |
| One named host service reachable | Via `allow_hosts`, or `--connect` for one run⁹ ¹¹ | Via `allow_hosts` for proxy-aware clients, or `--connect` for one run, except in `offline`¹¹ ¹² | Via `allow_hosts` for proxy-aware clients, or `--connect` for one run¹¹ ¹² |
| Another project's sandbox reachable over loopback | No¹⁰ (per-project package) | No (each has its own netns) | Untested |
| A contained server reachable from the host | Only via `--expose`⁹ | Yes (shared stack, no inbound block) | Yes |
| Fails closed if a primitive is missing | Yes | Yes (Landlock 5.13+, iproute2 for netns) | Yes⁵ (refuses to run without `/usr/bin/sandbox-exec`) |

² On macOS the Seatbelt profile allows filesystem reads outside the home
directory. The dynamic linker must read system libraries and the dyld shared
cache. Their locations vary by macOS version (e.g. the Cryptexes firmlink on
Apple Silicon) and nvx cannot enumerate them reliably. A strict read allowlist
breaks process launch. Write containment and egress control remain enforced, and
nvx scrubs environment secrets and redirects `$HOME` to a guest profile under
`~/.nvx`. That profile is thrown away after each run, except for pnpm and for
tools approved as trusted, which keep one profile per project.

Under the home directory reads are denied. After the blanket read allow, the
profile denies reads of the real home and of nvx's own home (`~/.nvx`, or
wherever `NVX_HOME` points). It then reopens what a contained run reads there,
which is what Linux grants: the project, the guest home, nvx's `versions`, `bin`
and `current`, and every `isolation.filesystem.allow_read_exec` root. File
metadata stays readable, so a contained process can stat a path in the home and
cannot read its contents. A runtime installed under the home outside nvx, such
as one from nvm, runs contained only when its directory is listed in
`allow_read_exec`, as on Linux. Until 2026-10-06 the profile denied only the
credential stores below, and every other file in the home was readable, other
projects included.

The user's credential stores are denied last, after everything the profile
reopens, so a project or an `allow_read_exec` root that holds one does not
expose it. The profile denies reads of `~/.npmrc`, `~/.yarnrc`, `~/.yarnrc.yml`,
`~/.config/pnpm/rc`, `~/Library/Preferences/pnpm/rc`, `~/.bunfig.toml`,
`~/.docker/config.json`, `~/.netrc` and `~/.git-credentials`, and of everything
under `~/.ssh`, `~/.aws`, `~/.gnupg`, `~/.config/gh`, `~/.kube`,
`~/.config/gcloud`, `~/.azure` and `~/Library/Keychains`. `~` is the real home,
and each path is also named with symbolic links resolved, because Seatbelt
matches the resolved path. None of these is on the dynamic linker's path. Until
2026-10-01 the profile denied none of them.

Reads outside the home stay allowed. That includes the per-user temp and cache
directories under `/private/var/folders`, which other apps use, and a project
or a credential kept on another volume. Linux denies those too.

Writes are contained to the project and the guest home, where `$TMPDIR` points.
Outside them the profile grants writes only on named device files: `/dev/null`,
`/dev/zero`, `/dev/random`, `/dev/urandom`, `/dev/tty`, `/dev/ptmx`,
`/dev/dtracehelper`, `/dev/fd/*` and `/dev/ttys*`. Until 2026-10-06 it also
granted all of `/dev`, `/private/tmp`, `/private/var/tmp` and
`/private/var/folders`. The last holds every app's per-user temp and cache
directories, which uncontained programs read back.
`scripts/sandbox-enforcement-macos.sh` requires a contained write to each of
`/private/tmp`, `/private/var/tmp` and the user's Darwin temp and cache
directories to be refused, and a write to the contained process's own temp
directory to succeed.

**That redirection does not stop anything reading a file, and this note used to
say it did.** It claimed "the sensitive material is still protected", which is
true of writes and false of reads.

`$HOME` decides where `~` expands to. It does
not stop anything opening `/Users/<you>/.ssh/id_rsa` by absolute path. A
postinstall script looking for credentials does not need `~` to find them. That
is why the profile denies the home directory and the credential stores above
by path.

On macOS the read guarantee covers the home directory and nvx's home. Reads
elsewhere on the disk stay allowed, which is a narrower product than the same
sentence describes on Linux, where a contained process sees only what it is
granted.

¹ On macOS, the loopback proxy and OS network rules gate egress. Linux
also removes all non-loopback interfaces (network namespace), so DNS to
external resolvers cannot leave. On macOS a determined process could still
attempt DNS via the OS resolver. This is the main per-OS difference and is why
Linux has the strongest network story.

⁵ **macOS hardware confirms the macOS column, with one stated exception.**
`scripts/sandbox-enforcement-macos.sh` runs on a hosted macOS runner on every CI
build and asserts the denials instead of only that the command ran. A contained
process reports, and CI requires:

```
WRITE_OUTSIDE=DENIED   WRITE_INSIDE=ALLOWED   READ_OUTSIDE=DENIED   READ_INSIDE=ALLOWED
EGRESS=DENIED          UDP_EGRESS=DENIED      CONNECT=200 (allowlisted host)
```

Three of those are load-bearing in a way the others are not. `WRITE_INSIDE`,
`READ_INSIDE` and `CONNECT=200` are the positive controls. Every denial above them would also pass
for a sandbox that had failed to start. Requiring something to
*succeed* is the only thing that tells enforcement from breakage. `CONNECT=200`
is the one that closed the largest gap here. Until 2026-08-24 the whole script
ran with an empty allowlist and could only ever observe refusals.

The script's third phase starts nvx with `HOME` set to a throwaway directory
holding a planted `.npmrc` and `.ssh/id_test`. The OS must refuse a contained
read of each. A project file and node's own binary must still read,
each checked by exit code. Contained `npm config get registry` must succeed with
that `.npmrc` present and must not report the registry planted in it.

`READ_OUTSIDE` reads a file in the real home outside the project, and the OS
must refuse it with EPERM or EACCES. The project, `NVX_HOME` and an
`allow_read_exec` directory sit under the home for this run, so the controls
`READ_INSIDE`, `READ_RUNTIME` (node's own binary under `NVX_HOME/versions`) and
`READ_EXEC_ROOT` would fail against a profile that denied the whole home.
`NVX_HOME_READ` reads a file in nvx's home outside its runtimes and must be
refused. A fourth phase repeats that with an `NVX_HOME` under `/var/folders`,
outside the home. Before the profile denied the home, all three reads succeeded
(run 37399750782). After it, all three were refused and every control passed,
as did the macOS smoke's contained `npm install` and the launch-escape probe
(run 37400274341).

`UDP_EGRESS=DENIED` comes from Seatbelt refusing at **bind**, not at send. Sending
on an unbound UDP socket makes the runtime bind one implicitly. Seatbelt
rejects that with EPERM on 0.0.0.0. That is stronger than the send-level refusal
expected.

It arrives as an error event instead of a callback error. Unhandled, it
killed the probe before it wrote a report. That is how the first version of that
check failed on a real runner instead of recording a pass.

A unit test asserts that nvx fails closed without `/usr/bin/sandbox-exec`, and
the script does not. It needs that file to be absent, and the script cannot
remove it from the machine under test. `seatbeltExecPath` is a variable so the
test can move it. The test asserts both that nvx reports failure and that the
command left no trace. Reporting failure while having run the thing uncontained
is the outcome that would actually matter. The script separately checks
`sandbox-exec` exists and fails loudly if a future runner image drops it, which
is a different claim.

**Still unclaimed.** `EGRESS=DENIED` does not say which layer refused. The probe's
request is a direct one (Node's classic `https` API ignores `HTTPS_PROXY`, so it
never reaches the proxy). It does not complete, but a DNS failure and a
refused TCP connect are the same observation from inside. Per ¹, macOS is the
platform where that distinction is real, so this page leaves it open instead of
assuming it favourably.

This footnote read "nobody has checked" until 2026-08-23. Before that the
only macOS check in CI was `scripts/sandbox-smoke-macos.sh`, which asserted that a
sandboxed `node` could write its own working directory and nothing else. It would
have passed unchanged against a build whose sandbox blocked nothing. That is the same
shape of gap that let the Windows egress, piped-stdio and esbuild claims ship
broken.

⁶ **Fixed 2026-08-20. It used to be every loopback service.** Every restricted
mode emitted `(allow network-outbound (remote tcp "localhost:*"))`. So contained
code could reach a local database, a daemon's TCP port or another project's dev
server with no `allow_hosts` entry. Any of those that forwards
traffic (a debugging proxy, `ssh -D`, a dev-server proxy route) turns into
unrestricted egress, so the allowlist stopped meaning anything.

The per-port rules
underneath were dead code the wildcard had already subsumed. It had been that way
since the sandbox was first implemented.

nvx now grants loopback per mode. `proxy` reaches the proxy's own ports and
nothing else. `offline` gets no network rule at all (it previously reached all of
loopback, so "offline" was not offline). `loopback` keeps the wildcard because
that is the entire meaning of that mode. `proxy` with no known proxy port grants
nothing instead of falling back to the wildcard. Pinned by
`TestSeatbeltGrantsLoopbackOnlyWhereTheModeMeansIt`, which was confirmed to fail
against the previous behaviour.

Note what this does and does not change. It removes a real hole in the generated
profile. A macOS runner now confirms that the sandbox denies egress with an empty
allowlist (⁵). That is not the same as confirming the per-mode loopback scoping.
Nothing stands up a loopback listener on macOS and checks which modes can
reach it. So this particular row stays "profile only" in the sense that matters to it.

⁷ **A directory nvx used before 0.5.0 carries a dead permission, and it stopped
being exploitable on 2026-08-29.** Up to 0.5.0 every sandbox ran as one shared
package identity and the `(OI)(CI)(M)` grants it wrote were never revoked. So a
contained install in project A could write into project B.
`removeStaleAppContainerGrant` clears them, but only for the working directory of
the session currently running. A project cleans itself the next time you use
nvx there, and a project you never revisit keeps the ACE indefinitely.

What changed is who can satisfy it. Sandboxes now run under a per-project
AppContainer package, so no current session holds the shared identity those old
grants name. Measured 2026-08-31 by recreating the exact condition this footnote
describes. That meant granting the real `nvx.sandbox` package SID `(OI)(CI)(M)` on
a fresh directory and then running a contained process from an unrelated project
against it:

```
LEGACY_WRITE=DENIED EPERM
LEGACY_LIST=DENIED EPERM
```

So the ACE is litter instead of an opening. This entry said "still writable by
every sandbox on the machine" for two days after that stopped being true. That
is the safe direction to be wrong in and still worth correcting. An
acceptance pass reproduced the scenario expecting to confirm a hole and found none.

README disclosed this under Known limitations from 0.5.0, and SECURITY.md
carries it now. This row said an unqualified "Yes" until 2026-08-20. An acceptance pass caught that by writing
into the nvx repository itself from a sandbox scoped to a different project. That
repository carried 19 such grants at the time.

It is now observable instead of only documented. `nvx doctor` reports leftover
grants on the project you run it in, counts them against health so the command
exits non-zero, and removes them under `--fix`. nvx keeps no record of where it
has run, so it cannot sweep the machine. The check answers for the directory
you are standing in, which is the one about to matter.

It is deliberately not a launch-path warning. The launch path already removes them from the working
directory before running anything, so by then there is nothing left to report.
Pinned by `TestStaleProjectGrantsAreFoundReportedAndFixed`, with a companion test
asserting the scan ignores per-project capability SIDs, because removing one of those
would revoke the running sandbox's own access.

⁴ **Windows containment became per-project in 0.5.0. Before that it was
per-machine.**

The AppContainer profile is stable by design. `platformLaunchNative` uses
`stableSandboxProfile` "so its SID is a durable target for `nvx setup` grants", so
every session on the machine runs as the same package identity.
`prepareAppContainerFilesystem` granted that identity `(OI)(CI)(M)` on the working
directory and the guest home, and nothing ever revoked those ACEs.

The two facts composed. A grant added while installing in project A was still
present, and the same SID still satisfied it, when nvx later ran in project B.
Measured 2026-08-18 with a contained child. It read *and wrote* a second project's
files and read a concurrent session's guest home. It also read a `tool_home` profile's
credential, the store nvx grants a trusted tool persistence for.

nvx now grants the writable roots to a **capability SID derived from the project**
instead. A Windows token carries capability SIDs alongside the package SID. The OS
honours an ACE naming one for file access, and it denies a process holding a different
capability. All three were measured before the change was built (see
`sandbox_capability_sid_probe_windows_test.go`). So the package SID stays stable,
`nvx setup`'s drive-root grants keep working, and the per-project identity carries
the isolation.

The package SID did not stay stable for long. 0.5.x made it per-project too, for
loopback isolation (see the `--connect` entry). nvx was then writing the read-only
grants that had been riding on it once per project into directories it owns. Those
grants were the runtime, the staged supervisor and the parent of the guest home. A
mismatched has-grant check repeated them on every launch.

Since 2026-09-02 those
go to a second capability every sandbox carries, `nvx.runtime.readonly`, granted
once per path per machine (`sandbox_runtime_identity_windows.go`). A token now
holds three identities. They are the project's (writable roots and `allow_read_exec`),
the runtime's (read-only trees), and setup's (drive roots).

Deriving from the project instead of the session is what makes it affordable. The
same project derives the same SID every run, so the `icacls` write happens once and
`appContainerHasGrant` skips it thereafter. A per-session identity would pay that
write on every launch and leave a dead ACE on the user's project directory after
each one. nvx handles upgrading installs too. It removes a stale package-SID ACE
on a path now governed by a capability the first time it runs there. Otherwise every
already-granted project would keep the old behaviour.

Ancestor grants are traverse and read-attributes only, not read. `(RX)` would
include list-folder, which let a contained process enumerate the names in a
granted parent. That was enough to see which credential stores exist even with their
contents denied.

Note what this does NOT cover: `%USERPROFILE%` itself is
listable from any AppContainer because Windows ships an ACE for
ALL APPLICATION PACKAGES on it. nvx does not grant that and cannot revoke it.
Deny ACEs were measured not to override it. So a contained process can see the
names of the directories in your home, though not their contents.

Two things this deliberately does not separate. Sessions in the *same* project
share one capability, because a project's own tool credentials are in its own trust
domain. And ancestor directories keep a shared this-folder-only (X,RA) grant for
traverse. That lets a sandbox walk *through* a parent without reading what else is
inside it.

³ **Windows egress became enforced in 0.5.0. It was not before, and this table
claimed otherwise until 2026-08-17.**

In the old behaviour an AppContainer cannot reach a loopback listener outside itself
without a loopback exemption, which only an elevated `nvx setup` could add. Absent
that, `windowsSandboxNetwork` granted the `internetClient` capability and
`stripProxyEnv` removed the proxy variables, so the contained process connected
directly and the allowlist was never consulted, not even cooperatively. Measured
on 2026-08-18 against the 0.4.0 build. A postinstall script reached both
`1.1.1.1:443` and `registry.npmjs.org:443` directly.

In the current behaviour nvx grants no network capability at all, so the OS refuses
direct connections and DNS does not resolve. The parent exposes its egress proxy
on an AF_UNIX socket (a filesystem object, so the AppContainer network
restriction does not cover it). `nvx __appcontainer-exec`, a supervisor
running inside the container, re-exposes it as loopback TCP for tools that only
understand `host:port`. Intra-container loopback needs no exemption.

`HTTP_PROXY` points at the relay, but honouring it is no longer the target's choice.
It is the only route out. The same postinstall script now reports `EACCES` and `ENOTFOUND`
for both hosts while `npm install` completes normally. An independent acceptance
pass found the second half of that sentence unbacked and, at the time, false. A
contained process cannot create a named pipe, Windows builds piped child stdio
out of named pipes, and npm pipes lifecycle-script output by default. So any
install of a script-bearing dependency hung inside libuv before the child
existed.

nvx now runs lifecycle scripts with inherited stdio. `scripts/sandbox-smoke.ps1`
installs a dependency with a postinstall and fails if it does not complete, the
check whose absence let the claim ship.

That check was still too weak, and a later pass proved it. Its fixture's
postinstall only writes a file. It never captures a subprocess, so it cannot fail
on the case that remains broken. Inherited stdio fixes npm's own piping and
nothing more. A postinstall that captures its OWN child still blocks, because the
restriction is on the contained process creating the pipe, not on npm.

Measured
2026-08-19 against `esbuild@0.28.2`, whose postinstall calls
`execFileSync(..., {stdio:"pipe"})`. It did not complete after 13 minutes contained
and completed in 8 seconds uncontained.

Nothing can lift the restriction itself, and a test checks that instead of assuming it.
`TestAppContainerCannotCreateNamedPipes` calls `CreateNamedPipeW` inside a real
AppContainer and gets `ERROR_ACCESS_DENIED` (5) for three different name shapes,
while all three succeed outside. It is the NPFS device refusing, not a name
collision, so no choice of name routes around it. Granting it would mean
loosening `\Device\NamedPipe` machine-wide for every AppContainer on the
host, including real UWP apps. That is not a trade worth making.

**Windows refuses creating a pipe but allows opening one, and that distinction is the fix.** Nobody
tested the second question until 2026-08-22, having reasoned from the first for
months. `TestAppContainerCanConnectToAParentCreatedNamedPipe` shows a contained
process opening a pipe the parent made and completing a round trip. That works only
when the DACL names the user AND that container's package identity. All four
single-ACE cases deny. That reads exactly like a device-level refusal and is why
an earlier version of that probe nearly recorded the opposite conclusion.

`TestContainedChildCanGiveAHostPipeToItsOwnChild` adds the remaining step.
The contained process can hand that opened handle to its own child as that child's
stdout.

So nvx creates the pipes and contained code only opens them. Granting the
specific container's package SID instead of ALL APPLICATION PACKAGES keeps the
pipe closed to other sandboxes, so per-project identity survives. nvx grants the
container nothing to make this work, and no capability changes.

The user half of that DACL is this user's SID. It read `WD` (Everyone)
until an acceptance review enumerated the pipes from an ordinary process and
opened one. So a *different local account* cannot reach these pipes, and another
process running as the same user can. The contained token carries the user's
identity, so the ACE that admits the sandbox admits the user too. That is a
property of the mechanism, not a gap to close, and SECURITY.md states it.

`TestContainedProcessCanStreamAChildsOutput` drives the whole path through the
real binary (500 lines streamed, stdout and stderr separate, exit code
propagated). It does this because the fix spans a Go broker, an environment variable and a
JavaScript preload. No unit test on one of those notices the others drifting.

The symptom is a different question, and nvx has fixed it for the case that matters.
File descriptors are not restricted, so `sandbox_stdio_shim.js`, preloaded into
every contained node process via `NODE_OPTIONS --require`, routes the
synchronous capture APIs through temp files in the guest home. `npm install
esbuild` now completes in seconds and the resulting binary works. Async
`spawn(..., {stdio:"pipe"})` is a genuine stream a file cannot substitute for, and
it works too. nvx creates the pipes outside the container and the preload only
opens them, which Windows permits.

Measured 2026-08-29 inside a real
AppContainer. `spawn` with piped stdio returned its child's output and exit
code. This file said it "still hangs" for weeks after that stopped being true.
That matters more here than elsewhere because PRODUCT.md names this file as the
authority.

Writing to a contained child's stdin works through the same broker
since 2026-09-04. What remains under Known limitations is narrower. A child given
an IPC channel (`child_process.fork`) is refused. The smoke fixture's postinstall now
captures a subprocess and asserts the captured text, so the case that shipped
broken is the case it tests. Disabling the preload and watching the
smoke hang verified this.

`network.mode: open` is the documented opt-out and is the only mode that grants a
network capability. Setup no longer registers a loopback exemption, and it removes an
existing one, because the relay makes it an access grant with no remaining
purpose. `nvx setup` is now only about read and list access to the root of every fixed
volume and its Users folder.

**A leftover exemption defeats the loopback half of this, and 0.5.0 shipped
without saying so.** Everything above rests on Windows refusing an AppContainer's
connections to loopback addresses outside its own package. An exemption registered
by a pre-0.5.0 elevated `nvx setup` removes that refusal for every 127.0.0.1
destination. That covers a local database, a daemon's TCP port and another project's
dev server, regardless of `allow_hosts`. Measured on 2026-08-19 by an independent acceptance
pass, which read a host listener from inside a contained process while Windows
refused `1.1.1.1:443` in the same run.

This page said "egress to other hosts is unaffected" for one day, and that was
wrong. A second pass disproved it. A contained process completed a TLS handshake
and a full HTTP exchange with an external host. A CONNECT proxy was listening on
127.0.0.1 at the time. On a real dev machine, mitmproxy, Charles, Burp, a corporate
agent, `ssh -D` or a dev server's proxy route all play that proxy's role. The same run
refused `1.1.1.1:443` directly.

What remains true is narrower and worth
stating exactly. The AppContainer holds no network capability, so *direct*
connections and DNS still fail. That is not the same as the allowlist holding.
Any reachable loopback service that forwards traffic makes egress arbitrary. So
while an exemption exists, treat the allowlist as unenforced
instead of as covering everything except loopback.

Removing it needs elevation, so nvx cannot do it on a normal launch. It now
detects the exemption and warns on every affected contained launch, and `nvx
doctor` reports it and exits non-zero. Both print the exact `CheckNetIsolation
LoopbackExempt -d` command. Note what the unit test
`TestLoopbackIsNotAutomaticallyAllowed` covers and does not. It exercises the
proxy's allow decision, which cannot see an OS-level exemption, so no test failed
while the guarantee did. The check is now pinned by
`TestExemptMachineIsWarnedAbout`, which asserts against the machine's real
exemption list instead of a model of it.

⁸ **The Linux rows were green in CI for months while nothing tested them, and that is
worth knowing when reading them.** `scripts/sandbox-enforcement-linux.sh` now runs
unprivileged on a hosted Ubuntu runner and requires:

```
WRITE_OUTSIDE=DENIED  WRITE_INSIDE=ALLOWED  READ_OUTSIDE=DENIED  READ_INSIDE=ALLOWED  EGRESS=DENIED
```

`scripts/sandbox-smoke-egress.sh` adds the direction a denial-only check cannot
reach. It sends CONNECT to nvx's proxy and reads the status. It gets 403 for a host
outside the allowlist and 200 for the same host once allowlisted. Reading the
status instead of an exit code is what makes those distinguishable. A machine
with no outbound DNS previously produced the same failure as a refused host.
That is precisely how phase 1 used to "pass" without anything consulting the allowlist.

Until 2026-08-23 none of this ran. Every Linux script gated itself on `unshare -n`,
which fails for an ordinary user. nvx pairs the network namespace with a
user namespace specifically so it works unprivileged. So all three skipped on
every machine including the runner, and reported success.

Underneath, the sandbox
could not start a process at all. nvx gave the target a nested user namespace
whose uid/gid mapping is written through `/proc`, which its own Landlock ruleset
does not grant. Both smoke scripts were also launching their probes uncontained.
The rows above were not wrong about the design. Nothing was checking them.

On Linux a contained process sees only the paths nvx grants it, in a root of its
own. Landlock below ABI v9 does not restrict `connect()` to a UNIX socket by path.
Measured on WSL2 Ubuntu 24.04, kernel 6.18, before the change. A contained
process got HTTP 200 from `/var/run/docker.sock` while the sandbox denied its writes outside the
project.

A socket inside a granted path stays reachable. The granted paths are
the project, the guest home, and below ABI v9 also the system and runtime
directories and any `allow_read_exec` root. In `network.mode: open` the host
resolver sockets in `/run/systemd/resolve` and `/run/nscd` stay visible too.

⁹ **Two things about Windows containment that surprise people, both measured.**

**An AppContainer shares the host's network stack.** It is not a Linux network
namespace. A port bound inside the container occupies the same port on the host, and
vice versa. What Windows blocks is *connections into* the container, not the
port's existence. So a contained server is unreachable while still holding the
port.

`--expose` cannot publish a port under the same number it uses inside
-- the parent's listener wins the race and the contained server dies with
`EADDRINUSE`. Measured on Windows 11 with 51733 on both sides.

`--expose` therefore maps `inside:host` with two different numbers. It grants no
network capability. The contained side dials outward over AF_UNIX and the parent
splices inbound requests onto those tunnels, so egress stays exactly as
restricted. `TestExposedPortIsReachableFromTheHost` asserts both halves in one
run -- the host reaches the contained server, and the contained process still
cannot reach the internet.

`--connect` is the same machinery pointed the other way, and the same two-number
rule applies for the same reason. nvx runs the listener inside the sandbox and
dials `127.0.0.1:<host>` itself from outside. So the contained side chooses when
to connect and never where -- one port, for one run, closed when the command
exits. `TestAContainedDialReachesTheHostServiceItWasGranted` drives a real
connection end to end through both halves.

**nvx checks "for one run", and the boundary is the project.** An AppContainer's
loopback is not private. Windows permits it within a package, and every run of
one project shares that project's package (¹⁰). Until 2026-08-29 every nvx
sandbox shared one package, so the in-sandbox listener was reachable from every
other nvx sandbox running concurrently.

Measured 2026-08-28. A sandbox in an
unrelated project with no grant of its own read the granted service. The same probe
could reach neither the real port nor an unrelated one. Note the shape. This is
the hazard the egress relay already defends against with a per-session proxy
credential. See EgressProxy.token, and the acceptance pass of 2026-08-19 that
found a sibling borrowing another project's allowlist.

A credential works there
because HTTP has somewhere to put one. A tunnel carrying an arbitrary protocol
does not, so the parent identifies the peer instead. Every process a run launches is in
that run's Job Object. So the parent resolves the connection to a process and
refuses anything outside it. It does this in the parent because `GetExtendedTcpTable` is
ACCESS_DENIED inside an AppContainer. The parent refuses unverifiable peers instead of
admitting them.

Runs of one project still share a package and a capability. Treat
them as one trust domain and do not rely on the peer check to keep them apart.

That is what makes it defensible where the pre-0.5.0 loopback exemption was not.
`CheckNetIsolation LoopbackExempt` was machine-wide, permanent, opened *every*
service on 127.0.0.1 to the sandbox, and needed elevation to revoke. This grants
nothing at the OS level at all.

**A blocked write can report success.** A contained process writing to the user
profile root gets no error, reads its own file back, and stats it. The
host has no such file at that path. Windows redirects the write into a
per-container view instead of refusing it. Measured 2026-08-24, with the
contained process still running at the time of the host check, so this is not
cleanup racing the observation.

Containment holds either way, but it means an in-sandbox return value is not
evidence on its own, in either direction. Every probe here checks the host's
disk and what the contained process reported. That is why
`scripts/sandbox-enforcement-windows.ps1` asserts the forbidden path is absent
instead of trusting `WRITE_OUTSIDE=DENIED`.

## Docker provider

| Guarantee | Behavior |
|---|---|
| Host filesystem | Only the working directory is bind-mounted (`/app`); the rest of the host is not visible. |
| Environment secrets | Scrubbed before entering the container. |
| Hardening | `--cap-drop=ALL`, `--security-opt=no-new-privileges`, `--pids-limit`, `tmpfs /tmp`. |
| `network.mode: offline` / `loopback` | **Enforced** via `--network none` (no interfaces at all). |
| `network.mode: proxy` | **Not enforced** — the allowlist would be cooperative only, so proxy mode is disallowed under Docker. Use the native provider for allowlisted egress. |
| Docker not installed / not running | Fails closed with a clear error before anything launches. |



Measured, not read off the arguments. `scripts/sandbox-smoke-docker.sh` asserts
every row above except the proxy one against a container this project launches on
a Linux runner. The assertions are:

- the command ran inside a container
- the container mounts the project, and writes cross the boundary both ways
- a readable host file outside the project is not reachable
- `offline` refuses an outbound connection

Until that script existed the guarantees rested on unit
tests of the argument list, which is not the same thing.

## CI note

Linux, macOS and Windows each run an enforcement probe on a hosted runner of that
OS (⁵, ⁸). Windows was the exception until 2026-09-21. Hosted Windows runners
refused to create AppContainer children. `CreateProcess` returned "Access is
denied" for every executable, including `cmd.exe`, so anything that launched a
real contained process skipped there.

The cause was the launch asking Windows for
`CREATE_BREAKAWAY_FROM_JOB` inside a job that forbids it. See PR #52 and the
CHANGELOG entry about AI agent shells. The fix let the runner create
AppContainers.

CI run 37244525606 (2026-10-04) shows the result. Its Windows probe step ran the
whole package with `NVX_PROBE=1` and passed with 8 skips, none of them a refusal
to launch. Two were internal helper children. Three were spikes and prototypes gated behind
`NVX_IPC_SPIKE=1` or `NVX_PROBE_PROTOTYPES=1`. One was a check that has nothing to do on
a machine without a loopback exemption.

Two were probes whose premise the runner
does not meet. Those premises are no access for AppContainers to the user profile and no permission
to change the ACL of `C:\Windows\System32\cmd.exe`. In the same run
`sandbox-enforcement-windows.ps1` and both Windows smoke scripts ran their
assertions to the end and passed.

That does not make every Windows cell CI-backed. A passing run says the probes
launched and asserted, and says nothing about a cell no probe asserts.

**For the current numbers, read the run instead of this page.** Every CI run
prints them in its job summary and in the log as one greppable line:

```
NVX_PROBE_COUNTS pass=… skip=… fail=…
```

followed by the distinct skip reasons. `gh run view <id> --log | Select-String
NVX_PROBE_COUNTS` gets it, and the job summary shows it without opening a log.

This page used to quote a count instead, and it rotted twice. It said "441 pass,
21 skip" describing a run that was 442 and 35. A reviewer could not check the later
correction at all, because a developer machine *runs* the probes
that a hosted runner then skipped. A number nobody can reproduce is a number that goes
quietly wrong. The skip reasons are the signal worth reading. The totals are just
how you notice they changed.

`scripts/sandbox-enforcement-windows.ps1` was the by-hand answer to that. It
asserts the same five outcomes as the Linux probe (writes and reads denied
outside, both allowed inside, egress denied with an empty allowlist). It also runs
on a real Windows machine before a release (see CONTRIBUTING.md). It runs in
CI as well, where it now gets as far as its assertions. It still detects the two
refusals a host is known to give and skips. So a runner image that refuses again
shows up as a skip in the step's log.

Two things it deliberately does not cover. First, egress denial there is
direct-connection only. The AppContainer holds no network capability, so the
refusal does not depend on the allowlist. A machine carrying a leftover
pre-0.5.0 loopback exemption can still have egress forwarded through a loopback
service. See ³, and `nvx doctor` is the check for it. The script prints a warning
when it detects one.

Second, the smoke script's own host-write check writes through
the sandbox's redirected `%USERPROFILE%`, so it passes whenever redirection
works. The enforcement probe uses absolute paths resolved outside the sandbox
for that reason.

One Linux test skips unprivileged and is re-run as root in the same build.
Ubuntu 24.04's AppArmor hardening lets an unprivileged user create a user namespace
but refuses `CAP_NET_ADMIN` inside it. So nothing can bring loopback up, and
the egress test has nothing to measure. It probes for that capability instead of
for the namespace. The two answers differ, and only the first one
decides whether the test can run. CI relaxes
`kernel.apparmor_restrict_unprivileged_userns` before the smoke and enforcement
steps, which is why those do run unprivileged there.

¹⁰ **One AppContainer package per project, because loopback is package-scoped.**

Windows permits loopback *within* an AppContainer package. Until 2026-08-29 nvx
ran every sandbox on a machine under one package. So any port a contained
process bound was reachable from every other contained process, across
unrelated projects, with an empty allowlist and no `--connect`.

Measured with both controls, which is what pins it to the package instead of to
loopback generally:

| from | to | result |
|---|---|---|
| true host process | sandbox A's listener | DENIED (ETIMEDOUT) |
| sandbox B | a listener on the host | DENIED (ETIMEDOUT) |
| sandbox B (different project) | sandbox A's listener | **GOT the payload** |

Varying only the profile name confirmed it is the package identity. Windows
refused the same connection for two different names, and it succeeded when both
sides shared one.

Packages are per project now. **Two runs of the same project still reach each
other** (same package, same dependencies, same policy, one trust domain). That
is the boundary and not an oversight. `scripts/sandbox-enforcement-windows.ps1`
asserts the cross-project refusal with the same-project connection as its
positive control, so a sandbox that refuses everything cannot pass it.

The Linux column is No for a different reason. Each contained process gets its
own loopback-only network namespace, so there is no shared loopback to meet on.
macOS is Untested. Seatbelt does not namespace the network, and nothing here
stands up two contained listeners on macOS to check.

¹¹ **Loopback is reachable when the policy says so, and only then. This table
claimed otherwise until 2026-08-31.**

The rows above used to read "only with a leftover exemption" and "only via
`--connect`". Both were true of the pre-relay design and neither survived it. The
egress proxy runs in the parent, *outside* the containment, and dials on the
contained process's behalf. So a destination the allowlist permits is reachable
whether or not it is loopback, exemption or no exemption. An acceptance pass
demonstrated it with a listener on `127.0.0.1:51997`, no exemption on the machine
and no `--connect`. The payload came back through nvx's own proxy.

nvx intends that behaviour, and the docs site's policy page
(`site/src/content/docs/docs/policy.md`) documents it
(`"allow_hosts": ["localhost:5432"]`). A developer whose project talks to a local
Postgres or a local registry needs it, and the alternative they reach for is
`--no-sandbox`, which is worse. `PRODUCT.md` scopes the guarantee to "a host
outside the policy allowlist", and a host inside `allow_hosts` is inside it.

**nvx offers no loopback by prompt.** Whatever the sandbox is running raises the
unknown-host prompt, and that is the untrusted code. So a postinstall
could ask on its own behalf for the developer's local database. Localhost is
exactly where the services that take no credentials live. nvx now refuses a
loopback destination that is not already allowlisted, without asking, and points at
`allow_hosts` and `--connect`.

Approving any other host at the prompt lasts for
that run only. nvx used to write it into the grants store for ever.

So `--connect` is no longer "the only route to a host service". It is the only
*ephemeral, peer-verified* one. `allow_hosts` is the durable form, and being
durable is why you have to write it down instead of agreeing to it at a prompt.


¹² **`--connect` on macOS.**

macOS shares its loopback with the sandbox, so the profile denies a host service there
instead of leaving it unreachable. The Seatbelt profile in `proxy` mode permits the egress
proxy's own ports and stops at that. The profile could therefore name the
service's port and finish there.

nvx runs a listener anyway and opens only
that listener's port. That keeps one meaning for the flag across platforms.
It is the same command, the same two-number rule and the same `NVX_CONNECT_<port>`.
It is also the same property that nvx picks the destination while the contained
process picks the moment.

Windows needs a peer check on its tunnel because every run of one project there
shares one package identity. macOS needs none. A process outside any sandbox can open the
service directly already. Another sandbox cannot reach the listener, since
its own profile permits only its own proxy ports.

Two things back this. `TestSeatbeltConnectOpensTheRelayPortAndNotTheService`
asserts the generated profile names the listener and not the service, which is
the profile-only confidence every other macOS row carries.
`scripts/sandbox-smoke-macos.sh` goes further on CI hardware. It runs the same
contained fetch twice, once without `--connect` and once with, and fails if the
first one succeeds. A macOS that stopped enforcing the profile's network rules
would fail that smoke.

**Linux tunnels it, the way the egress proxy is already tunnelled.** Its sandbox
sits in a network namespace of its own. So 127.0.0.1 in there is a different
127.0.0.1, and no permission grants a route to yours. The supervisor listens on
the in-sandbox port inside the namespace and forwards over a UNIX socket in the
guest home. That crosses because it is a filesystem object. nvx dials the real
service from outside.

Linux needs no peer check either, and for a stronger
reason than on macOS. Another sandbox has its own namespace and its own guest
home, so neither half is addressable from it.

`offline` is the exception, and the refusal is loud. It installs
`buildOfflineNetworkFilter`, which denies `connect()` outright and denies creating
any AF_INET or AF_INET6 socket. So a contained tool cannot dial the in-sandbox
listener at all. Carrying `--connect` there would mean granting the mode an IP
socket, which is the thing it exists to withhold. nvx says so and names the modes
that can carry it, instead of accepting the flag and doing nothing.

nvx refused `loopback` the same way until 2026-09-25. The premise behind that
stopped being true on 2026-09-08, when the mode moved to the proxy filter (¹³). It
carries `--connect` now. The loopback redirect leaves the flag's own port alone.

`scripts/sandbox-smoke.sh` runs the same two-sided check the macOS one does. The
negative half is load-bearing in a different way. If nvx silently failed to create
a network namespace, the sandbox would sit on this machine's loopback.
The positive result alone would then prove nothing.

**The docker provider carries none of this, and reports it.** Every relay above
has an in-sandbox half that is a process of nvx's. That is the AppContainer supervisor,
the Landlock supervisor, or, on macOS, nvx itself on the other side of a profile
rule. The Docker provider has no such process, because it launches the target command
as the container's only one. The container has a network namespace of its
own, so there is nothing inside it to listen on the in-sandbox port. In `offline`
and `loopback`, the only two modes this provider enforces, `--network none` leaves
the container its own loopback and nothing else regardless.

Until 2026-09-08 this was silent. `dockerRunArgs` never read ConnectPorts. So a
policy carrying `connect_ports` launched a container that could not reach the
service, with nothing on screen connecting the two. It now warns and names the
native provider.

`TestDockerSaysItCannotCarryConnect` covers every platform and
mode, against `connectRefusalFor`. That is a pure function for the reason `dockerRunArgs`
is one. Reaching the decision through the launch path needs Docker
installed and a sandbox that starts.

¹³ **`network.mode: loopback`, and what it means on each platform.**

The mode's definition lives in one rule in the egress proxy. The proxy permits a
loopback destination without an `allow_hosts` entry, and only in this mode.

Until 2026-09-08 that rule was unreachable on three of the four backends, so the
mode was `offline` by another name wherever it was not macOS. Windows granted no
network capability and started no relay. Linux gave it `buildOfflineNetworkFilter`,
which denies `connect()` outright, so the contained process could not reach the
proxy that implements the mode. Docker runs it with `--network none`. Nothing
caught it. The only test of the mode was of the proxy rule itself, in a
state where nothing could consult the proxy.

Windows and Linux now route it through the relay, exactly as `proxy` mode does.
Neither gains any OS-level reach. Windows still holds no network capability,
Linux still runs in its own network namespace, and every destination is still the
parent proxy's decision. The single difference from `proxy` is that one rule.

**Linux carries raw connections too, by redirecting them.** Every loopback TCP
connection in the namespace goes to a relay. The relay asks the kernel through
`SO_ORIGINAL_DST` what the connection was for and carries it to the parent over
AF_UNIX. The parent dials that address, and refuses any that is not loopback.

That refusal is the enforcement point, and it is not a formality. The socket sits
in the guest home, so a contained process can skip the relay, open it directly
and name whatever address it likes. The address the tool dialled is the address
reached, which is the whole difference from `--connect`.

Two things it does not break. Rules that precede the redirect exclude nvx's own
listeners inside the namespace (the egress proxy relay and any `--connect` port).
So egress does not take a hop through this.

And a server the
SANDBOX runs stays reachable from inside it. The relay tries the namespace before
the host. So a contained dev server on 127.0.0.1:3000 and a contained test client
still find each other. They do not reach the developer's own port 3000.

A host whose kernel will not take the rules falls back to the proxy-mediated reach
and says so. The kernel refuses them when there is no iptables, or no nat table
inside an unprivileged user namespace. That is the safe direction and not a
fail-closed case. The host loses reach and keeps containment.

**Windows still covers only what a proxy-aware client sends**, since its reach
comes from the proxy alone. A raw socket to a local database works on macOS and
Linux and does not on Windows. This page records that gap and does not smooth it over.

Docker still refuses the mode's reach, for the reason it refuses `proxy`. nvx
would be handing the container a proxy address and trusting it to use one, so the
allowlist would be advisory. `--network none` stands.

`scripts/sandbox-smoke.sh` measures it on Linux CI twice over. As a CONNECT to the
proxy in both modes (refused under the default, tunnelled under `loopback`). Then as
a raw connection from a client that knows nothing about HTTP_PROXY, which
only arrives if the redirect is carrying it. The same raw client ran earlier under
the default mode and reported a failure. That is the control. Without it, success
here would equally be what a sandbox with an accidental route out looks like.

Windows has unit coverage of the two decisions
(`TestWindowsLoopbackModeRelaysAndHoldsNoCapability`) and no end-to-end run of the
mode itself.

¹⁴ **The project's `.git` is read-only to contained runs.**

The working directory is a writable root on every platform, and git never runs
contained. Until 2026-10-01 a contained install could write `.git/hooks` or
`.git/config`, and the next `git commit` ran what it left there as the user.
Now each platform takes `.git` back out of the writable root. It also takes the git
directory a `.git` file names when that lies inside the project:

- **Linux** has the supervisor bind-mount it read-only in its private mount
  namespace before Landlock applies. The supervisor also drops `CAP_SYS_ADMIN` from
  the target, so the target cannot change the mount back. `TestGitMetadataReadOnly*`
  covers this in the privileged CI step.
- **macOS** uses a `(deny file-write* ...)` rule after the profile's allow.
  `scripts/sandbox-enforcement-macos.sh` asserts it on the macOS runner, with
  writes to `package.json` and `node_modules` as the positive control.
- **Windows** differs. A deny entry for the project's capability did not hold.
  Measured
  2026-10-01 with the deny first in `.git`'s list, a contained process still
  created a hook, rewrote `config` and renamed `.git`. So `.git` stops inheriting
  from the project, keeps every other entry it had, and gives the capability read
  and execute only. `TestSandboxCannotWriteGitMetadata` and
  `TestSandboxCannotWriteGitMetadataFromSubdirectory` (NVX_PROBE=1).

Everything else in the project stays writable, because an install writes it:
`package.json`, `node_modules`, lockfiles and build output. That is why a
contained install can still affect `npm test` or `npm run build` run later at the
`standard` level.

## Measured costs and platform floors

Moved here from README. These are measurements instead of guarantees, and
they date quickly. Each carries the date and machine of its measurement.

- **`nvx setup` is optional, and nvx no longer asks you to run it.** Contained
  `npx` does need `C:\Users` and the drive root to answer a stat. npm's own
  realpath walks every directory above the cache `npx` uses. That cache is under
  the sandbox's home, and an AppContainer with no drive-root grant gets `EPERM`
  on both. Measured 2026-09-03. `npm install` in a project on `C:` works, and `npx -y
  cowsay hi` from the same project fails with `EPERM: operation not permitted,
  lstat 'C:\Users'`. An earlier version of this entry said `npx` needed no grant.
  Every run behind that claim happened while the grant was present.

  nvx now answers that stat itself. A preload in every contained node process
  replies with a directory's stats when the OS refuses the real ones. It does this
  only for the directories above the sandbox's own working directory and
  home. Those
  directories exist by construction, and the sandbox may already pass through
  them. The OS hid only their attributes. With it, `npx` runs contained on a
  machine that has never run `nvx setup`, as measured with the same `npx` from the same
  project and no grant.

  `nvx setup` still serves a non-node tool
  that walks to a drive root. If one fails with `EPERM` there, nvx names setup
  after the failure, and `nvx doctor` shows the missing roots as a note. The
  grant gives read/execute on the root folder itself, never inherited, for the
  sandbox's identity only.

  Setup grants every fixed volume. Each grant used to
  cost time proportional to the volume's size (22 minutes for 5.6 million
  entries). The write walked everything beneath the root. It no longer
  does.

  Measured 2026-10-04. On a directory holding 20,000 files, the write took
  2.67 to 3.01 s with the walk and under 1 ms without. No file's permissions changed
  either way. `nvx setup --undo` takes it back.
- **A contained command costs a few hundred milliseconds, and the first one after nvx
  stages a new runtime can be minutes.** A contained launch has to prepare an
  isolated home and check permissions, which the shim's own dispatch does not. The first run in a project is slower than the rest, because
  that is when nvx makes and remembers the permission grants.

  Measured 2026-08-24 on a Windows 11 machine, with ~2.4s for a project's first
  contained run and ~390ms for every one after. Re-measured 2026-08-29 on a second Windows 11 machine, with 2.9s
  first and 785ms steady (median of 8 runs). The same Node binary took 58ms
  when run directly. Plan against "a few hundred milliseconds to about a second"
  instead of either figure. The steady state moves by roughly a factor of two
  between machines, while the first-run cost reproduced closely.

  This used to add "of which ~210ms is Node's own startup". This page dropped that
  decomposition instead of re-guessing it. It does not reproduce (58ms here).
  Subtracting it left ~180ms for nvx against the ~370ms this section quoted for nvx's own
  setup, so the two figures could not both be right. This page replaced that second
  figure with a measurement too.

  Before 0.5.6 the steady state was ~650ms here, and other machines measured ~1s and
  ~2.2s. The cause was that
  nvx re-read every access-control entry on every launch, seventeen `icacls`
  processes a command. It now remembers the ones it has already verified.

  The first run after nvx stages a runtime copies the whole distribution, and
  measurements put it at 45s to 3 minutes. Uncontained commands are unaffected.
- **The first contained run after an install is slow, once, in proportion to the
  dependency tree.** Measured 2026-08-20. Loading a freshly installed 2,552-file
  package inside the sandbox took 5.8s the first time and 461ms every time after.
  The same load uncontained is ~500ms, so **steady-state containment costs
  essentially nothing here**. The one-off is the filesystem and antivirus caches
  filling while a sandboxed process reads thousands of files for the first time.
  It is not work nvx is doing. That is the marginal cost of a large file tree, not the
  cost of containing a command at all. On the machine re-measured above, an empty
  contained run took 785ms against 92ms for the same command uncontained.

  It matters only where something is waiting with a timeout. If you are wiring a
  contained command into a tool that gives up after a few seconds, run it once by
  hand after installing. That absorbs the cost.
- **Windows may flag nvx as malware, and SmartScreen may warn on a new
  download.** Releases from 0.7.0 on are Authenticode-signed. Observed on 2026-09-04. Windows Defender quarantined
  freshly built nvx binaries as `Trojan:Win32/Bearfoos.A!ml`, three times in one
  minute. `go build` could not produce an executable at all until the build
  directory had an exclusion.

  The `!ml` suffix marks a machine-learning verdict instead of a signature
  match. nvx is a plausible thing for such a classifier to dislike. It
  creates named pipes with custom security descriptors, manipulates AppContainer
  tokens, and rewrites filesystem ACLs. Those are the mechanisms nvx builds
  containment from, and they are also what malware does.

  Releases carry SHA-256 checksums and a SLSA build-provenance attestation, which
  let you verify a download came from this repository's CI. Defender and
  SmartScreen read neither. From 0.7.0, `nvx.exe` is also Authenticode-signed
  with a Certum certificate issued to "Open Source Developer Felix Stubner", and
  timestamped. So the signature stays valid after the certificate expires. The
  release workflow signs it in a job of its own and checks the signature before
  it makes any checksum or attestation. The first signed build, from a manual run
  on 2026-10-05, read back as Valid in Windows' `Get-AuthenticodeSignature`.

  A signature does not end SmartScreen warnings at once. SmartScreen judges a
  download partly by its reputation, and a certificate builds that up as people
  download and run what it signed. So the first signed releases can still show
  "Windows protected your PC". A Defender machine-learning verdict like the one
  above is also still possible on a signed file.

  If it happens to you, you can report a false positive to Microsoft at
  <https://www.microsoft.com/en-us/wdsi/filesubmission>. Reporting is worth more
  than an exclusion. An exclusion stops your machine scanning that path, which is
  a real reduction in your own protection, and it does nothing for anyone else.
- **Bun in the sandbox, per platform.** Measured 2026-09-08 with Bun 1.4.2.
  Contained on **macOS** it runs scripts and installs packages correctly.
  On **Linux** it does too, but only since the sandbox began mounting a
  procfs of its own. Bun reads `/proc/self` to size its stack, and before
  that a contained `bun install` failed with "JSON document is too deeply
  nested" against a valid file. Windows is below.
- **Bun does not run inside the Windows sandbox.** Measured 2026-09-17.
  `bun install` fails on every run, with `ENOENT` on 1.3.1 and `EBADF` on
  1.4.2, and `bun -e` cannot read its own working directory. The docs site's
  limitations page (`site/src/content/docs/docs/limitations.md`) has the
  detail. An earlier measurement, on 2026-09-06, found 1.4.2 running `bun
  install`, `bunx` and relative-path reads and writes contained, and 1.3.1
  failing every relative-path operation with `EBADFD`. The later run is the
  one that stands.

  Bun added
  AppContainer support in [oven-sh/bun#33119](https://github.com/oven-sh/bun/pull/33119),
  merged 2026-07-20 and shipped from 1.4.0. The related sandbox report is
  [oven-sh/bun#28220](https://github.com/oven-sh/bun/issues/28220), now closed.
  Older Bun keeps a working-directory descriptor captured at startup that an
  AppContainer will not honour, so absolute paths work and relative ones do not.
  Node is unaffected because it holds no such descriptor.

  `nvx --no-sandbox` is the escape hatch, which means running Bun **without**
  containment, so treat what it installs accordingly. npm and yarn run
  contained.

  Measured 2026-10-04 with Bun 1.4.2, `bun install` works contained on the
  system drive and fails with a bare `EBADF` on `D:` and `H:`. Bun rebuilds a
  file's path only for that drive ([oven-sh/bun#38365](https://github.com/oven-sh/bun/pull/38365)
  is the fix). When a contained `bun install`, `add`, `remove`, `update`, `patch`
  or `pm` fails in a project off the system drive, nvx now names that cause.
