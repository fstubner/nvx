# Runtime providers

nvx manages runtimes through the `RuntimeProvider` interface (`version.go`).
Two providers ship today — **Node.js** (`NodeProvider`) and **Bun**
(`BunProvider`) — and the interface is designed so more can be added without
touching the CLI, sandbox, or policy code.

## The interface

```go
type RuntimeProvider interface {
    Name() string
    Install(version, nvxHome string) error
    Uninstall(version, nvxHome string) error
    ResolveVersion(query string) (string, error)
    ListRemote() ([]string, error)
    ListLocal(nvxHome string) ([]string, error)
    DetectConfig(dir string) (version, sourceFile string, err error)
    ShimCommands() []string
    ResolveBinary(cmd, nvxHome, pinnedVer string) string
    DefaultNetworkAllow() []string
    SandboxImage(version string) string
    SessionEnv(versionDir string) map[string]string
}
```

A provider is registered by adding it to the `Providers` map (`version.go`).
The CLI (`runtime@version` parsing), shim router, list output, sandbox image
selection, and default-network allowlist all pick it up automatically.

## Adding a runtime

1. Implement `RuntimeProvider` in `provider_<name>.go`.
2. Register it in `Providers`.
3. Reuse the shared helpers: `DownloadFile`, `VerifyChecksumFromShasums`,
   `ExtractZip` / `ExtractTarGz`, `acquireRuntimeInstallLock`,
   `resolveLocalVersion`, and the `versions/<runtime>/<v>` layout so
   `getActiveShellVersionFor` / `getGlobalDefaultVersionFor` work unchanged.

`BunProvider` is the reference example: single-binary archive with a per-release
`SHASUMS256.txt`, a cached release list to stay under GitHub's rate limit, and
`bin/<exe>` layout with an alias (`bunx`).

## Readiness for Python / Go / Rust

Walking the interface against these runtimes shows where it holds and where it
would need a small, additive extension. None of these are implemented yet — this
is a design note so the contract survives their addition.

| Method | Python | Go | Rust | Verdict |
|---|---|---|---|---|
| Install / Uninstall / List* / ResolveVersion | python-build-standalone archives | go.dev/dl tarballs + JSON index | static toolchain tarballs | Holds |
| DetectConfig | `.python-version` | `go.mod` `go` directive | `rust-toolchain.toml` | Holds (Rust's is a toolchain descriptor, slightly lossy as a string) |
| ResolveBinary | `Scripts\` vs `bin/` | `GOROOT/bin` | `bin/` | Holds |
| DefaultNetworkAllow | pypi.org | proxy.golang.org, sum.golang.org | static.crates.io | Holds |
| SandboxImage | `python:<v>` | `golang:<v>` | `rust:<v>` | Holds |
| **SessionEnv** | — | **GOROOT / GOTOOLCHAIN** | **RUSTUP_HOME-style vars** | Needs the `SessionEnv` hook (added) |
| **ShimCommands** (static) | pip console-scripts appear **after** install | `go install` binaries | rustup components (clippy, fmt) | **Cracks** — needs a future post-install hook |

**Extension points already added** so the above lands cleanly later:

- `SessionEnv(versionDir)` — activate a runtime with extra environment variables
  (Go/Rust need this; Node/Bun return nil).
- `SandboxImage(version)` — container image per runtime for the Docker provider.

**Known future need, deliberately not built yet:**

- A `PostInstall(version, nvxHome)` hook (or dynamic shim enumeration) for
  runtimes whose available commands are only known after install
  (pip-installed console scripts, rustup components). Adding it is non-breaking
  because all providers live inside this module.
