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

### Before cutting a release, on Windows

Two commands, both of which need a real Windows machine:

```powershell
go build -o nvx.exe .
./scripts/sandbox-enforcement-windows.ps1
```

```powershell
$env:NVX_PROBE=1; go test -timeout 40m .
```

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

Expect **0 failures and exactly these 4 skips**:

| Skip | Why it is expected |
|---|---|
| flaky feasibility prototype | excluded on purpose; needs `NVX_PROBE_PROTOTYPES=1` |
| internal child for the launch timing probe | a helper, not a test |
| creating symlinks needs Developer Mode | environment, not product |
| this machine has no nvx loopback exemption | the healthy state; the exempt branch is covered by `sandbox_loopback_exemption_seam_windows_test.go` |

A fifth means something is quietly not being checked — go and look at it rather
than at this table. Last measured on Windows 11, 2026-08-24: **332 passing** in
about 4½ minutes.

That number is a tripwire and it has already caught something. It read "3 skips"
while the real count was 4, and the extra one was the loopback-exemption check
verifying nothing on a healthy machine — found by an acceptance pass noticing the
mismatch, not by anyone re-reading the tests.

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
