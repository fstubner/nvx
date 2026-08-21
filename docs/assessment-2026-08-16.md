# nvx — Engineering Assessment

**Date:** 2026-08-16
**Commit:** `2891434` (branch `audit-remediation`)
**Scope:** whole repository — 78 Go files (13,709 LOC), installers, CI workflows, scripts, docs, specs
**Method:** full source read, automated checks, and an execution-based validation harness
(Windows host; Linux findings validated in `golang:1.26` containers, privileged, kernel 6.18)

---

## 1. Executive summary

**nvx's core feature — the sandbox — does not currently work on any of its three
platforms.** This is not "has weaknesses"; on each platform it is either
non-functional or fails to deliver the containment it advertises:

| Platform | State | Finding |
|---|---|---|
| **Linux** | Sandbox **cannot start any command at all**. `applyLandlockSandbox` fails on `/dev/null` on every Linux system. | F50 |
| **macOS** | Default path grants the sandboxed process **write access to `~/.nvx`** (policy, grants, credentials) and to the runtime binary directory — a persistent sandbox defeat. | F22 |
| **Windows** | Piped stdio never reaches the child, so every stdio-protocol daemon (all MCP servers) fails deterministically; abandoned launches accumulated until the tool had to be uninstalled. | F46, F1 |

Every one of these was reproduced by execution, not inferred.

**The design is not the problem.** The threat model, cascading-policy trust
boundary, grant model, fail-closed posture, and the honesty of
`docs/enforcement-matrix.md` are genuinely well-considered — better than most
tools in this category. The policy-trust logic is correct *as a mechanism*:
content-hash pinning with independent re-verification, no TOCTOU, and
blocklists/trusted-lists unioned so a project file cannot subtract protection.

**But that mechanism is void on macOS today, and an earlier draft of this
summary wrongly called it "correct under adversarial reading" without
qualification.** Pinning binds only while the pin store is beyond the
adversary's reach, and F22 makes `~/.nvx` — pins, grants, global policy, and the
binary-resolution cache alike — writable to the contained process. Composed, that
turns F22 from a sandbox defeat into unsandboxed code execution as the user
(**F64**, Critical) and nullifies the trust boundary (**F65**). See §11: the most
severe finding in this document exists only in the interaction between
components that are each correct on their own terms.

**The problem is that the enforcement layer has never been verified.** Test
coverage is 34.3%, concentrated on pure helper functions — classification,
flag parsing, policy merging — which are the easy parts. **No test calls any
launch or dispatch entry point** (`platformLaunchNative`, `runNativeSandbox`,
`runSandbox`, `runShim`). Every Critical and most High findings live at exactly
that untested boundary where the OS primitives are assembled.

**CI does not compensate.** All three sandbox smoke tests skip in practice:
Windows checks `if ($env:GITHUB_ACTIONS -eq 'true') { exit 0 }` unconditionally;
Linux exits 0 when `unshare -n` is unavailable, which it is for a non-root
runner; and the one egress test that does run only asserts that blocked traffic
*fails* — a sandbox that denies everything passes it perfectly.

**Correction — CI is not green; it is red and has been ignored.** An earlier draft
of this summary said "the CI badge is green over a feature nothing exercises."
That was wrong, and wrong in the more damaging direction. `go test -race ./...`
runs on `ubuntu-latest`, `macos-latest` and `windows-latest`, and it **fails on
the first two** — `TestIsPackageManagerCommand` asserts `C:\x\npm.cmd`
unconditionally, and `filepath.Base` does not treat `\` as a separator off
Windows. Introduced in `2f40a41` (2026-07-07), so **51 commits and five weeks of
red CI on two of three platforms** (F67, now fixed). The finding is not "green CI
manufactured false confidence" but "CI has been failing for over a month and
nobody read it" — which also means the smoke-test skips were never the binding
constraint on CI's usefulness.

**Blast radius today is zero, and that is the opportunity.** The published
release (`v0.2.0-beta`) is 86 commits behind `main`, has 0 stars/forks, and
predates all of this. Nothing is deployed to users. This is the cheapest moment
this project will ever have to fix its foundation — but it also means no
external party has ever validated any of these guarantees.

**Recommendation:** freeze feature work (the cage split, polyglot runtimes),
fix the six blocking defects, and make CI genuinely execute the sandbox on at
least one platform. Until something runs the enforcement path end to end, any
claim nvx makes about containment is unverified by construction. With zero
users there is no pressure to ship a half-product — fix containment first
(see §7 for why the supply-chain checks cannot substitute for it).

---

## 2. Tooling results

| Check | Result |
|---|---|
| `go build ./...` | Pass |
| `go vet ./...` | Pass |
| `go test ./...` | Pass **on Windows only** — 34.3% coverage, 198 functions at 0%. Fails on linux/macOS at HEAD (F67); every "suite passes" claim in §2 and §8.5 was measured on Windows and should have been qualified. |
| `go test -race` (targeted) | **FAIL — data race confirmed** at `egress_proxy.go:153` |
| `gosec` (CI args) | 0 issues |
| `gosec` (unfiltered) | 83 issues, all LOW/MEDIUM, inherent to a syscall-heavy process launcher |
| `govulncheck` | 54 stdlib vulns — artifact of a local **go1.19** toolchain; CI pins 1.26.4, so releases are unaffected |
| `gitleaks` (125 commits) | No leaks |
| `gofmt -l` | 24 files — CRLF only; index is LF |

---

## 3. Findings

Severity per the standard rubric. **Validation** is the key column:
`executed` = proven by running code; `verified` = confirmed against an
authoritative external source; `read` = deterministic code reading, not run;
`inferred` = follows from code but not demonstrated.

### Critical

| # | Finding | Evidence | Validation |
|---|---|---|---|
| **F50** | **Linux sandbox cannot start any command.** `applyLandlockSandbox` adds `/dev/null`, `/dev/urandom`, `/dev/random`, `/dev/zero` with `landlockAccessReadExec`, which includes `LANDLOCK_ACCESS_FS_READ_DIR`. Landlock rejects a directory-only right on a character device with `EINVAL`, and the code treats it as fatal. Every Linux system has `/dev/null`. Fail-closed, so not a security hole — the feature is simply dead. | `sandbox_landlock_linux.go:150-164`. Differential test, same ruleset fd: `/usr` +READ_DIR → ok; `/dev/null` +READ_DIR → `invalid argument`; `/dev/null` −READ_DIR → ok. End-to-end: `landlock read rule for "/dev/null": invalid argument` | **executed** |
| **F22** | **macOS default path defeats its own sandbox.** `buildSeatbeltProfile` has two callers: `sandbox_seatbelt.go:58` passes 2 writable roots; `sandbox_native_darwin.go:22` passes 4 — adding `config.NvxHome` and the runtime binary's directory. The latter is the **default** provider. A sandboxed process can rewrite `policy.json`, self-approve grants, poison `npm_global`, read/write `tool_home` credentials, or trojan the node binary. | Calling the real builder with the darwin caller's args emits `(subpath "/Users/x/.nvx")` as writable. `git show --stat bc0089f` confirms the July fix touched only `sandbox_seatbelt.go` | **executed** |
| **F1** | **Windows sandbox launch accumulates orphaned processes without bound.** Each spawn gets a fresh guest home, so its ACL grant can never be cached; `labelLowIntegrity` runs `icacls /t` with **no timeout** (the only icacls call not time-boxed). Observed live: 193 `node.exe` + 103 `nvx.exe`, growing continuously, until nvx was removed from the machine. | `sandbox.go:139-140`, `sandbox_windows.go:71`; process counts observed directly | **executed** (observed) |

### High

| # | Finding | Evidence | Validation |
|---|---|---|---|
| F46 | **Piped stdio never reaches the sandboxed child.** `bInheritHandles=0` and `si.Flags=0` mean the assigned `StdInput/Output/Err` handles are ignored. A console-attached child still inherits the console (so terminal use works), but **pipe handles are not inherited** — so every stdio-JSON-RPC daemon fails deterministically. This is the precise cause of the MCP incident. Specified as "Fix B" on 2026-07-20; never implemented. **Fix B must set both `STARTF_USESTDHANDLES` and `bInheritHandles=TRUE`** — see the third probe variant below. | `sandbox_appcontainer_launch_windows.go:161,208`; `docs/superpowers/specs/2026-07-20-*.md`. Probe replicating the exact `CreateProcessW` call shape, stdout assigned to a pipe: current shape → **0 bytes on the pipe**, child exit 0, output leaked to the *parent's* console (why terminal use looks fine); `USESTDHANDLES`+`inherit=1` → **12 bytes, marker received**; `USESTDHANDLES` alone → **child exit 1** | **executed** |
| F25 | **One environment variable disables containment, defeating `--strict`.** `NVX_SANDBOX=1` short-circuits `shouldSandbox`. It is nvx's own re-entrancy marker but is plain and forgeable: a `package.json` script — uncontained under the default level — can export it so nested installs run un-contained. The same file deliberately blocks this exact bypass for CLI flags. Supply-chain checks still run (they precede `shouldSandbox`); only OS containment is lost. **The check cannot simply be deleted:** `inSandboxSession()` (`sandbox_session.go:11`) is an in-process atomic counter, so `NVX_SANDBOX` — injected into the child env at `sandbox.go:247` — is the *only* cross-process re-entrancy signal. A real fix needs an unforgeable substitute, not a removal. | `shim_options.go:67` vs `:15-19`. Executed: baseline enters sandbox; with the var set, "Running directly (not sandboxed)" | **executed** |
| F3 | **Data race in the egress proxy → unrecoverable crash.** `p.session` is read unlocked at `:153` and written under `promptMu` at `:183`, from per-connection goroutines. Concurrent map read/write is a fatal Go runtime error. Reachable in the default `proxy` mode by ordinary parallel npm traffic. | Race detector: `WARNING: DATA RACE` at `egress_proxy.go:153`; test FAILs under `-race` | **executed** |
| F24 | **`linux/arm64` Landlock syscall numbers are off by one** — 445/446/447 instead of 444/445/446, so `restrict_self` invokes `memfd_secret`. A published release target. Fails closed, but the sandbox is unusable there and the error misleadingly blames the kernel version. | `sandbox_landlock_syscall_arm64.go`; `asm-generic/unistd.h` (the table arm64 uses) confirms 444/445/446 and 447 = `memfd_secret` | **verified** |
| F23 | **Linux proxy-mode seccomp filter is inverted.** IPv4 TCP → DENY, IPv4 UDP → ALLOW (the one thing it claims to block), IPv6 TCP → DENY, **AF_UNIX → DENY**. Cause: instruction `[5]` is reachable from `[3]`'s false branch without `args[1]` loaded. Currently unreachable dead code behind F50 — fix both together. | `sandbox_network_seccomp_linux.go:115-134`; executed via a cBPF interpreter against the real filter, with `buildOfflineNetworkFilter` as a passing control | **executed** |
| F2 | **Windows egress is not allowlisted by default**, contrary to `README.md:234` and `enforcement-matrix.md:25`. Without elevated `nvx setup`, the sandbox is granted `internetClient` and runs direct — the code comment says so explicitly. | `sandbox_native_windows.go:176-177`, `sandbox_loopback_windows.go:9` | read |
| F31 | **Linux proxy mode has no route to allowlisted hosts.** The egress proxy is started *inside* the loopback-only netns (`setupLoopbackNetworkNamespace` at `:174`, then `startEgressProxy` at `:189`); the parent's proxy port is threaded through the CLI then discarded. | `sandbox_landlock_linux.go:172-195`; the discard is `_ = proxyPort` at `sandbox_network_seccomp_linux.go:46`. Executed in-container: `dial tcp 1.1.1.1:443: connect: network is unreachable`, loopback still works | **executed** |
| F33 | **`SECURITY.md`'s threat model is contradicted in four places**: fail-closed (F28), write confinement (F22), proxy-mediated egress (F2, F31), seccomp blocking non-proxied DNS (F23). All four fall inside its own declared in-scope categories. | `SECURITY.md:45-55,65-69` | read |
| F48 | **The approved remediation for the incident was never implemented.** Of the 2026-07-20 spec: Part 1 (project-dir-gated containment) ❌, Fix A (remove ancestor walk) ❌ (today's fix moved the opposite way), Fix B (stdio) ❌, Linux `wait4` reaping ❌; only Fix C (job object) was done, on 2026-08-15. | grep across `shim_options.go`, `sandbox_appcontainer_*.go`, spec Part 3 | **executed** (grep) |
| F13 | **Release discipline has collapsed.** 86 commits since the published release, 83 since the newest CHANGELOG entry (`[0.3.0]`, dated 2026-07-05); `## [Unreleased]` empty; `0.3.0` never tagged (tags: `v0.1.0`, `v0.2.0-beta`) while code and README claim 0.3.0. Users download a build predating every security fix. | `git rev-list v0.2.0-beta..HEAD --count` = 86; `git log --since=2026-07-05` = 83; `git tag -l`; `version.go:14` | **executed** |
| F12 | **Windows CI smoke tests always skip**, so the step reports green having verified nothing. | `scripts/sandbox-smoke.ps1:15-18`, `sandbox-smoke-egress.ps1:8-11` | read |
| F5 | **Checksum verification has 0% test coverage** — `VerifyNodeChecksum`, `VerifyChecksumFromShasums`, `verifyExpectedSHA256`, `ComputeSHA256` all 0.0%, no test references them. Only the *path-traversal* guards are covered: `safeArchiveTarget` 100%, `safeArchiveTargetStrip` 80% (`TestSafeArchiveTargetRejectsEscapes`). The other two extraction guards are **not** — the zip-bomb byte cap `copyArchiveFile` is 0.0%, and the tar symlink-escape check lives inside `ExtractTarGz`, also 0.0%. Both read as correct; neither is exercised. | `go tool cover -func`; `download.go:416-435` (symlink guard), `:505` (bomb cap) | **executed** |
| F4 | **The Unix installer never persists PATH.** `install.sh` writes only `eval "$(nvx env)"` to the profile and merely echoes the export. `nvx` is not on PATH, so the profile line emits `nvx: command not found` on every new shell and nvx never activates. `install.ps1` does it correctly. | `install.sh:107,121,143` vs `install.ps1:41` | read |
| F47 | **Staging a runtime whose directory is a junction fails.** `copyDirTree` → `filepath.Walk` lstats the junction as a non-directory and tries to copy it as a file. Triggers whenever a runtime resolves through a symlink/junction dir outside `~/.nvx/versions` — including nvm-windows' `C:\Program Files\nodejs` junction. | Isolated test reproduces the exact error: `open <dest>: is a directory` | **executed** (mechanism) |
| F26 | Linux Landlock grants read+exec over all of `nvxHome`, which contains `tool_home` — other tools' persisted credentials — plus `grants/` and `policy.json`. | `sandbox_landlock_linux.go:154-156`, `sandbox.go:107` | **inferred** (blocked by F50) |

### Medium

| # | Finding | Validation |
|---|---|---|
| F34 | `--agent-mode`/`NVX_YES` auto-approves every security gate — typosquat, install-scripts, release-age, and "proceed despite active vulnerabilities". `-y` semantics are intentional; the defect is positioning it for unsupervised AI-agent safety while disabling every gate. | **executed** |
| F18 | `policyLoosens` ignores `Typosquatting.MaxDistance`, so a project policy can silently narrow detection without the trust prompt. | **executed** |
| F38 | The egress allowlist permits **all** loopback destinations unconditionally, contradicting documented `allow_hosts` gating for local services. | **executed** |
| F30 | Neither seccomp filter validates `seccomp_data.arch` — the standard x32 bypass on x86_64. | **executed** |
| F14 | `verify-security.ps1` prints "All security scans completed successfully!" while gosec reports 53 issues (PowerShell does not trap native non-zero exits). The `.sh` twin always fails. | **executed** |
| F35 | `nvx cleanup` deletes every guest home under `sandbox_home`, including those of live concurrent sandboxes. | read |
| F28 | On non-Windows/Linux/macOS Unix, nvx runs with **no OS isolation** and only logs an info line — contradicting the documented fail-closed stance. `netCtx` ignored entirely. | read |
| F29 | `wsl`, `wslc`, and `nspawn` providers take no `NetworkLaunchContext` — `network.mode: offline` is silently ignored. | read |
| F36 | Docker and wslc containers run as **root** with the project bind-mounted read-write, so created files are root-owned on the host. `wslc` also omits every hardening flag the Docker path sets. | read |
| F37 | `install.ps1` misreports a checksum **mismatch** as "Checksum file not available" then "Failed to download" — masking a tampering signal. The intended cleanup/exit lines are unreachable. Still fails closed via the catch. | read |
| F15 | A killed install leaves a stale lock that permanently blocks reinstalling that version; the PID is written but never checked for liveness. | read |
| F6 | `DownloadFile`'s 60s `Client.Timeout` covers the body read, so 25–60 MB runtime archives fail below ~4–8 Mbps. | read |
| F19 | Runtime downloads are checksum- but not signature-verified; Node's GPG-signed `SHASUMS256.txt.sig` is never fetched. Disclosed in SECURITY.md. | read |
| F7 | CI generates build provenance attestation; neither installer verifies it, so the guarantee is unused. | read |
| F27 | The egress smoke test only asserts denial, never that allowlisted egress succeeds — a deny-everything regression passes it. | read |
| F8 | `storeBinCache` writes a fixed temp filename, so concurrent nvx processes (routine in npm lifecycles) can interleave; the "atomic" comment overstates the guarantee. | read |
| F9 | Local toolchain is go1.19 while `go.mod` says 1.23; locally built binaries carry 54 stdlib CVEs. Releases unaffected. | **executed** |
| F16 | `build-release.ps1` omits `-trimpath`, `CGO_ENABLED=0`, tests, the combined manifest, and provenance — weaker artifacts than CI. | read |
| F17 | `test-nvx.ps1` contradicts shipped behavior (runs `npm install -g`, now refused under the sandbox) and reads the pre-migration versions layout. | read |
| F39 | `ResolveNpmPackageDetails` fetches the full packument per package — no abbreviated `Accept`, no size limit. | read |
| F40 | `wslc` hardcodes `node:` images, so Bun under that provider runs in a Node image. | read |
| F41 | `TestAppVersionMatchesCurrentBetaRelease` hardcodes `"0.3.0"`, actively pinning the version drift in place. | **executed** |
| F42 | `TestShouldSandboxHonorsSandboxEnvironment` enshrines the `NVX_SANDBOX` bypass as correct behavior; fixing F25 requires changing this test. | **executed** |
| F49 | The containment-v2 spec justifies the nested-invocation short-circuit with "the outermost boundary contains the whole subtree", but its own Part 3 makes `npm run` uncontained by default — so often there is no outer boundary. F25 is an emergent flaw from composing two individually-sound parts. | read |

### Low

| # | Finding |
|---|---|
| F43 | No test calls any launch/dispatch entry point — the structural reason F22 survived. |
| F44 | Bare `policy.json` in any ancestor directory is parsed as an nvx project policy, colliding with OPA/IAM/Terraform files. **executed** |
| F10 | No `.gitattributes`; `gofmt -l` flags 24 files on Windows checkouts (CRLF only). |
| F11 | `main()` 173 lines; `launchAppContainerProcessOnce` 169. |
| F20 | Bun's cached release list never expires on network failure. |
| F21 | `MigrateLegacyNodeVersions` silently ignores rename errors. |
| F32 | `nspawn` requires root and bind-mounts cwd read-write → root-owned host files. |
| F45 | `min()` shadows the Go 1.21 builtin; `CONTRIBUTING.md:55` advises `gofmt -w`, which rewrites all CRLF files. |

**Totals: 68 findings — 4 Critical, 17 High, 32 Medium, 15 Low.**
36 proven by execution, 1 verified against an external authority, 30 by
deterministic code reading, 1 inferred. F67 and F68 were found during
remediation (§12), which also moved eight findings from read to executed.
F1–F50 are above; **F51–F57 in §9** (test suite), **F58–F63 in §10**
(plans/specs), **F64–F66 in §11** (composition/second-order — including the
document's most severe finding), **F67–F68 in §12** (found while remediating).
§12 is the remediation log: nine findings are now fixed and verified.
See §8 for the re-verification pass, §9–§11 for the three full passes.

---

## 4. Structural analysis

Four patterns explain nearly every finding above.

**1. Tests are inverted relative to risk.** Coverage concentrates on pure
functions — `classifyInvocation`, `parseStartupFlags`, `MergePolicies`,
`findShasumEntry` — which are easy to test and where nothing critical was found.
The launch paths that assemble OS primitives have zero coverage. F22 is the
archetype: the helper was tested with the *fixed* argument shape while the
vulnerable caller went untested, so the suite passed and the fix looked complete.
The line-by-line pass (§9) sharpens this: the problem is less *how much* is
tested than *what the assertions say*. The seatbelt tests check that permitted
writable roots are present and never that forbidden ones are absent, so they pass
unchanged against the vulnerable caller (F52). Positive-only assertions on a
containment boundary cannot fail in the direction that matters.

**2. CI validates everything except the product's purpose.** Three-OS matrix,
race detector, govulncheck, gosec — all real. But all three sandbox smokes skip,
and the one network assertion is one-sided. A completely broken sandbox
(F50, today's actual state) is green in CI.

**3. Project state is not recoverable from the documents — in either direction.**
All four specs read "Approved (brainstorm), pending implementation plan(s)", and
the 2026-07-20 spec diagnosed the MCP incident precisely — latency, stdio,
reaping — then sat unimplemented for four weeks until the incident recurred on
this machine (F48, re-verified in §10.3: one of eight items delivered). But the
full plans read (§10) found the inverse failure alongside it: **all 134 plan
checkboxes are unchecked while the work is largely done.** So the docs overstate
what is pending *and* understate what shipped. Only the code is authoritative
here — which is precisely why every finding in this document is anchored to a
file and line rather than to a status header.

**4. Documentation asserts guarantees the code does not provide.** Not through
carelessness — `enforcement-matrix.md` is unusually candid about macOS read
exposure and CI skips. But `SECURITY.md`, the README matrix, and the code
diverged as the implementation changed underneath them.

**What is genuinely strong**, and worth protecting: zero third-party
dependencies; archive extraction whose zip-slip, tar-slip, symlink-escape and
zip-bomb guards all read as correct (though only the traversal helper is under
test — see F5); release engineering (pinned Go, `-race`, `-trimpath`, provenance,
per-asset + combined checksums, draft-by-default); a policy-trust boundary that
holds under adversarial reading; and a fail-closed instinct that is correct
almost everywhere it is actually implemented.

---

## 5. Recommended order

0. **F64** — before anything else, validate the bin cache. `lookupBinCache`
   must reject any cached path outside the expected roots. One line, worth doing
   independently of F22, and today it is the difference between "sandbox defeat"
   and "unsandboxed code execution as the user." Then treat `~/.nvx` as a trusted
   control plane: assert in a test that no containment profile on any platform
   makes it writable (F65).
1. **F50** — Linux sandbox dead. Split file vs directory roots, or drop
   `READ_DIR` from device-file rules.
2. **F22** — macOS default path. Make `sandbox_native_darwin.go:22` pass the
   same two roots as `sandbox_seatbelt.go:58`. Then close the two structural
   holes that let it survive last time: drop the variadic tail so a stray
   writable root cannot be added silently (**F51**), and add a *negative*
   assertion that `nvxHome` is absent from the `file-write*` set (**F52** —
   today's tests pass against the vulnerable shape).
3. **F46** — piped stdio. Apply Fix B: `bInheritHandles=TRUE` +
   `STARTF_USESTDHANDLES`. **Both, or neither** — setting the flag without the
   inherit bit makes the child fail to start outright (proven: exit code 1),
   which is worse than today's misrouted output. Unblocks every MCP/daemon
   workload.
4. **F25** — make the re-entrancy marker unforgeable (verify the process is
   genuinely inside a sandbox rather than trusting a plain env var).
5. **F3** — lock the `p.session` read, or use `sync.Map`.
6. **F24** — correct the arm64 syscall constants; **F23** — fix the BPF filter
   (do these together, since F23 is unreachable until F50 is fixed).
7. **F13 / F12** — tag a release; make at least one platform's smoke test
   actually run and assert the *allow* path, not only denial. Cheapest starting
   point: `TestUnelevatedSandboxRunsPackageManager` already drives a real
   AppContainer launch end to end but is gated behind `NVX_PROBE=1` (**§9.2**) —
   turning it on is far less work than writing a launch test from scratch.

Then reconcile the docs (F33, F2) so `SECURITY.md` describes what the code does.

**Defer** the cage split and polyglot runtimes until the enforcement layer is
verified. Both add surface to a foundation that does not currently hold.

---

## 6. Coverage gaps

- **Not executed on real macOS.** F22 is proven at the profile-generation level;
  the runtime behavior of `sandbox-exec` with that profile is not.
- **F26 unvalidated** — requires a working Landlock ruleset, which F50 prevents.
- **Windows AppContainer end-to-end** was exercised only on this machine, whose
  `~/.nvx/versions` was removed mid-assessment; F47's trigger was corrected once
  that was discovered.
- ~~Tests and plans surveyed structurally, not line by line.~~ **Both gaps are
  now closed:** the 3,504 lines of tests were read in full (§9) and the 4,226
  lines of plans and specs were read in full (§10). Every source file, test file,
  script, workflow, installer, and design document in the repository has now been
  read end to end.
- **No fuzzing, load, or penetration testing.** The SHASUMS and archive-header
  parsers are attacker-adjacent and would suit fuzzing.
- **staticcheck / golangci-lint** not installed; not run.
- **Two findings were corrected during validation** (F46 narrowed from "never
  receives stdin" to "piped stdio"; F47's trigger rewritten). Both had been
  asserted from reading before being executed — the same error pattern as the
  original F22 fix.

---

## 7. Product assessment

### 7.1 The threat model decides the strategy

A product review panel (product strategist, security practitioner, target-user
persona) converged on "ship the supply-chain checks, defer the sandbox." **That
conclusion is rejected.** It rested on a synthetic persona's claim about
*demand* ("the warnings are what I actually wanted"), which is an opinion with
no evidence behind it, not a finding. The maintainer's own threat model — a
malicious package escaping to the home directory and stealing credentials — is
the correct one, and it is what the architecture should be judged against.

The modal npm supply-chain attack is credential exfiltration from `$HOME`:
`ua-parser-js` (2021) shipped a credential stealer, `event-stream` (2018)
targeted wallet keys, and the pattern since has been postinstall scripts
harvesting `~/.npmrc` tokens, `~/.aws/credentials`, SSH keys and environment
variables. This is not hypothetical for this project: during the audit, the
maintainer's `~/.npmrc` was observed holding live npm and GitHub Packages auth
tokens. Environment scrubbing plus `HOME` redirection is exactly what stands
between a malicious postinstall and those tokens.

The checks cannot substitute for containment, structurally:

| Control | Catches | Misses |
|---|---|---|
| Typosquat (Levenshtein + download counts) | Lazy name-confusion delivery | Compromised *legitimate* packages — `ua-parser-js` and `event-stream` involved no typo at all. Also dependency confusion and malicious transitive deps |
| OSV lookup | Known, disclosed CVEs | Anything not yet disclosed — precisely the window a fresh attack lives in |
| Release-age warning | Very recent publishes | Advisory only; a patient attacker waits out the window |

All three are **detection heuristics that fail against novel or targeted
attacks**. The sandbox is **containment that holds whether or not detection
fired** — a strictly stronger property, and the reason it is the hard thing to
build. Building it was the right call.

Note the irony: F22 — the macOS Critical — *is* "malicious package writes into
your home directory and rewrites the security policy." The stated fear is the
exact thing the audit found broken.

### 7.2 What survives from the product panel

Narrower than the panel framed it, but real:

- **Do not claim containment until it is verified.** A sequencing point, not an
  argument against building it. Remove the unearned claims from `README.md`,
  `SECURITY.md` and `enforcement-matrix.md` rather than softening them.
- **Default `isolation.enabled` to false while it is broken.** A silently
  permissive sandbox that is on by default manufactures false confidence — worse
  than none, because it changes user behaviour: people run packages they would
  otherwise refuse.
- **Self-disclose now.** Zero users is an asset: no CVE, no coordination, nobody
  harmed. Found by others, seven execution-verified defects plus a threat model
  contradicted by its own code reads as marketing. Self-published, it reads as
  rigor.
- **`--agent-mode` must not auto-approve a known CVE.** Auto-approving install
  scripts is defensible; pre-answering "proceed despite active vulnerabilities?"
  in the one mode built for unsupervised agents inverts the product's purpose.
- **Ship the checks as honest defense-in-depth**, framed as heuristics. Worth
  having. Not the product.

### 7.3 Consequence for planned work

`docs/superpowers/specs/2026-07-23-cage-split-design.md` should be **shelved,
not implemented**. It splits the *sandbox* out as a standalone product on the
premise that it is the strong, differentiated half. That premise is sound on the
threat model but unsound on current state: the half being spun out is the half
that does not work. Reconsider only once containment is verified on at least one
platform.

Polyglot runtimes (`feature/polyglot-runtimes`) should likewise wait. Both add
surface to a foundation that does not currently hold.

### 7.4 Positioning, once the claims are earned

nvx's differentiation is real and is not the version manager: fnm and volta warn
about nothing, and Socket/Snyk warn in CI — hours after the laptop already ran
the postinstall. Local, pre-execution containment plus pre-install checks is a
genuinely distinct position. It is simply not deliverable today.

One unresolved strategic question, flagged rather than answered: the platform
closest to working (Linux — fails closed, and where unsupervised agents run in
CI/containers/devcontainers) is not the platform the maintainer and the intended
user are on (Windows, where the incident occurred). Fixing one platform is the
right scope; which one is a judgement call that depends on whether the target is
agent infrastructure or the developer's own machine.

---

## 8. Re-verification pass

A second pass re-checked every finding against the implementation at the same
commit (`2891434`, working tree clean). **47 of 50 were re-confirmed at the
cited location.** Four errors in this document were found and corrected; three
findings could not be re-checked. Nothing was withdrawn.

### 8.1 Corrections made to this document

| Was | Now | Why |
|---|---|---|
| "61 commits behind `main`" (§1, F13) | **86** (`v0.2.0-beta..HEAD`); 83 since the CHANGELOG entry | 61 did not reproduce under any measure tried — the substance of F13 was right, the number was not |
| §4: "archive extraction with **tested** zip-slip/tar-slip/symlink/bomb guards" | guards read correct, but only the traversal helper is tested | `copyArchiveFile` (bomb cap) and the tar symlink check are both **0.0%** covered. F5's own parenthetical was accurate; the §4 summary overstated it |
| F11: `main()` 177 lines, `launchAppContainerProcessOnce` 171 | 173 and 169 | miscount |
| F31 cited `_ = proxyPort` in `sandbox_landlock_linux.go` | it is at `sandbox_network_seccomp_linux.go:46` | wrong file; the finding's substance holds |

### 8.2 Findings strengthened

- **F46 upgraded `read` → `executed`.** A probe replicating nvx's exact
  `CreateProcessW` shape with stdout assigned to a pipe: **0 bytes arrive**,
  the child exits 0, and its output surfaces on the *parent's* console — which
  is precisely why terminal use always looked healthy and only daemons broke.
  The same probe confirms Fix B works (12 bytes, marker received) and surfaces
  a new constraint: `STARTF_USESTDHANDLES` **without** `bInheritHandles=TRUE`
  makes the child exit 1. A half-applied fix is a hard failure, not a partial one.
- **F3 re-reproduced.** `WARNING: DATA RACE` at `egress_proxy.go:153` under
  `-race`, driving `allowed()` from 8 goroutines against the `promptMu`-guarded
  write. Probe deleted after running; tree left clean.
- **F23 confirmed by hand-tracing the jump offsets**, independent of the
  original cBPF interpreter run. Instruction `[3]`'s false branch lands on `[5]`
  with `args[0]` still in the accumulator, so: IPv4/TCP → DENY, IPv4/UDP →
  **ALLOW**, IPv6 (any) → DENY, AF_UNIX → DENY. Two independent methods agree.
- **F25 refined, and it is harder to fix than stated.** `inSandboxSession()` is
  an in-process atomic, so `NVX_SANDBOX` is the *only* cross-process
  re-entrancy signal. Deleting the check would make every nested shim start a
  fresh sandbox; it needs an unforgeable replacement.
- **F22's severity reinforced.** `sandbox_seatbelt.go:52-58` carries a comment
  describing this exact defeat — "let any sandboxed process rewrite the global
  policy, self-approve grants, or trojan the node/npm binaries themselves" — in
  the file that *was* fixed, while the default darwin caller still passes
  `config.NvxHome` and `filepath.Dir(cmdPath)` as writable roots.

### 8.3 Not re-verifiable

- **F1** (orphan accumulation) was proven by direct observation of process
  counts on this machine. nvx has since been removed (`~/.nvx/bin` renamed to
  `bin.disabled-20260727-001828`, `~/.nvx/versions` deleted), so it cannot be
  re-observed without reinstalling. The original observation stands; it is not
  re-confirmable in place.
- **F47**, **F26** — unchanged from §6: mechanism-only and blocked-by-F50
  respectively.

### 8.4 One apparent contradiction, resolved

§2 reports gosec at **0 issues (CI args)** while F14 reports **53**. Both are
correct and neither needs changing: CI passes
`-severity=high -confidence=high -exclude=G204,G304,G301,G306,G702,G703,G704`,
while `verify-security.ps1` passes only `-exclude=G204,G304,G301,G306` — no
severity or confidence floor. Noted so a future reader does not "fix" a
contradiction that is not one.

### 8.5 Tooling re-run

`go build ./...` exit 0 · `go vet ./...` exit 0 · `go test ./...` pass at
**34.3%** coverage (identical to §2) · local toolchain still **go1.19** against
a `go.mod` declaring 1.23 (F9).

---

## 9. Line-by-line test-suite review

§6 listed the test suite as "surveyed structurally, not line by line." **That gap
is now closed: all 3,504 lines across 17 test files were read.** The suite is
better than its 34.3% coverage number suggests — the policy-trust boundary,
shell-injection escaping, archive path traversal, classification, and the
`hintIfShadowed` marker all have genuine, well-reasoned tests, several with
comments explaining the failure mode they pin. Seven new findings follow, plus
one correction to F43.

*Totals after this pass (superseded by §10's): 57 findings — 3 Critical,
16 High, 25 Medium, 13 Low.*

### 9.1 New findings

| # | Sev | Finding | Validation |
|---|---|---|---|
| **F52** | Medium | **The seatbelt tests cannot detect F22.** A probe replayed every assertion from both existing tests (`TestBuildSeatbeltProfile`, `TestBuildSeatbeltProfileContainsWritesAndEgress`) against the *darwin* caller's 5-argument shape. **All assertions passed**, with `(subpath "/Users/x/.nvx")` sitting in the emitted `file-write*` allowlist. Both tests assert only that *permitted* roots are present; neither asserts that `nvxHome` is **absent**. The same tests do write negative assertions for `(allow default)` and `(allow network*)` — so the pattern was known and simply not applied to the write scope, which is the actual containment boundary. This is why F22 survived a fix and a passing suite. | **executed** |
| **F51** | Medium | **`buildSeatbeltProfile` is variadic** (`writableRoots ...string`, `sandbox_seatbelt.go:104`). Two callers pass different numbers of writable roots and neither the compiler nor a signature review can object. The fix for F22 should replace the variadic tail with an explicit type, so adding a writable root becomes a deliberate, reviewable act rather than an extra argument. | read |
| **F53** | Medium | **`--agent-mode` is partly fiction.** `main.go:356-357` documents it as "Auto-approve all prompts and suppress success/info messages (equivalent to `-y -q`)". The parser (`main.go:50-52`) sets `agentModeFlag` and `yes` but **never `quietFlag`** — so the documented suppression does not happen. Separately, **`agentModeFlag` has no reader anywhere in production code**: it is write-only state, so there is no hook at which agent-specific behaviour could be implemented. Any fix for F34 must add a read site, not just change a condition. | **executed** |
| **F55** | Low | **An existing passing test demonstrates F18.** `TestLoadPolicyNearestWins` (`nvx_test.go:683-726`) has a parent policy at `max_distance: 3` and an untrusted child at `max_distance: 1`, and asserts the child wins. Since `CheckTyposquattingAuthority` flags on `dist >= 1 && dist <= maxDist`, a *lower* max_distance detects *fewer* typosquats — so this is an untrusted project file loosening detection with no trust prompt, and the test frames it as correct behaviour. | **executed** |
| **F56** | Low | **`TestCleanupStaleSandboxes` enshrines F35.** It creates one guest home, runs `cleanupStaleSandboxes`, and asserts it was deleted. It never asserts that a *live* sandbox's home survives. Like F42, fixing F35 requires changing this test. | read |
| **F54** | Low | **Test state leaks across the package.** `TestParseStartupFlagsQuietAndAgentMode` (`nvx_test.go:927`) zeroes `quietFlag`/`agentModeFlag`, then calls `parseStartupFlags`, which mutates both globals directly — with no restore. Every test that runs afterwards in the package sees `quietFlag == true`. Nothing security-relevant reads it today (see F53), so the current impact is suppressed log output in later tests, but it makes outcomes order-dependent. | read |
| **F57** | Low | **`policyLoosens` checks trusted packages by length** (`policy.go:526`: `len(after...) > len(before...)`), which is sound *only* because `MergePolicies` unions that list, so any addition necessarily grows it. Every neighbouring check uses proper set comparison (`hostsAdded`). If merge semantics ever changed to replace, the trust gate would silently stop firing. Nothing pins the coupling — no comment, no test. | read |

### 9.2 Correction to F43

F43 said "no test calls any launch/dispatch entry point." The four functions
named in §1 (`platformLaunchNative`, `runNativeSandbox`, `runSandbox`, `runShim`)
are confirmed at **zero** test references, so §1 stands. But one test does drive
a real launch: `TestUnelevatedSandboxRunsPackageManager`
(`sandbox_unelevated_windows_test.go`) calls `launchAppContainerProcess`
end-to-end, running `npm -v` and `npm run` inside a throwaway AppContainer with
no grants. **It is skipped unless `NVX_PROBE=1`**, so it never runs in CI or a
normal `go test`. The right framing is not "no launch test exists" but "a good
one exists and is switched off" — which is a materially cheaper problem to fix
and should be the first candidate for the CI work in §5 item 7.

### 9.3 What the suite does well

Worth protecting, since §4's criticism of test placement should not read as a
verdict on test quality:

- **Adversarial inputs, not just happy paths.** `TestShellEnvAssignmentEscapes*`
  uses `$(touch pwned)` and `'; Write-Error pwned; #`;
  `TestProjectBinShimQuotesCommandNames` uses `bad %PATH% & name`.
- **Fail-closed paths driven via subprocess re-exec.**
  `TestRunVerifyInstallFailsClosedOn{Metadata,OSV}Failure` genuinely prove denial
  rather than asserting a return value.
- **Regression tests that state their own reason.** The comments on
  `TestHintIfShadowedPersistsAcrossProcessesAndRearms`,
  `TestReapingJobKillsProcessOnClose`, and the `classify_test.go` cases for
  value-taking flags each name the specific bug they pin. That is the pattern the
  seatbelt tests need (F52).
- **A deliberately documented omission.** `grants_trusted_tools_test.go` ends
  with a comment explaining which branch is untested and why the machinery to
  reach it was judged not worth it. Honest, and correct.

---

## 10. Line-by-line plans & specs review

§6 listed 4,226 lines of plans and specs as surveyed structurally. **That gap is
now closed: all eight documents were read end to end** (4 specs, 774 lines;
4 plans, 3,452 lines), and every implementation claim in them was checked against
the code. Six new findings, plus a correction to §4's characterisation of this
project's failure mode.

*Totals after this pass (superseded by §11's): 63 findings — 3 Critical,
16 High, 29 Medium, 15 Low.*

### 10.1 The correction: the code is *ahead* of the documents, not behind

§4 pattern 3 said "design velocity exceeds implementation velocity — the
documents are ahead of the code." For the four **plans**, the opposite is true,
and it matters more.

**All 134 plan checkboxes are unchecked** (50 + 35 + 28 + 21), with zero `- [x]`
anywhere — yet the work is largely done. Checked against the code,
`live-promotion-plan1` meets **all five** of its acceptance criteria:
`~/.nvx/tool_home/<hash>` persistence exists, `resolveUseRealHome` /
`realHomeSwapSupported` / the `guestHome = realHome` branch are **gone**,
`ensureTrustedToolGrant` no longer refuses on Windows, keying is per
(project, tool), and `cleanupStaleSandboxes` touches only `sandbox_home`.
`shim-interception-part1` and `containment-v2-parts-2-4` likewise substantially
met theirs — `nvx doctor`, `diagnosePath`, `rebuildUserPath`, `classifyInvocation`,
`shouldContain`, `isolation.level`, the `--strict`/`--standard` anti-smuggling
flags, and installers that no longer put `~/.nvx/current` on PATH all exist.

So the tracking failure runs in **both** directions: specs sit at "Approved,
pending implementation" while plans that were executed are recorded as untouched.
The repo cannot tell you what remains. That is how F48's "never implemented"
framing came to overstate the case for some items — a document-derived reading of
project state is unreliable here in either direction, and only the code is
authoritative. **F48 stands for the daemon spec** (re-verified below), but its
reasoning should not be generalised to the other three.

Credit where it is due: the plans' self-review sections are genuinely rigorous.
The `containment-v2-parts-2-4` review caught and fixed a task-ordering bug that
would have produced a non-compiling intermediate commit ("the original draft had
this backwards"), and each review honestly separates descoped work from delivered
work.

### 10.2 New findings

| # | Sev | Finding | Validation |
|---|---|---|---|
| **F60** | Medium | **Plan tracking gives a false negative.** 134 unchecked checkboxes, 0 checked, across four plans whose acceptance criteria are largely met (§10.1). Project state is unknowable from the repo's own records; `shim-interception-part1`'s self-review even claims its acceptance test was "verified manually" while every box beneath it is unticked. | **executed** (grep + criteria check) |
| **F61** | Medium | **`part5-grants` is a stale acceptance gate with no supersession notice.** Its acceptance still asserts behaviour that was deliberately *removed*: "the tool's credentials persist to the real home directory", a prompt reading "wrangler wants access to your real home directory…", and "Windows prints a clear one-line explanation and points at `--no-sandbox`". All three were reversed by `live-promotion-plan1`. Its self-review cites `realHomeSwapSupported`, a symbol that no longer exists. Nothing in the file records that it was superseded, so anyone verifying against it reports false failures against correct code. | read |
| **F58** | Medium | **`CLONE_NEWPID` shipped without its required reaper, on the wrong process.** The daemon spec's Linux section required `CLONE_NEWPID` *plus* a `wait4` loop, warning that "PID-1-of-a-namespace semantics mean orphans reparent there and are never cleaned up otherwise." The flag is present (`sandbox_linux.go:28`); **no `wait4`/`Wait4` exists anywhere**. Worse, it is set on the *target* command's `SysProcAttr`, so the target becomes PID 1 — not nvx's supervisor, as the spec's design assumed. The spec's "reaping is free" claim (kill the supervisor, the namespace collapses) therefore does not hold: killing nvx leaves the target running as PID 1 of a live namespace. With `Setpgid: true` ("Don't propagate signals automatically") alongside it, Linux has the same orphan-survival property as F1 on Windows. Latent behind F50. | read |
| **F59** | Medium | **Daemons still cannot persist auth — the exact failure Part 4 was written to prevent.** The daemon spec required Tier-2 persistence to apply "automatically, without a prompt, to any contained process," *because* "a daemon has no TTY to answer a grant prompt." Shipped behaviour gates it on an **approved** trusted tool: `env.go:646` sets `ToolName` only when `trustedToolCandidate` returns `wantsPersistence`, which requires an auth-shaped subcommand (`login`/`auth`/`configure`). `npx <mcp-server>` has none → `usePersistentProfile` false → fresh ephemeral home every restart → re-auth every restart. | read |
| **F63** | Low | **The macOS reaping mitigation was never implemented.** The daemon spec required "process-group kill via a signal handler on graceful nvx exit (covers normal exit, `SIGTERM`, `SIGINT`)", with hard `SIGKILL` documented as an accepted gap. **No `signal.Notify` exists anywhere in the codebase.** So the accepted gap is now the *only* behaviour: macOS has neither graceful-exit cleanup nor hard-kill cleanup, and the mitigation meant to bound the gap is absent. | **executed** (grep) |
| **F62** | Low | **The cage spec mis-describes a file it proposes to move.** It states `grants_trusted_tools.go` "today hardcodes 'npm/yarn/pnpm get a persistent guest home'". That file contains **no reference to npm, yarn, or pnpm**; what it hardcodes is `authLikeSubcommands = {login, auth, configure}`, and its executor check delegates to `executorCommands` in `classify.go`. Separately unflagged: `classify.go` is slated for outright *deletion* while the *moved* `grants_trusted_tools.go` depends on `executorCommands` defined there — resolvable via the spec's own generalisation, but the dependency is not called out in the sequencing. | **executed** (grep) |

### 10.3 F48 re-verified and sharpened

The daemon spec (2026-07-20) remains the one document whose remediation genuinely
did not land. Item by item, against the code:

| Spec item | State |
|---|---|
| Part 1 — project-dir-gated containment (`package.json` or `.git`) | ❌ no `.git` check exists anywhere |
| Fix A — remove the per-launch ancestor re-grant | ❌ **and reversed** — `grantWorkdirAncestors` is called at **three** sites (`sandbox_appcontainer_windows.go:58, 59, 233`), one added by the recent remediation |
| Fix B — `bInheritHandles=TRUE` + `STARTF_USESTDHANDLES` | ❌ (F46) |
| Fix C — Job Object `KILL_ON_JOB_CLOSE` | ✅ shipped 2026-08-15 |
| Linux — `CLONE_NEWPID` | ⚠️ present but misplaced and unreaped (F58) |
| Linux — `wait4` reaping loop | ❌ |
| macOS — signal handler | ❌ (F63) |
| Part 4 — automatic Tier 2 for contained processes | ❌ (F59) |

**One of eight actionable items delivered, one delivered incorrectly.** Fix A is
the sharpest point: the spec identified the ~70s stall by *measurement* — "4 of 5
ancestor grants hang to their full timeout," two of them redundant because the ACE
was already present — and that measured cause is not merely still in the code, it
now runs at three call sites. This is the mechanism behind F1's orphan pile-up.

### 10.4 Where the specs and the code agree

Not everything diverged, and the pattern of what held up is informative — the
migrations that landed cleanly are the ones with a *removal* step someone had to
execute deliberately:

- **The real-home swap was removed exactly as designed.** The
  live-promotion spec called `resolveUseRealHome` and the `guestHome = realHome`
  branch dangerous enough to delete; all of it is gone, and the replacement
  keeps the sandbox out of the real home on every platform.
- **The consent prompt was rewritten to match.** It now reads "…keep a
  persistent profile for this project… (Still sandboxed; your real home is
  untouched.)" — no residual over-claim about real-home access. A prompt that
  asks for consent to something that no longer happens would train users to
  approve alarming text; it was correctly updated.
- **Narrow prompting held.** `trustedToolCandidate` gates on auth-shaped
  subcommands, so `npx cowsay hi` never prompts — the spec's stated intent
  ("prompting rarely, not on every never-before-seen npx invocation") is honoured.
- **The loosen/tighten asymmetry works.** A project policy raising
  `isolation.level` to `strict` applies silently; lowering it prompts — specified,
  implemented, and tested both ways.

One more contradiction for F33's list: the containment-v2 spec's Part 4 states
"the project directory is **writable**… everything else is read-only." The darwin
caller makes `~/.nvx` writable (F22), so the spec is a fourth document contradicted
by that single defect, alongside `README.md`, `SECURITY.md`, and
`enforcement-matrix.md`.

---

## 11. Second-order review: how the parts compose

Sections 3, 8, 9 and 10 are **first-order by construction** — each finding names a
file and line and is confirmed there. That method cannot see a defect that exists
only in the interaction between two correct-looking components, and this pass was
run specifically to look for those. It found three, one of which **escalates F22
from a sandbox defeat to a full escape**, and one of which **falsifies a claim of
strength made in §1**.

*Totals after this pass (superseded by §12's): 66 findings — 4 Critical,
17 High, 30 Medium, 15 Low.*

Two hypotheses were tested and **rejected**, recorded so they are not re-chased:
`cleanupStaleSandboxes` is called only from the `cleanup` command (`main.go:195`),
not per-run, so F35 cannot delete a concurrent live sandbox's home; and
`policyLoosens`'s length-based trusted-package check cannot be evaded by
substitution, because `MergePolicies` unions that list (F57 records the fragility,
not a live bug).

### 11.1 F64 (Critical) — F22 composes into unsandboxed code execution

Each link is individually unremarkable. Together they are an escape.

1. On macOS — **the default provider** — `sandbox_native_darwin.go:22` puts
   `config.NvxHome` in the Seatbelt `file-write*` allowlist (F22, proven by
   execution in §8: `(subpath "/Users/x/.nvx")`).
2. `~/.nvx/cache/bin-resolve.json` maps bare command names to **absolute paths**
   (`bin_cache.go:21-23`).
3. `lookupBinCache` (`bin_cache.go:32-52`) validates a hit by exactly two things:
   the stored `PathHash` equals `sha256(os.Getenv("PATH"))`, and `os.Stat(p)`
   succeeds and is not a directory. **There is no check that `p` lies under
   `~/.nvx`, under a runtime version dir, or under any trusted root at all.**
4. The contained process can compute the parent's future hash. `prependPath`
   builds the sandbox `PATH` by prepending the runtime dir to the *inherited*
   `PATH`, so stripping the first entry from its own `PATH` yields the parent's —
   no guessing required.
5. `lookPathSkippingNvxShims` (`env.go:220-222`) returns the cached path
   unchecked, and it is **nvx the unsandboxed parent** that then executes it.

So a malicious postinstall, correctly contained on macOS, writes one JSON file and
obtains arbitrary code execution **as the user, outside any sandbox**, on the next
`node`/`npm` invocation. It also survives indefinitely: the entry is only
invalidated by a `PATH` change or the target disappearing, and the attacker
controls the target.

**Scope is macOS-only, and that is verified, not assumed.** On Linux `nvxHome` is
added to `readOnlyRoots` with read+exec (`sandbox_landlock_linux.go:150-156`), and
on Windows the write grants cover the guest home and workdir while `nvxHome` paths
are granted RX — so neither platform yields the step-1 write primitive.

Why neither existing finding covers it: F22 was scored for *policy* tampering
("rewrite `policy.json`, self-approve grants, trojan the node binary") and F8
concerns a concurrent-write race in the same file. Nobody asked what the cache is
*trusted for*. The fix is one line of validation in `lookupBinCache` — reject any
cached path outside the expected roots — and it is worth doing **independently of
F22**, because a cache that maps names to unvalidated absolute paths is a
confused-deputy primitive waiting for its next write vector.

### 11.2 F65 (High) — the trust boundary is only as strong as the pin store

§1 credits the policy-trust logic as "correct under adversarial reading:
content-hash pinning with independent re-verification, no TOCTOU." **In isolation
that is accurate. In composition on macOS it is void**, and the praise is hereby
withdrawn for that platform.

`LoadPolicy` resolves whether a loosening project policy applies by comparing its
content hash against `grants.PolicyPins` (`policy.go:400-406`). That store is
`~/.nvx/grants/<key>.json` — writable to the contained process under F22. So the
contained process can:

- **Forge its own pin** — drop a `.nvx-policy.json` that sets
  `network.mode: open`, `typosquatting.enabled: false`, or a different filesystem
  provider, then write that file's SHA256 into the grants file. On the next run it
  is "trusted" and applies silently.
- **Skip the pin entirely.** `grants.AllowHosts` is appended to the effective
  allowlist at `policy.go:413-415` with **no pin check of any kind**, so arbitrary
  egress hosts can be added directly.
- **Bypass the whole mechanism.** `~/.nvx/policy.json` is the *global baseline*
  that pinning protects project files against, and it is guarded by no pin at all.
  One write sets `isolation.enabled: false` and there is no sandbox next time.
- **Silence the tripwires** — `shadow-hint-shown` and `drive-root-notices.json`
  are ordinary files in the same tree; pre-creating them suppresses the
  PATH-bypass and missing-grant advisories permanently.

Content-hash pinning is a sound design that carries an unstated precondition: the
pin store must be outside the adversary's reach. That precondition is undocumented
and currently false on the default macOS path. The lesson generalises past F22 —
`~/.nvx` is nvx's **trusted control plane**, and no containment profile on any
platform should ever make it writable, which is worth asserting as a test rather
than leaving to each caller.

### 11.3 F66 (Medium) — the shadow-PATH warning fires when interception is working

An ordering interaction with npm's own behaviour, and the mechanism behind the
repeated warnings observed live during `npm publish`.

- `hintIfShadowed(nvxHome)` runs at `env.go:607` — **before** the policy load and
  the containment decision, so on every shim invocation.
- `project_bin.go:36` installs nvx shims **into `<project>/node_modules/.bin`**.
- npm prepends both `node_modules/.bin` and the active node directory to `PATH`
  for lifecycle scripts. *(This half is documented npm behaviour, asserted from
  knowledge — not executed here. The nvx-side preconditions below are verified.)*
- The active node directory is `~/.nvx/versions/node/<v>/...`, which is
  `dirWithin` `~/.nvx/versions` — one of `nvxRuntimeDirs` (`doctor.go:88-93`).

So during any npm lifecycle script, an nvx shim in `node_modules/.bin` is invoked —
**proving interception worked** — and that same process sees a runtime dir ahead of
`~/.nvx/bin` and warns "some commands may bypass nvx. Run: `nvx doctor`". The
warning is structurally guaranteed at the exact moment it is false.

The marker-file fix addressed the *repetition* (correctly — that was a real defect)
but not the false positive, and the remaining consequence is the sharper one: it
steers users toward `nvx doctor`, which **rewrites the persistent User PATH in the
registry** (`repairPersistentPath`). A spurious warning driving an unnecessary
persistent-PATH rewrite is a worse outcome than the noise it replaced. The fix is
to skip the hint when the shadowing entry is the runtime dir of the *current*
resolved version — that is nvx's own doing, not a broken user PATH.

### 11.4 What this pass says about the method

The three findings above share a shape: **a component is correct against its own
contract, and the defect is in what another component assumes about it.** The bin
cache is a correct memoizer and a broken trust boundary. Hash pinning is correct
cryptography resting on an unstated custody assumption. `hintIfShadowed` is a
correct predicate over a `PATH` that nvx and npm both mutate.

That is not reachable by confirming cited lines, which is what §3/§8/§9/§10 did.
It also suggests where the remaining risk concentrates: **every file under
`~/.nvx` is an input that some unsandboxed code path trusts** — `policy.json`,
`grants/*.json`, `cache/bin-resolve.json`, `bun-releases.json`,
`popular_packages.json`, the marker files, and `current`/`versions` themselves.
None of them is integrity-checked on read. Today only macOS hands an adversary the
write primitive; the audit did not enumerate what else could, and that enumeration
is the natural next pass.

---


---

## 12. Remediation log — Linux

Fixes were applied Linux-first (maintainer's call), because Linux is the platform
where containment can be *proven* in CI rather than argued. Everything below was
verified inside `golang:1.26` containers, `--privileged`, kernel 6.18.33, and
**every fix has a revert-check confirming its test fails against the old
behaviour** — the discipline the original F22 fix lacked.

**Linux containment now works end to end.** Before this, it could not start a
single command.

### 12.1 Fixed and verified

| # | Fix | How it was proven |
|---|---|---|
| **F50** | `landlockReadOnlyRules` derives each root's access mask from its inode type, dropping `READ_DIR` for non-directories. | Real-kernel ruleset test, plus **an end-to-end test that applies the sandbox including `landlock_restrict_self` and then execs a command** — the launch-path assertion the project never had (F43). Reverted: `SANDBOX_SETUP_FAILED: landlock read rule for "/dev/null": invalid argument`. |
| **F23** | `buildProxyNetworkFilter` rewritten; order is now syscall → type (masked) → domain. | Real-kernel probes install the filter in a subprocess and attempt each socket. Before: `inet_tcp=denied, inet_udp=allowed, unix_stream=denied`. After: TCP and AF_UNIX allowed, IPv4+IPv6 UDP denied. `buildOfflineNetworkFilter` passes throughout as a control. |
| **F23-b** *(found while fixing)* | `socket(2)` carries `SOCK_CLOEXEC`/`SOCK_NONBLOCK` OR'd into its type argument and Go's own `net` package sets them, so comparing the raw type missed every flagged UDP socket. Added a `SOCK_TYPE_MASK` AND. | Neutralising only the mask flips `inet_udp_cloexec` to `allowed` while every other probe stays correct. |
| **F24** | The three per-arch landlock syscall files are **deleted** for one shared definition. Landlock landed on every arch table simultaneously with identical numbers, so the split bought nothing and cost correctness; fixing the numbers alone would have left the divergence possible. | RED→GREEN **on real aarch64** — constant test cross-compiled and run under QEMU: 445/446/447 before, 444/445/446 after. |
| **F26** | The sandbox no longer gets read+exec over all of `nvxHome`. It now gets `versions/` (the actual requirement), `bin/` (PATH inside still resolves nested node/npm through the shims) and `current/`. | Upgraded from **inferred to observed**: a contained process could read `tool_home/*` credentials, `grants/*.json`, `policy.json`, `cache/bin-resolve.json` and other sessions' guest homes. Test uses the **real production layout** with the guest home under the now-ungranted `sandbox_home`, and asserts the guest home stays writable and `policy.json` stays unwritable. |
| **F31** | The egress proxy stays in the parent, outside the namespace, and is exposed on a UNIX socket; `startProxyRelay` bridges the last hop from loopback inside the namespace. UNIX sockets are filesystem objects and cross a netns cleanly; npm/node only accept `host:port` in `HTTP_PROXY`, hence the relay. | End-to-end, all three properties at once: direct egress from inside the namespace **blocked**, allowlisted host **200 through the relay**, non-allowlisted **403**. Old design measured at **502 Bad Gateway for an explicitly allowlisted host**. |
| **F58** | `CLONE_NEWPID` moved from the target to the **supervisor**, so nvx is PID 1 and its death tears down the tree. `reapUntilChildExits` replaces `cmd.Wait()` and reaps orphans. | Behavioural: a descendant heartbeats to a file, the supervisor is killed, the heartbeat must stop. With the flag removed the heartbeat keeps advancing, so the test checks the property rather than the plumbing. |
| **F64** | `lookupBinCache` validates that a cached path's directory is one the uncached resolver would search, which also excludes the shim dir. | Tests cover a forged entry off PATH, a forged entry planted in the shim dir, and a normal round trip so the check cannot pass by rejecting everything. Cross-platform: green on Windows and Linux. |
| **F67** *(found while fixing)* | `TestIsPackageManagerCommand` asserted `C:\x\npm.cmd` unconditionally; off Windows `filepath.Base` returns the whole string. Now platform-split. | `go test ./...` and `go test -race ./...` now pass on linux/amd64. See the §1 correction: this had broken CI on two of three platforms for **51 commits**. |
| **F68** *(found while fixing)* | The netns was created by `unshare(CLONE_NEWNET)` inside the child with no `runtime.LockOSThread()`. That moves only the calling thread, and Go schedules goroutines across threads freely. Now requested as a clone flag, covering the whole process from birth. | Measured: after the in-process unshare, **52 of 64 goroutines were still in the original namespace and one reached 1.1.1.1:443**. |

Cross-compilation verified for linux/amd64, linux/arm64, windows/amd64,
darwin/amd64 and darwin/arm64 after every commit. `go vet` clean throughout.

### 12.2 A correction, and what F68 does *not* mean

While investigating F68 I suspected the sandboxed target was itself escaping the
network namespace. **That was wrong, and worth recording as wrong.** A direct
probe showed **12 of 12** exec'd children landing in the unshared namespace with
outbound traffic blocked: `fork` inherits the calling thread's namespaces, and in
practice the fork happened from the thread that had unshared. So F68 was a
determinism and correctness-of-design defect affecting the *supervisor* (where the
egress proxy runs, which is why F31 presented as "no route"), not a live target
escape. The fix is still right — relying on which thread the Go scheduler picked
is not a security posture — but the blast radius was narrower than first assumed.

### 12.3 Limits of this verification

- **F24's runtime behaviour on arm64 is still unverified.** QEMU returns `ENOSYS`
  for the landlock syscalls, indistinguishable from a wrong syscall number — the
  same ambiguity that made the original bug present as "kernel too old". The
  constants are verified against `asm-generic/unistd.h` and by a RED→GREEN
  constant test on aarch64; the create→add→restrict sequence is not. Real arm64
  hardware or a KVM arm64 runner would close it.
- **A test skip still masks that class of bug.** The landlock tests skip when
  `landlockCreateRuleset` fails, which on arm64 looked like "old kernel" rather
  than "wrong constant" — the exact misdirection F24 describes, reproduced in the
  new tests. An unexpected `ENOSYS` on a kernel that should support Landlock
  ought to fail loudly.
- **The macOS half of F67 is inferred.** `filepath.Base` uses the same separator
  logic on darwin, so the failure is certain by construction, but no macOS run
  was performed.
- **`go test -race ./...` passing does not clear F3.** No test drives
  `EgressProxy.allowed` concurrently; the race needs the targeted probe from
  §8.2. F3 remains open, and the fix is one lock.
- **Unprivileged Linux is unchanged.** Creating the namespace still needs root or
  `CAP_NET_ADMIN`, exactly as the previous `unshare` did — the clone-flag move
  changed determinism, not privilege requirements. Pairing `CLONE_NEWUSER` with
  it would make proxy mode work for an ordinary user and is worth its own pass.

### 12.4 Still open

- **F3** — the `p.session` data race. One lock, plus promoting the §8.2 probe to a
  permanent test.
- **F22 / F51 / F52** — macOS: the default caller still passes `config.NvxHome`
  and the runtime dir as writable roots, the variadic signature still lets a
  caller add roots silently, and the tests still pass against the vulnerable
  shape. **F64 has removed the escalation path to unsandboxed execution, but the
  write grant itself remains**, so F65 (a forgeable pin store) still stands.
- **Windows** — F46 (piped stdio never reaches the child; both
  `STARTF_USESTDHANDLES` and `bInheritHandles=TRUE`, or the child fails to start
  at all) and F1 / Fix A (the measured per-launch `icacls` ancestor-grant stall,
  still present at three call sites).
- **F25** — the forgeable `NVX_SANDBOX` re-entrancy marker. Needs an unforgeable
  cross-process signal, not a patch; `inSandboxSession()` is in-process only.
- **F12 / F27** — CI still does not execute the sandbox. The cheapest start is
  `TestUnelevatedSandboxRunsPackageManager`, which already drives a real
  AppContainer launch but is gated behind `NVX_PROBE=1` (§9.2). Note that the new
  Linux tests added here *do* run in CI and genuinely exercise Landlock, seccomp
  and the egress path, so the Linux half of this gap is now largely closed.
