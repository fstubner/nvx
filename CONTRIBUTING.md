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

```powershell
go build -o nvx.exe .
./scripts/sandbox-enforcement-windows.ps1
```

This is the one platform gate CI cannot run. GitHub-hosted Windows runners
refuse to create AppContainer children — `CreateProcess` returns "Access is
denied" for every executable, including `cmd.exe` — so the script detects that
and skips, and the step in CI is there to start asserting if that ever changes
rather than to assert today. On a real Windows machine it takes about a minute
and prints each result, so a partial regression is visible rather than just a
pass or fail.

That is why `docs/enforcement-matrix.md` says **measured** for the Windows
column and **CI** for the other two. Running this is what keeps the word
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
