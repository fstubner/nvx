# Contributing to nvx

Thanks for your interest in improving nvx! This document covers how to build,
test, and submit changes.

## Ground rules

- Be respectful. This project follows the [Code of Conduct](CODE_OF_CONDUCT.md).
- **Do not report security vulnerabilities in public issues or PRs.** Use the
  private channel described in [SECURITY.md](SECURITY.md).
- Keep changes focused. One logical change per pull request.

## Project overview

nvx is a single-module, **zero-dependency** Go program (standard library only).
That constraint is intentional — it keeps the trusted computing base small and
auditable. Please do not add third-party dependencies without discussing it
first in an issue.

Key areas:

- **Runtime management** — `version.go` (the `RuntimeProvider` interface and
  `NodeProvider`), `download.go` (download + checksum verification + safe
  extraction), `env.go` (version detection, PATH, shims).
- **Security layer** — `policy*.go` (policy model and loading), `security.go`
  (typosquatting, OSV, release-age), `egress_proxy.go` (allowlist proxy).
- **Sandboxing** — `sandbox*.go`, split per OS/primitive
  (`sandbox_appcontainer_windows.go`, `sandbox_landlock_linux.go`,
  `sandbox_seatbelt.go`, etc.).

## Prerequisites

- Go **1.23 or newer** (releases are built with 1.26.4).
- To exercise sandboxing locally you need the platform primitives:
  - **Linux:** kernel 5.13+ (Landlock), `iproute2` (network namespace).
  - **macOS:** `/usr/bin/sandbox-exec`.
  - **Windows:** AppContainer support (Windows 10+).

## Build and test

```sh
go build ./...
go test -race ./...
```

Platform sandbox smoke tests live in `scripts/` and are run by CI:
`sandbox-smoke.sh` / `.ps1` and `sandbox-smoke-egress.*`.

Alongside them are three **enforcement** probes, which are the ones that can
fail. A smoke test checks that a contained process runs; an enforcement probe
asserts what must be denied *and* what must still be allowed, so a sandbox that
refuses everything fails it rather than passing:

| Script | Runs where |
|---|---|
| `sandbox-enforcement-linux.sh` | CI, every build, unprivileged |
| `sandbox-enforcement-macos.sh` | CI, every build |
| `sandbox-enforcement-windows.ps1` | **Manually — see below** |

### Running the Linux probe without a Linux machine

WSL2 is enough, and it is worth doing: until 2026-09-01 the Linux column of
`docs/enforcement-matrix.md` rested entirely on CI, which is the same evidence
the Windows column had when it was found to be wrong. Cross-compile from
Windows, stage into WSL's own filesystem (not `/mnt`, which is slow and has
different permission semantics), and run:

```sh
GOOS=linux GOARCH=amd64 go build -o /tmp/nvx-linux .
GOOS=linux GOARCH=amd64 go test -c -o /tmp/nvx-linux.test .   # the Linux-only tests
```

The test binary matters as much as the script. `sandbox_landlock_*_test.go` and
`sandbox_network_mode_linux_test.go` are behind `//go:build linux`, so they never
compile on a Windows developer machine — running the cross-compiled binary is the
only way to see them pass anywhere but CI. `-race` does not cross-compile (it
needs cgo), so that run is without the detector; the Windows gate covers that.

Check `unshare -Urn -- ip link set lo up` succeeds first. If it does not, the
script skips its egress assertion and still exits 0 — a pass that proves the
filesystem half only.

### Before cutting a release, on Windows

Two commands, both of which need a real Windows machine:

```powershell
go build -o nvx.exe .
./scripts/sandbox-enforcement-windows.ps1
```

```powershell
$env:NVX_PROBE=1; go test -race -timeout 40m .
```

**`-race` is part of the gate, not an optional extra.** Without it, nothing in
this project ever ran the probe tests under the detector: CI's unit step uses
`-race` but not `NVX_PROBE`, and CI's probe step used `NVX_PROBE` but not
`-race`, so the two were never combined and this line matched the latter. A
probe test held a data race indefinitely as a result — a `strings.Builder`
shared between `os/exec`'s copier goroutines and a poll loop — and it took an
acceptance pass running both flags together to see it. Both now use `-race`.

This is the one platform gate CI cannot run. GitHub-hosted Windows runners
refuse to create AppContainer children — `CreateProcess` returns "Access is
denied" for every executable, including `cmd.exe` — so anything that launches a
live contained process skips there. The enforcement script detects that and
skips; the CI step exists to start asserting if a future runner image can host
one, not to assert today.

`NVX_PROBE=1` matters as much as the script. Those probes launch real
AppContainers to check that a sandbox cannot read another project, that a deny
ACE hides a secret, that one session cannot read another's guest home, and that
the relay does not expose host loopback services — roughly twenty end-to-end
containment assertions that skip on hosted CI and run here.

Expect **0 failures and exactly these 6 top-level skips**. `go test -v` also
prints a further `--- SKIP` line for the subtest
`TestStageAppContainerExecutableThroughALinkedDirectory/symlink`, which has the
same Developer Mode cause as the third row — count top-level skips, or a reader
following this literally goes looking for a phantom:

| Skip | Why it is expected |
|---|---|
| `TestReverseRelayReachesAServerInsideTheContainer` | flaky feasibility prototype, excluded on purpose; needs `NVX_PROBE_PROTOTYPES=1` |
| `TestMeasureContainedLaunchPhasesNoOpChild` | internal child for the launch timing probe — a helper, not a test |
| `TestExtractTarGzAllowsInternalSymlink` | creating symlinks needs Developer Mode; environment, not product |
| `TestExemptMachineIsWarnedAbout` | this machine has no nvx loopback exemption — the healthy state; the exempt branch is covered by `sandbox_loopback_exemption_seam_windows_test.go` |
| `TestPipedStdioReachesRealAppContainerChild` | needs write access to the DACL on `C:\WINDOWS\System32\cmd.exe`, which an unelevated account does not have. **Expected on a normal run**, since nvx is meant to be used without elevation — run the gate elevated to make this one assert. |
| `TestReportsItsOwnRaceBuildTag` | the child half of the uninstrumented-probe-child check — it only does anything when run as a child with `NVX_REPORT_RACE=1`, so in the parent it is a helper, not a test |

A seventh means something is quietly not being checked — go and look at it rather
than at this table. Last measured on Windows 11, 2026-08-31, unelevated:
**444 passing, 6 skipping, 0 failing** on Windows (Linux adds two more: the network-mode readers only build there). Measured on this machine: 163–209s under
-race and 154s without, so the detector costs roughly a quarter, not the double
this line claimed until an acceptance pass measured it.

The summary line must read `ok github.com/fstubner/nvx <time>` and nothing else.
`[no tests to run]` appended to it means a child process wrote to the test
binary's stdout and `go test` attributed it to the package — the gate's headline
then reads exactly like a run in which nothing executed. See
`probe_control_child_silent_test.go`.

That duration used to be nine to twelve minutes, and the drop is a fix rather
than a shortcut: nearly all of it was this binary waiting out the same stalled
ACL writes over and over. A permission write that overruns its deadline is
abandoned, and a path that has stalled once is not retried now, so the hundreds
of launches in one test process no longer each pay the timeout. An acceptance
pass found that accumulation the other way round — as a run that died with
`runtime: SetWaitableTimer failed`, with 49 of 83 goroutines blocked in that
write.

**If you see this take many minutes again, that is the regression**, not a slow
machine.

### The gate needs free memory, and says so when it does not

The probes run a copy of the test binary inside each AppContainer. Under `-race`
that child used to be race-instrumented too, which made a gate run cost several
gigabytes of commit charge in bursts; on a machine near its commit limit, whatever
happened to be allocating at that moment failed instead. Measured: three of ten
`-race` runs failed against none of ten without it, wearing five different faces —
"The paging file is too small for this operation to complete", `error code: 1455`
(ERROR_COMMITMENT_LIMIT), `exit status 0xc0000142` (STATUS_DLL_INIT_FAILED), a
`net.Listen` refusing, and interface enumeration coming back empty. Two of those
five arrived as SKIPS, so a run could go green having checked no containment at
all.

Two things now stop that. The child is rebuilt once per run without
instrumentation, which is worth 212.5MB against 54.0MB of peak commit per child on
the same workload — the parent keeps `-race`, which is the half that catches real
races. And a refused AppContainer launch is only excused after a control launch
has shown this host genuinely cannot create them; on a host that can, a refusal is
a failure with the machine's commit headroom printed next to it.

**A run whose skip count exceeds the table above has not checked what it claims**,
whatever the failure count says. That is the number to read first.

An extra skip beyond the table appeared once and is not in it:
`TestDenyACEHidesSecretFromAppContainer` self-skips when it cannot stage the test
binary as a contained child. The test documents that as intermittent on hosted
runners; it ran normally on the next attempt.

**Adding a test? Update the pass count in the same commit.** The skip list is the
tripwire; the pass count is a fact with a short shelf life, and it has now gone
stale five times — at 337, 392, 394, 397 and 400, each time because a test
was added in the commit after the count was written.

A seventh skip appeared once for a reason worth recording, because it looked like
a product fault and was not: `TestDoctorDiagnosesAPolicyItCannotRead` needs
`nvx doctor` to report a healthy baseline before it can assert anything, and an
unrelated `package.json` in the home directory made doctor report every temporary
directory as a project carrying leftover grants. Deleting that file restored the
sixth-skip count. If this one starts skipping, look above the working directory
for a stray manifest before looking at doctor. A number nobody maintains
teaches the reader to ignore the table it sits in.

That number is a tripwire and it has caught something three times. It read "3 skips"
while the real count was 4, and the extra one was the loopback-exemption check
verifying nothing on a healthy machine. Then it read "4 skips" and "337 passing"
while an unelevated run — the normal way to run this, since nvx is built not to
need elevation — produced 5 and 392: the `cmd.exe` row was missing, so the table
told a maintainer on a clean checkout that something was quietly not being
checked. All three were found by an acceptance pass noticing the mismatch, not
by anyone re-reading the tests. The third time it read 394 against a real 396,
again because tests were added after it was written -- which is what the note
above is now for. Rows are named, so a mismatch says which one rather than only
how many.

That is why `docs/enforcement-matrix.md` says **measured** for the Windows
column and **CI** for the other two. Running both is what keeps the word
"measured" true.

## Making changes

1. Fork and create a topic branch from `main`.
2. Make your change with tests. New behavior needs a test; bug fixes need a
   regression test.
3. Run `go build ./...` and `go test -race ./...` — both must pass.
4. Run `gofmt -w` on changed files. CI runs `govulncheck` and `gosec`
   (`-severity=high -confidence=high`); avoid introducing new findings.
5. Open a PR describing **what** changed and **why**. Link any related issue.

### Guidance for security-sensitive changes

- Preserve the **fail-closed** invariant: if you touch policy loading, sandbox
  setup, or egress control, a failure must deny the operation, not allow it.
- Never widen what a *project-local* policy can do without an explicit,
  fail-closed user confirmation.
- If you add a new `RuntimeProvider` or `FilesystemProvider`, include the
  capability/availability checks and document its enforcement guarantees.

## Commit and PR style

- Write imperative commit subjects ("Add Bun provider", not "Added…").
- Keep unrelated formatting churn out of functional PRs.
- By contributing, you agree your contributions are licensed under the
  project's [MIT License](LICENSE).
