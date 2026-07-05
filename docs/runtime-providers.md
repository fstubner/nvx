# Runtime providers

nvx manages runtimes through the `RuntimeProvider` interface (`version.go`).
Five providers ship today — **Node.js** (`NodeProvider`), **Bun**
(`BunProvider`), **Deno** (`DenoProvider`), **Go** (`GoProvider`), and **Python**
(`PythonProvider`) — across two ecosystems, and the interface is designed so more
can be added without touching the CLI, sandbox, or policy code. Go and Python are
non-JavaScript runtimes and exercise the parts of the interface the JS runtimes
never touch (see "Non-JavaScript runtimes" below).

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
`bin/<exe>` layout with an alias (`bunx`). `DenoProvider` is nearly identical but
shows two small variations the shared pipeline already handles: a per-asset
sidecar checksum in PowerShell `Get-FileHash` format (see `findShasumEntry`), and
a zip whose binary sits at the archive root, extracted with `ExtractZipFlat`
instead of the folder-stripping `ExtractZip`.

## Non-JavaScript runtimes: Go and Python (shipped), Rust (roadmap)

Go was implemented as the first non-JavaScript runtime specifically to test the
parts of the interface the JS runtimes never touch. It validated the design:

- **`SessionEnv` is now used, not hypothetical.** `GoProvider.SessionEnv` returns
  `GOROOT`, and `nvx use go@1.23` emits it alongside the PATH change. Node/Bun/Deno
  still return nil.
- **`bin/` layout on every platform.** Unlike node/bun/deno (binary at the version
  root on Windows), Go keeps `bin/go` everywhere; `GetVersionBinDir` prefers a
  `bin/` subdir when present, so no interface change was needed.
- **Own version scheme.** `go1.23.4` is mapped to nvx-internal `v1.23.4`
  (`goVersionToInternal`) so the shared version helpers work unchanged.
- **Inline checksums.** The go.dev JSON index carries per-file SHA-256, so
  `verifyExpectedSHA256` verifies against a known hash rather than fetching a
  checksum file.
- **No package audit.** Go modules are not npm packages, so the `go` shim is
  sandbox-executed but not routed through the npm-oriented verifier.

Python (`PythonProvider`) then confirmed a second, messier non-JS shape using the
python-build-standalone distribution:

- **Date-tagged releases carrying many versions.** A release tag is a date; each
  carries every supported minor. A version query resolves to the newest matching
  asset *within* the latest release (`resolveInstallAsset`), rather than one
  release per version.
- **Platform quirks.** Interpreter at the version root on Windows, `bin/python3`
  on Unix; the Unix tarball uses relative symlinks, which surfaced (and fixed) a
  missing parent-dir `mkdir` in `ExtractTarGz`.
- **`python -m pip`, not a pip launcher.** The interpreter (`python`, `python3`)
  is shimmed; pip is a module invocation, so a standalone pip launcher is left to
  the post-install hook (below) rather than faked.

Walking the interface against the remaining runtime:

| Method | Rust | Verdict |
|---|---|---|
| Install / Uninstall / List* / ResolveVersion | static toolchain tarballs | Holds (as Go/Python proved) |
| DetectConfig | `rust-toolchain.toml` | Holds (a toolchain descriptor, slightly lossy as a string) |
| ResolveBinary | `bin/` | Holds |
| DefaultNetworkAllow / SandboxImage / SessionEnv | static.crates.io, `rust:<v>`, `RUSTUP_HOME`-style | Holds (Go/Python exercised these) |
| **ShimCommands** (static) | rustup components (clippy, fmt) added **after** install | **Still cracks** — needs a post-install hook |

**Known future need, deliberately not built yet:**

- A `PostInstall(version, nvxHome)` hook (or dynamic shim enumeration) for
  runtimes whose available commands are only known after install (pip-installed
  console scripts, a standalone pip launcher, rustup components). The five
  shipped runtimes all have fixed core commands, so none needs it yet; adding it
  is non-breaking because all providers live inside this module.
