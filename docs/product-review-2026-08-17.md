# nvx — Engineering Assessment, 2026-08-17

**Assessor:** Claude (Opus 5), independent of `docs/assessment-2026-08-16.md`
**Subject:** `audit-remediation` @ `bb17317`, including the 14 remediation commits
made earlier the same day, which had received no independent review.

---

## 1. Scope

**In scope, read in full:**

* `download.go` — checksum verification and archive extraction (lines 55–240)
* `install.sh`, `install.ps1` — PATH persistence and profile integration
* `verify-security.ps1`, `verify-security.sh` — the security-gate scripts
* `.github/workflows/ci.yml`, `release.yml`, `build-release.ps1`
* `sandbox_write_scope.go`, `sandbox_marker_verify.go`, `shim_options.go`
* `sandbox_wsl.go` / `sandbox_wslc.go` env construction, `sandbox.go` docker args
* `go.mod`, and the full diff of all 14 commits on this branch

**In scope, examined via tooling only** (coverage report, gosec, vet, build): the
remaining 66 non-test Go files.

**Out of scope, not opened:** `main.go` (1,103 LOC), `env.go` (844), `policy.go`
(666), `provider_bun.go` (503), `version.go` (392), `doctor.go` (319), and the
platform sandbox files beyond the regions changed this session;
`docs/superpowers/` plans and specs (4,000+ lines); `assets/`; `bench.yml` and
`bench.py`. These were excluded for context budget, not because they are low-risk —
`main.go` and `policy.go` in particular carry the CLI surface and the trust model.

**Depth:** `targeted` for the in-scope-read list; `surface` for the remainder.
This is **not** a `deep` assessment — I did not attempt every check in the rubric
against every module, and the untouched files above have had no line-by-line read
from me at any point.

---

## 2. Environment

| Property | Value |
|---|---|
| Language | Go, single module `github.com/fstubner/nvx` |
| Dependencies | **None.** `go.mod` declares no `require` block |
| `go.mod` directive | `go 1.23` |
| Local toolchain | **`go1.19` windows/amd64** (`C:\Program Files\Go`) |
| CI toolchain | `1.26.4` (`ci.yml`, `release.yml`) |
| Release build | `build-release.ps1` downloads + SHA256-verifies Go `1.26.4` |
| Domain | CLI tool: runtime version manager with a security sandbox |
| Targets | Windows (AppContainer), Linux (Landlock + netns + seccomp), macOS (Seatbelt) |
| Size | 66 non-test files / 10,940 LOC; 38 test files / 5,967 lines |

---

## 3. Tooling Results

**Run successfully:**

| Tool | Result |
|---|---|
| `go build ./...` | Clean |
| `go vet ./...` | Clean (also `GOOS=linux`, `GOOS=darwin`) |
| `go test ./...` | Pass, **35.9%** statement coverage |
| `go test -race ./...` | Pass |
| `gofmt -l .` | **16 files** unformatted |
| `govulncheck ./...` | **54 stdlib vulnerabilities** — against the local `go1.19` toolchain only |
| `gosec ./...` | **83 issues**: 32 MEDIUM, 51 LOW, 0 HIGH. Scanned 47 files / 9,923 lines |
| Cross-compile | 5 targets build (linux amd64/arm64, windows amd64, darwin amd64/arm64) |
| Linux containment tests (privileged container, kernel 6.18) | All pass, no skips |

**Unavailable:** `staticcheck`, `golangci-lint` — not installed. These would have
covered dead code, unused results, and lint classes gosec does not.

**Not attempted:** fuzzing (the SHASUMS and archive-header parsers are the obvious
targets), load testing, penetration testing, any execution on macOS.

**Important tooling caveat:** `gosec` ran on Windows and therefore analysed only
files satisfying Windows build tags — 47 of 66. **The Linux- and macOS-specific
sandbox files were not statically analysed at all.**

---

## 4. Findings

| # | Sev | Area | Finding | Evidence | Recommendation |
|---|---|---|---|---|---|
| 1 | **High** | Reliability | **The Unix installer produces a broken install.** `install.sh` appends only `eval "$(nvx env)"` to the shell profile and never puts `nvx` on `PATH`. On the next shell the eval runs before `nvx` is resolvable, so every new shell prints `nvx: command not found` and nvx never activates. | `install.sh:121` writes `$INTEGRATION_LINE` only; `install.sh:143` *echoes* `export PATH="$HOME/.nvx/bin:$PATH"` as advice rather than writing it. `install.ps1:41` does it correctly via `SetEnvironmentVariable`. | Write the `export PATH=...` line into the profile ahead of the eval, in `setup_profile`. |
| 2 | **High** | Security / Tests | **The entire download-verification path is untested.** Checksum verification is the product's core supply-chain promise and parses untrusted network input, yet no test exercises it. | `go tool cover -func`: `ComputeSHA256` 0.0%, `VerifyNodeChecksum` 0.0%, `VerifyChecksumFromShasums` 0.0%, `verifyExpectedSHA256` 0.0%, `ExtractZip`/`extractZip`/`ExtractZipFlat` 0.0%, `ExtractTarGz` 0.0%, `copyArchiveFile` 0.0% (the zip-bomb cap), `runVerifyInstall` 0.0%. Only `safeArchiveTarget` (100%) and `safeArchiveTargetStrip` (80%) are covered. | Table-test `VerifyChecksumFromShasums` against a local manifest + file: match, mismatch, missing entry, empty manifest. Then `ExtractTarGz`/`extractZip` against fixture archives including a symlink escape and an oversized member, since those guards are written but never executed. |
| 3 | **High** | Reliability | **`verify-security.ps1` reports success unconditionally.** PowerShell does not trap native non-zero exits, and the script never checks `$LASTEXITCODE`, so the security gate always prints success. Its `.sh` twin has the opposite defect and always aborts. | Executed: `gosec -exclude=G204,G304,G301,G306 ./...` → **exit 1, 53 issues**. `verify-security.ps1:26` runs gosec; `:29` prints `"All security scans completed successfully!"` regardless. `verify-security.sh:5` sets `set -e`, so the same non-zero exit aborts the script instead. | Capture and check `$LASTEXITCODE` after each tool in the `.ps1`; in the `.sh`, run the tools with `set +e` and aggregate a real pass/fail. Neither script currently produces a usable signal. |
| 4 | **Medium** | Process | **Local builds use Go 1.19 while CI and releases use 1.26.4** — a four-major divergence. Everything verified locally, *including all 14 remediation commits from this session*, was compiled by a 2022 toolchain that is not the one shipping. | `go version` → `go1.19 windows/amd64`; `go version <test binary>` → `go1.19`; `where go` shows a single install. `ci.yml:24` and `release.yml:24` pin `1.26.4`. `govulncheck` reports 54 stdlib vulnerabilities against the local toolchain. | Upgrade the local toolchain to 1.26.4 and re-run the suite; a green run on 1.19 does not establish a green run on CI. **Released binaries are unaffected** — see Strengths. |
| 5 | **Medium** | Process | **`go.mod` says `go 1.23` but the local toolchain silently ignores it.** Go before 1.21 treats the directive as advisory, so a contributor on an old toolchain gets no error — only divergent behaviour. It surfaces only as a `note: module requires Go 1.23` hint appended to unrelated compile errors. | `go.mod:3` `go 1.23`; local `go1.19` compiles the module regardless. | Add a toolchain check to CI or a `toolchain` directive; contributors on ≥1.21 auto-switch, those below need to be told rather than silently diverging. |
| 6 | **Medium** | Correctness | **My own change: `containmentDisproved` is wrong-by-design for the container providers.** The probe treats "the real home is writable" as proof of no containment. Inside a Docker or WSL container the real home *is* writable, so the check would disprove containment and re-sandbox — despite the marker being set legitimately. | `sandbox_wslc.go:64` and `sandbox_wsl.go:47` set `NVX_SANDBOX=1`; `sandbox_marker_verify.go:32` probes the real home. Latent only because `sandbox.go:339` runs `config.Command` — not nvx — in the container, and nvx is not mounted (`sandbox.go:329` mounts only the cwd). | Gate the probe on the native providers, or detect container isolation separately. This becomes live the moment nvx runs inside a container, which the "cage" design in `docs/` contemplates. |
| 7 | **Medium** | Architecture | **My own change: the "single declaration" of sandbox write scope is not single.** `sandboxWritableRoots` documents itself as the one place the write scope is decided, and the Seatbelt and Landlock paths consume it — but the Windows path still hardcodes its two grants, so it can drift from the declaration the tests pin. | `sandbox_write_scope.go:22` claims the role; `sandbox_seatbelt.go:109` and `sandbox_landlock_linux.go:196` consume it; `sandbox_appcontainer_windows.go:37,51` still call `grantAppContainerPath` directly. | Either drive the Windows grants from the shared list (keeping the differing error handling via an explicit per-root switch), or soften the comment so it does not overstate the guarantee. As written, the doc is ahead of the code — the exact pattern this codebase keeps repeating. |
| 8 | **Medium** | Process | **My own change: the new CI Windows probe step has never run on a GitHub runner.** It was added specifically to replace an assumption with a measurement, but the measurement has not been taken, and it can fail the job. | `ci.yml` "Windows AppContainer probes" runs `NVX_PROBE=1 go test -run 'TestPipedStdio\|TestAppContainer\|TestUnelevatedSandbox'`. Verified on local Windows only: 4 pass, 1 skips. | Push to a branch and observe before merging to `main`, so the first data point is not on the default branch. |
| 9 | **Low** | Maintainability | **16 files fail `gofmt`, and CI never checks formatting.** | `gofmt -l .` → 16 files; `gofmt -d sandbox_linux.go` shows a stray trailing blank line. `ci.yml` has build/test/smoke steps but no format step. | Run `gofmt -w .` and add `gofmt -l .` as a failing CI step. |
| 10 | **Info** | Security | **gosec: 32 MEDIUM, 51 LOW, no HIGH.** The MEDIUMs are dominated by `G304` (file inclusion via variable, ×23) and `G204` (subprocess with variable, ×7), both largely inherent to a tool whose purpose is running user-named binaries against user-named paths. | `gosec -fmt=json`; `G302` ×2 at `provider_bun.go:473,484` is a **false positive** — `copyExecutable` uses `0755` because the file is an executable. | No action on G302. The G304 sites deserve a one-time audit confirming each path is validated before use, but none is evidently exploitable. |

---

## 5. Unconfirmed / Requires Investigation

* **`findShasumEntry` fallback leniency.** Two branches return a hash without
  matching the filename: a `Hash :` line with no `Path :` line
  (`download.go:218`), and a single lone hash (`download.go:223`). In isolation
  that looks permissive. I could **not** show it exploitable: the manifest and the
  archive come from the same origin, so an attacker able to forge one can forge
  both, and a wrong hash still fails the comparison closed. The function is also
  the best-tested in the file (`remediation_test.go:235–258`, 10 cases including
  both fallbacks and negatives). Recorded as unconfirmed, not as a finding.
* **The 54 `govulncheck` stdlib findings** reach users only if someone builds and
  distributes with a local toolchain instead of `build-release.ps1`. I did not
  examine whether any release has ever been produced that way.
* **`.xtctx/`** is neither tracked nor ignored. Not examined; likely local tooling
  state, but it will show up in `git status` for every contributor.

---

## 6. Summary

### Strengths

* **Zero third-party dependencies.** `go.mod` has no `require` block at all. For a
  tool whose premise is supply-chain safety, having no transitive dependency
  surface is the strongest possible statement of intent, and it is rare.
* **The release toolchain is itself verified** — in intent. `build-release.ps1`
  downloads Go, compares a published SHA256 and refuses to proceed on mismatch,
  which is better hygiene than most projects apply to their own artifacts.
  **Correction (same day):** the comparison was broken. It fetched
  `<file>.sha256`, which go.dev no longer serves — that URL returns HTTP 200 with
  an HTML page, so the script parsed `<!DOCTYPE` as the expected hash and threw on
  every run. Verified by executing it; fixed to read the hash from the download
  index. The intent was right and the fail-closed direction was right, but this
  strength was not, as written, being delivered.
* **The verification path is fail-closed by construction.** Missing manifest entry,
  empty expected hash, and mismatch all return errors rather than proceeding
  (`download.go:126,146,137`). The logic is correct; only its test coverage is
  absent.
* **`findShasumEntry` is genuinely well-tested** — 10 cases spanning binary-mode
  markers, `./` prefixes, PowerShell `Get-FileHash` output, and negative cases.
* **No secrets in tracked files** (`git grep` for key/token/password shapes: none).
* **The Linux containment story is now real and executable.** All Landlock, seccomp,
  syscall-table and reaping tests pass on a 6.18 kernel, privileged and
  unprivileged.

### Key Risks

1. **The product is undeliverable on two of three platforms as installed**
   (finding #1). The sandbox work is moot if `nvx` is not on `PATH` after
   installation. This is the single highest-value fix in the report and it is
   about ten lines of shell.
2. **The core security promise is unverified** (finding #2). Checksum verification
   and archive extraction — the supply-chain guarantees the README leads with —
   have no executing test. The guards read correctly; nothing proves they run.
3. **Both security-gate scripts are non-functional in opposite directions**
   (finding #3), so the project's own security check produces no signal either way.
4. **Verification fidelity** (findings #4, #5, #8). Local green is a 1.19 green;
   CI green on Windows is still unobserved. Several of this session's confidence
   claims rest on that.
5. **Two of my own remediation commits overstate or misplace their guarantees**
   (findings #6, #7) — a doc claiming a single source of truth that is not single,
   and a containment probe that is wrong for container providers.

### Priority Order

1. **#1 — installer PATH.** Highest impact, smallest fix, blocks everything else
   from mattering on macOS and Linux.
2. **#3 — security-gate scripts.** Cheap, and until fixed no other security signal
   from this repo can be trusted.
3. **#2 — test the verification path.** Largest genuine security gap; start with
   `VerifyChecksumFromShasums` and the two extraction guards that exist but never
   execute.
4. **#4 — upgrade the local toolchain**, then re-run the suite to confirm this
   session's results hold on 1.26.4.
5. **#8 — observe the Windows CI run on a branch** before merging.
6. **#6, #7 — correct my own two overstatements** while the context is fresh.
7. **#9, #5 — gofmt and the toolchain directive.** Mechanical.

### Coverage Gaps

* **Not read line by line:** `main.go`, `env.go`, `policy.go`, `provider_bun.go`,
  `version.go`, `doctor.go`, and the platform sandbox files outside the regions
  changed this session. Together that is the majority of the codebase, and it
  includes the CLI surface and the policy trust model. **No finding above should be
  read as evidence that those files are sound.**
* **`gosec` analysed only 47 of 66 files** — Windows build tags. The Linux and
  macOS sandbox implementations have had no static analysis.
* **No execution on macOS.** The Seatbelt profile is verified only at generation
  level, here and in the prior audit. Whether `sandbox-exec` enforces it as written
  remains untested on real hardware.
* **The Docker, WSL, WSLc and nspawn providers were not executed** — only their
  argument construction was read.
* **`staticcheck` and `golangci-lint` unavailable**; no dead-code or unused-result
  analysis was performed.
* **No fuzzing, load, or penetration testing.** The SHASUMS parser and archive
  header handling are the natural fuzz targets and remain unfuzzed.
* **`docs/superpowers/` plans and specs not read** (4,000+ lines), so I cannot say
  whether the design documents match the current implementation.
* **The prior audit's findings were not re-verified**, except where they intersected
  the areas above. This report is independent, not a superset.
