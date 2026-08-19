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

| Guarantee | Windows (AppContainer) | Linux (Landlock + netns + seccomp) | macOS (Seatbelt) |
|---|---|---|---|
| Host filesystem write blocked (outside workdir + guest home) | Yes | Yes | Yes |
| Host filesystem read restricted | Yes⁴ | Yes | No² |
| Environment secrets scrubbed | Yes | Yes | Yes |
| Egress restricted to policy allowlist | Yes³ | Yes | Yes (loopback proxy) |
| Non-proxied raw TCP/UDP blocked at OS | Yes³ (no network capability) | Yes (loopback-only netns + seccomp) | Yes (`deny network*` except loopback) |
| Non-proxied DNS blocked | Yes³ | Yes (netns) | Partial¹ |
| Fails closed if a primitive is missing | Yes | Yes (Landlock 5.13+, iproute2 for netns) | Yes (needs `/usr/bin/sandbox-exec`) |

² On macOS the Seatbelt profile allows filesystem reads. The dynamic linker must
read system libraries and the dyld shared cache, whose locations vary by macOS
version (e.g. the Cryptexes firmlink on Apple Silicon) and cannot be enumerated
reliably; a strict read allowlist breaks process launch. Write containment and
egress control remain enforced, and environment secrets are scrubbed with `$HOME`
redirected to an ephemeral guest profile, so the sensitive material is still
protected.

¹ On macOS, egress is gated by the loopback proxy and OS network rules. Linux
additionally removes all non-loopback interfaces (network namespace), so DNS to
external resolvers cannot leave; on macOS a determined process could still
attempt DNS via the OS resolver. This is the main per-OS difference and is why
Linux has the strongest network story.

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

`network.mode: open` is the documented opt-out and is the only mode that grants a
network capability. Setup no longer registers a loopback exemption, and removes an
existing one, because the relay makes it an access grant with no remaining
purpose; `nvx setup` is now only about drive-root stat access.

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
