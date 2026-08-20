# Finding status — re-checked 2026-08-18

Every finding in `docs/assessment-2026-08-16.md` (F1–F68), re-checked against
`main` at v0.5.0 rather than against the remediation log's own claims.

**29 fixed, 4 partial, 35 open**, plus two findings this pass added and closed (F69, F70). The fixed set is almost entirely the Critical
and High band; what remains open is almost entirely Medium and Low, plus one
platform (non-Windows/Linux/macOS Unix) and one provider family (wsl/wslc/nspawn)
that were never in scope.

Method: each row was checked by reading the current code at the cited location or
by running the relevant test. Where a fix was claimed by a commit but the
mechanism was not re-read, the row says so. This is deterministic code reading
unless marked otherwise — it is not a fresh adversarial pass, and it inherits any
blind spot the original assessment had.

## Critical — 4 of 4 fixed

| # | Status | Evidence now |
|---|---|---|
| F50 | **fixed** | `landlockReadOnlyRules` drops `READ_DIR` for non-directories (`sandbox_landlock_linux.go:173`). Linux containment tests run in CI. |
| F22 | **fixed** | `buildSeatbeltProfile` takes `(netCtx, guestHome, workDir)` and derives roots from `sandboxWritableRoots`; the darwin caller passes the same three. `nvxHome` is no longer in the write scope. |
| F1 | **fixed** | `labelLowIntegrity` is time-boxed (20s, `sandbox_windows.go:77`), the ancestor walk is budget-bounded, and launches are assigned to a job object with kill-on-close. |
| F64 | **fixed** | `lookupBinCache` validates that a cached path's directory is one the uncached resolver would search, excluding the shim dir. |

## High

| # | Status | Evidence now |
|---|---|---|
| F46 | **fixed** | `STARTF_USESTDHANDLES` and `bInheritHandles=1` are set together (`sandbox_appcontainer_launch_windows.go:159,163`). |
| F25 | **fixed** | `NVX_SANDBOX` is now corroborated by `containmentDisproved()`; an unbacked marker is ignored and warned about. |
| F3 | **fixed** | `sessionAllows` reads the map under `promptMu` (`egress_proxy.go:205`). |
| F24 | **fixed** | One shared syscall file, 444/445/446. Runtime behaviour on real arm64 hardware remains unverified — the assessment's own caveat, unchanged. |
| F23 | **fixed** | Filter rewritten with `sockTypeMask`; real-kernel probes in CI. |
| F2 | **fixed** | v0.5.0. No network capability is granted; egress goes through the relay. |
| F31 | **fixed** | Proxy stays in the parent, exposed on a UNIX socket, bridged by `startProxyRelay`. |
| F13 | **fixed** | v0.5.0 tagged and released; `CHANGELOG` current; the version test compares against the newest changelog entry. |
| F12 | **fixed** | Windows CI runs the AppContainer probes and both smoke scripts, and skips only when the host genuinely refuses. |
| F5 | **fixed** | Download-verification tests added; they exposed a real symlink escape, since fixed. |
| F4 | **fixed** | `install.sh` persists PATH, repairs broken profiles, and is regression-tested in CI on Linux and macOS. |
| F47 | **fixed** | v0.5.0. Staging recurses with `os.ReadDir`/`os.Stat`, which follow both junctions and symlinks. |
| F26 | **fixed** | Landlock grants `versions/`, `bin/`, `current/` — not all of `nvxHome`. |
| F65 | **fixed** | Follows from F22: the pin store is no longer writable by a contained process. |
| F33 | **fixed 2026-08-20** | All four contradictions resolved (F22, F2/F31, F23, and now F28). The fail-closed claim holds on every platform: the last exception ran commands unprotected where no sandbox exists. |
| F48 | **partial** | Fix B ✅, Fix C ✅, Linux `CLONE_NEWPID` + reaper ✅. Still missing: Part 1 project-dir gating (no `.git` check exists), macOS signal handler (F63), Part 4 automatic Tier 2 (F59). Fix A was *bounded* rather than removed — the ancestor walk still runs, now within a budget. |

## Medium

| # | Status | Evidence now |
|---|---|---|
| F14 | **fixed** | Both verify scripts check exit codes and pin tool versions. |
| F27 | **fixed** | Both egress smoke scripts assert the allow path as well as the deny path. |
| F9 | **fixed** | Local toolchain is go1.26.4; the 54-CVE go1.19 situation is gone. `go.mod` still says `go 1.23` against a 1.26.6 CI pin — drift, not exposure. |
| F41 | **fixed** | The test compares `appVersion` to the newest changelog entry instead of a hardcoded string. |
| F42 | **fixed** | Follows from F25. |
| F51 | **fixed** | `buildSeatbeltProfile` is no longer variadic. |
| F52 | **fixed** | `TestSeatbeltProfileDoesNotGrantWriteToNvxHome` asserts the negative. |
| F58 | **fixed** | `CLONE_NEWPID` is on the supervisor; `reapUntilChildExits` replaces `cmd.Wait()`. |
| F34 | **fixed** | The two decisions that widen the trust boundary -- trusting a loosening project policy, allowlisting an egress host -- ignore `-y`, `--agent-mode` and `NVX_YES`; they need an interactive answer or `NVX_TRUST_YES`. Ordinary prompts still honour `-y`. |
| F18 | open | `policyLoosens` still has no `MaxDistance` check. |
| F38 | **fixed** | Loopback is allowlisted like any other destination. `network.mode: loopback` still permits it by definition; `offline` no longer does. Became urgent when the egress relay gave contained processes a route to the parent. |
| F30 | open | Neither seccomp filter validates `seccomp_data.arch`. |
| F35 | **fixed** | Guest homes record their owning pid; cleanup skips any whose owner is alive, and falls back to age when there is no marker. |
| F28 | **fixed 2026-08-20** | `sandbox_native_other.go` refused to run instead. It used to execute the command with no isolation while nvx printed "Running in native sandbox", so a contained install on FreeBSD or any other unlisted Unix got full access to the home directory, SSH keys and network. Now it fails closed and names `--no-sandbox` as the deliberate opt-out. The dead `sandbox_unix.go` helper that logged "using environment isolation only" is deleted. Pinned by `TestUnsupportedPlatformRefusesInsteadOfRunningUnprotected`, which asserts the command left no trace behind rather than trusting the exit code. |
| F29 | open | Only native, docker and seatbelt receive `NetCtx` (`fs_provider.go:79,103,122`); wsl/wslc/nspawn do not. |
| F36 | open | No `--user` anywhere; containers still run as root with the project bind-mounted. |
| F37 | open | A checksum **mismatch** is still reported as "Checksum file not available" (`install.ps1:113`). It fails closed, but the message misdescribes a tampering signal. |
| F15 | **fixed** | A lock whose owner is provably gone is cleared and reported; a live or unparseable one is respected. Reproduced end to end during acceptance before fixing. |
| F6 | open | `Timeout: 60 * time.Second` still covers the body read (`download.go:43`). |
| F19 | open | No GPG/signature verification; disclosed in SECURITY.md. |
| F7 | open | Neither installer verifies the build-provenance attestation CI produces. |
| F8 | open | `storeBinCache` still writes a fixed `.tmp` name (`bin_cache.go:124`). The rename is atomic; the temp-name collision is not. |
| F16 | open | `build-release.ps1` has no `-trimpath`, no `CGO_ENABLED=0`, no tests, no provenance. |
| F17 | open | `test-nvx.ps1` still installs Node 18 and reads the pre-migration versions layout. |
| F39 | open | No abbreviated `Accept` header or size limit on the packument fetch. |
| F40 | open | `resolveWslcNodeImage` still returns `node:` images for every runtime. |
| F49 | open | Design-level; unresolved while Part 1 is unimplemented. |
| F53 | open | `agentModeFlag` has three references, all writes. Still no reader, still does not set `quietFlag`. |
| F59 | open | `usePersistentProfile` is still `toolName != ""`, so a daemon re-authenticates every restart. |
| F60 | open | Plan checkboxes still unticked. |
| F61 | open | `part5-grants` still has no supersession notice. |
| F62 | open | Cage spec still mis-describes `grants_trusted_tools.go`. |
| F66 | open | `hintIfShadowed` still runs before the containment decision (`env.go:607`). |

## Low

| # | Status | Evidence now |
|---|---|---|
| F43 | **partial** | Launch paths are now genuinely exercised: the Linux containment tests run in CI, the Windows AppContainer probes run in CI, and the new egress end-to-end test drives a real contained launch. `platformLaunchNative` / `runSandbox` / `runShim` still have no direct test callers. |
| F44 | open | A bare `policy.json` in any ancestor is still parsed as an nvx policy (`policy.go:344`). |
| F10 | open | No `.gitattributes`; `gofmt -l` still flags every file on a CRLF checkout. |
| F11 | open | `main()` is 182 lines, `launchAppContainerProcessOnce` 173 — both grew. |
| F20 | open | Bun's cached release list still falls back to a stale cache indefinitely on failure. |
| F21 | open | `MigrateLegacyNodeVersions` still warns rather than failing on errors. |
| F32 | open | `nspawn` still requires root and bind-mounts cwd read-write. |
| F45 | open | `min()` still shadows the builtin (`security.go:451`). |
| F54 | open | `TestParseStartupFlagsQuietAndAgentMode` still leaves `quietFlag` set for later tests. |
| F55 | open | Follows F18. |
| F56 | **fixed** | The test that asserted the old cleanup behaviour now back-dates its directory, so it still covers the abandoned case. |
| F57 | open | `policyLoosens` still compares trusted packages by length, with nothing pinning the coupling to `MergePolicies`. |
| F63 | open | No `signal.Notify` anywhere; macOS still has neither graceful nor hard cleanup. |

## What the open set actually is

Grouped by why it is still open, which matters more than the count:

- **Deliberate scope** (F34, F53, F59, F49, F17, F16, F60, F61, F62) — agent-mode
  semantics, daemon auth persistence, and the plan/spec documents. Each needs a
  product decision, not a patch.
- **Providers never in scope** (F29, F32, F36, F40) — wsl, wslc, nspawn and
  docker-as-root. The native provider on the three supported platforms is where
  all the work went. F28 was in this group and has since been fixed: "out of
  scope" was the wrong call for it, because the platform did not decline to
  contain, it ran the command unprotected while reporting that it had contained
  it.
- **Real bugs, small blast radius** (F35, F15, F8, F6, F21, F44, F54) — each is a
  contained defect with a clear fix. F35 is the one I would take first: it deletes
  a live concurrent sandbox's home.
- **Hardening not yet done** (F30, F38, F19, F7, F37) — arch validation,
  unconditional loopback allow, signature verification, provenance checking, and a
  misleading checksum message.
- **Cosmetic / hygiene** (F10, F11, F45, F20, F55, F56, F57, F63, F66).

The severity distribution moved: every Critical and all but two High findings are
closed. F33 has since closed too, when its blocking finding (F28) was fixed on
2026-08-20, leaving F48 as the only partial.


## Added by the adversarial pass, 2026-08-18

Neither is in the original 68. Both were found by probing rather than reading, and
both are fixed.

| # | Severity | Finding | Status |
|---|---|---|---|
| **F69** | High | **A sandboxed command could read and write every project nvx had previously run in, read a concurrent session's guest home, and read a `tool_home` profile's credential.** The AppContainer profile is stable by design so every session shares one identity, and the grants nvx adds are never revoked, so a permission added for project A was still satisfied in project B. This contradicted README.md directly. | **fixed** — writable roots are granted to a capability derived from the project; stale shared-identity ACEs are removed on first run in an affected project |
| **F70** | High | **The egress relay opened a route to the host's loopback services.** `EgressProxy.allowed` permitted every loopback destination unconditionally (F38), which was inert while nothing contained could reach the proxy. Putting the proxy outside the containment and relaying to it meant a contained process could reach any local service with an empty allowlist. Introduced by the 0.5.0 relay work. | **fixed** — see F38 |

Method note, since it is the reason these were missed for so long: F69 was checked
against the real home directory earlier in the day and the *other projects* half of
the same sentence was never exercised. The claim shipped on the strength of a test
that did not cover it. A control probe was run before diagnosing F69, confirming
that a directory nvx never grants is unreachable — without it the cause could have
been an inherited ALL APPLICATION PACKAGES ACE, and the fix would have been aimed
at the wrong thing.

## Added by acceptance, 2026-08-18

| # | Severity | Finding | Status |
|---|---|---|---|
| **F71** | Medium | **`nvx doctor` performed an unrequested persistent system change** -- it rewrote the real Windows user PATH on sight, targeting whichever `NVX_HOME` was set, so running it against a throwaway home fronted the real PATH with a temp directory. Found by it happening to the acceptor mid-review. | **fixed** -- diagnosis is read-only; repair moved behind `--fix` |

## Added by independent acceptance, 2026-08-19

Found by a separate context that had not seen the build, running the acceptance
checklist against the shipped binary. Ordered by severity.

| # | Severity | Finding | Status |
|---|---|---|---|
| **F72** | Critical | **Installing any package with a lifecycle script hung forever on Windows.** A contained process cannot create a named pipe; Windows builds piped child stdio out of named pipes; npm pipes lifecycle-script output by default. The hang was inside libuv, before the child existed, so the target's own timeout never fired. | **fixed** -- lifecycle scripts inherit stdio; smoke test added and revert-checked |
| **F73** | High | **FIXED.** No interception in Git Bash on Windows, and `nvx doctor` reported the opposite. The shim directory holds only `.cmd`/`.ps1`, which bash will not select for a bare `npm`, so installs run unaudited and unsandboxed. `doctor` checks Windows `PATHEXT` resolution rather than the shell it is running in, and reports healthy. Hits the stated flagship user: agent harnesses on Windows commonly run Git Bash. | **fixed** -- extensionless shims are written on Windows too; doctor reports their absence and no longer calls it healthy |
| **F74** | High | **FIXED.** Prompts hung instead of failing closed when stdin is not a terminal. `PromptYesNo` opens `CONIN$` and treats "a console exists" as "a human is present", ignoring redirected stdin. README and SECURITY.md both promise the operation is denied in that case; it neither approves nor denies. | **fixed** -- interactivity is decided by GetConsoleMode (Windows) / a TCGETS-TIOCGETA ioctl (Unix), so a redirected stdin denies instead of blocking |
| **F75** | Medium | **FIXED.** `nvx --strict` was silently discarded in the position `nvx help` implies; only a leading `nvx --strict shim ...` takes effect. The anti-bypass reasoning is right for `--no-sandbox` and backwards for a flag that increases containment. | **fixed** -- honoured wherever it appears; --standard still is not, because it reduces containment |
| **F76** | Medium | **FIXED.** Sandbox launch cost seconds per invocation in steady state, against a published figure of ~38ms for shim dispatch. PRODUCT.md's constraint is that overhead stays invisible. | **fixed** -- grants that cannot succeed are no longer retried every launch; 5.3s to ~1.05s |
| **F77** | Medium | **PARTLY FIXED.** A contained process could list the names in `%USERPROFILE%` (`.ssh`, `.aws`, ...) though contents are denied. `docs/enforcement-matrix.md` says the ancestor grant permits walking through a parent "without reading what else is inside it"; listing the names is reading what is inside it. | **partly fixed** -- nvx's own ancestor grants are now traverse+stat (X,RA) rather than (RX), so a directory nvx grants is not listable. The profile root still is, from the ALL APPLICATION PACKAGES ACE Windows ships; nvx never granted it, and deny ACEs were already measured not to override it |
| **F78** | Low | README's CLI Usage block is stale against `nvx help`; a local tarball path is sent to the registry as if it were a package name. | open |

Method note. F72 is the one that matters most about the method: the suite was
green, and stayed green, because every test launched contained children *from the
parent* and none had a contained process spawn one. The builder's single
postinstall test used `--foreground-scripts`, the one npm flag that switches
lifecycle scripts from piped to inherited stdio, and so walked around the defect
and was then cited as evidence containment worked. F74 was observed by the builder
the day before, diagnosed as "just an interactive prompt", worked around with
`NVX_YES`, and not filed.

## F76 measured and fixed, 2026-08-19

Not yet fixed. Measured first, because the acceptance pass could report the total
and not where it went, and optimising the wrong phase is the likely outcome of
guessing.

Reproduced: `nvx --strict shim node -e "..."` takes 5.3-7.0s against 0.17s for the
same command with `--no-sandbox`. (The acceptance pass measured 13.2s on a
different project; same order -- seconds, not milliseconds.)

Per phase, cold and warm:

| Phase | cold | warm |
|---|---|---|
| ensureAppContainerSID | 25ms | 15ms |
| scopeCapabilitySID | 0ms | 0ms |
| grantSandboxModify(guestHome) | 59ms | 25ms |
| labelLowIntegrity(guestHome) | 21ms | 21ms |
| grantSandboxModify(workDir) | 44ms | 23ms |
| grantWorkdirAncestors(workDir) | 0ms | 0ms |
| **grantWorkdirAncestors(guestHome)** | **3057ms** | **3054ms** |
| stageAppContainerSupervisor | 26ms | 8ms |
| launchAppContainerProcess (no-op child) | 2025ms | 20ms |

One phase is nearly all of it, and it does not improve when warm. Per ancestor:

| Ancestor | has-grant check | grant #1 | grant #2 |
|---|---|---|---|
| `AppData\Local\Temp` | 37ms (false) | 1529ms | 1530ms |
| `AppData\Local` | 27ms (false) | 1529ms | 1527ms |
| `AppData` | 31ms (false) | 1527ms | 1529ms |
| `<nvxHome>/sandbox_home` | 22ms (false) | 44ms | 22ms |
| `<nvxHome>` | 24ms (false) | 43ms | 21ms |

**The cause.** The `icacls` WRITE on the AppData chain hangs and is killed at the
1500ms per-path timeout, every time. The cheap `appContainerHasGrant` read is not
the problem -- it answers in ~30ms, and it answers `false` forever, because the
grant it is checking for never lands. So nvx retries a grant that cannot succeed
on every launch, burning the whole 3s ancestor budget, for any project whose path
runs through AppData. This is the OneDrive/Defender filter-driver stall the budget
was added for; the budget bounded the damage and nothing ever stopped the retry.

Ancestors under `<nvxHome>` are fast, so a project outside AppData pays far less --
which is why ordinary use never surfaced it, and why the smoke tests, which run
from a project directly under the user profile, never showed it.

**The other 2s.** The first `launchAppContainerProcess` costs 2025ms cold and 20ms
warm. That is a one-off per profile rather than per launch, so it is not part of
the steady-state cost.

**Fix not attempted here**, and the choice is a real one rather than obvious:

1. Remember the failure. Persist which ancestor paths timed out and stop retrying
   them for a period. Keeps the grant working where it works.
2. Stop granting them. The evidence says these grants never succeed and the
   sandbox works anyway -- installs complete, both smoke scripts pass -- so they
   may simply not be needed on the AppData chain, where ALL APPLICATION PACKAGES
   already provides traverse.
3. Drop the per-path timeout to ~200ms. Cheapest, bounds the damage without
   deciding whether the grants matter at all.

(2) is the most likely to be right, and needs one measurement to confirm: whether
a contained process can traverse the AppData chain with no nvx grant at all. That
is the same shape as the ungranted-directory control probe, and it should be run
before any of these is chosen.

### The fix, and the measurement that chose it

The deciding question was whether the ancestor grants are needed at all. Measured
by preparing a sandbox exactly as a launch does but skipping the ancestor walk
entirely, then asking a contained child what it could still do:

```
ancestors deliberately NOT granted: [AppData\Local\Temp, AppData\Local, AppData]
STAT_WORKDIR=OK   WRITE_WORKDIR=OK
STAT_ANC0=DENIED  STAT_ANC1=OK  STAT_ANC2=OK
```

The container launched, statted and wrote its working directory with no ancestor
grants at all -- and the launch itself had to traverse that chain to read the
child executable out of the guest home, so reaching the output already proved the
point. Two of the three ancestors are statable anyway, from ACEs Windows ships.
The one that is not (`AppData\Local\Temp`) is exactly the grant that never lands,
so nvx was not providing it before either.

So the failing grants bought nothing and cost 1.5s each, every launch, forever.
They are now remembered and not retried for seven days -- a limit rather than
forever, because the cause is environmental (a filter driver, an antivirus policy)
and a machine that starts working should recover without anyone needing to know a
cache file exists. A grant that succeeds clears any old record immediately.

Result on the same command that measured 5.3s: **~1.05s steady state**, against
0.17s for `--no-sandbox`.

Not fixed, and a separate problem: the FIRST run in a fresh nvx home took 64s,
because with no nvx-managed runtime the whole host node distribution is copied
into `sandbox-exec`. That is a one-off per runtime rather than per launch.
