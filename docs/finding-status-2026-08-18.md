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
| F33 | **partial** | Three of the four contradictions are resolved (F22, F2/F31, F23). The fail-closed claim is still contradicted by **F28**, which is open. |
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
| F34 | open | `--agent-mode`/`NVX_YES` still auto-approves every gate. |
| F18 | open | `policyLoosens` still has no `MaxDistance` check. |
| F38 | **fixed** | Loopback is allowlisted like any other destination. `network.mode: loopback` still permits it by definition; `offline` no longer does. Became urgent when the egress relay gave contained processes a route to the parent. |
| F30 | open | Neither seccomp filter validates `seccomp_data.arch`. |
| F35 | **fixed** | Guest homes record their owning pid; cleanup skips any whose owner is alive, and falls back to age when there is no marker. |
| F28 | open | `sandbox_native_other.go` runs the command with no isolation at all. |
| F29 | open | Only native, docker and seatbelt receive `NetCtx` (`fs_provider.go:79,103,122`); wsl/wslc/nspawn do not. |
| F36 | open | No `--user` anywhere; containers still run as root with the project bind-mounted. |
| F37 | open | A checksum **mismatch** is still reported as "Checksum file not available" (`install.ps1:113`). It fails closed, but the message misdescribes a tampering signal. |
| F15 | open | The lock writes a PID and never checks it for liveness (`version.go:247`). |
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
- **Providers never in scope** (F28, F29, F32, F36, F40) — wsl, wslc, nspawn,
  docker-as-root, and Unix platforms that are none of the big three. The native
  provider on the three supported platforms is where all the work went.
- **Real bugs, small blast radius** (F35, F15, F8, F6, F21, F44, F54) — each is a
  contained defect with a clear fix. F35 is the one I would take first: it deletes
  a live concurrent sandbox's home.
- **Hardening not yet done** (F30, F38, F19, F7, F37) — arch validation,
  unconditional loopback allow, signature verification, provenance checking, and a
  misleading checksum message.
- **Cosmetic / hygiene** (F10, F11, F45, F20, F55, F56, F57, F63, F66).

The severity distribution moved: every Critical and all but two High findings are
closed, and the two partials (F33, F48) are each blocked on a specific open
Medium/Low rather than on anything structural.


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
