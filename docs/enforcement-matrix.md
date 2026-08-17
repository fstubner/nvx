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
| Host filesystem read restricted | Yes | Yes | No² |
| Environment secrets scrubbed | Yes | Yes | Yes |
| Egress restricted to policy allowlist | **No by default**³ | Yes | Yes (loopback proxy) |
| Non-proxied raw TCP/UDP blocked at OS | **No by default**³ | Yes (loopback-only netns + seccomp) | Yes (`deny network*` except loopback) |
| Non-proxied DNS blocked | **No by default**³ | Yes (netns) | Partial¹ |
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

³ **Windows egress is not restricted at all by default, and this table claimed
otherwise until 2026-08-17.** An AppContainer cannot reach a loopback listener
without a loopback exemption, which only an elevated `nvx setup` can add. Absent
that, `windowsSandboxNetwork` grants the `internetClient` capability and
`stripProxyEnv` removes the proxy variables, so the contained process connects
directly and the allowlist is never consulted — not even cooperatively. After an
elevated `nvx setup`, the sandbox is proxied and the allowlist applies as
described.

Everything else in the Windows column — filesystem write containment, read
restriction, environment scrubbing, fail-closed setup — is unaffected, and the
pre-install supply-chain checks run in the unsandboxed parent either way.

A no-elevation path does exist and has been measured: an AppContainer can reach an
AF_UNIX socket held by the parent (verified with no network capability granted at
all), and intra-container TCP loopback works. Together those allow the same
parent-side-proxy plus in-container relay design Linux now uses. It needs an
in-container supervisor process, which does not exist on Windows yet, so it is a
planned change rather than a current guarantee.

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
