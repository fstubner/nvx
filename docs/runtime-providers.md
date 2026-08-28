# Runtime providers

nvx manages JavaScript runtimes through the `RuntimeProvider` interface
(`version.go`). Two providers ship today — **Node.js** (`NodeProvider`) and
**Bun** (`BunProvider`) — and the interface is designed so additional JS
runtimes can be added without touching the CLI, sandbox, or policy code.

Experimental Deno, Go, and Python providers live on the
`feature/polyglot-runtimes` branch.

Nothing in the shipped build carries stubs for them any more. `classifyInvocation`
kept branches for `uv` and `deno`, and `uvx`/`pyx` sat in the ad-hoc-tool list,
left behind when those providers were removed — unreachable, because a command is
only ever classified after nvx has shimmed it, and none of those names is in any
provider's `ShimCommands`. Removed 2026-08-28 after an acceptance pass pointed out
that unreachable code reads as support for runtimes this build does not manage.
The branch has the real versions; a provider returning brings its own
classification with it.

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

## Shipped providers

| Provider | Install source | Version pins | Shims |
|----------|----------------|--------------|-------|
| **Node** | nodejs.org | `.nvmrc`, `.node-version`, `package.json` engines | `node`, `npm`, `npx`, `yarn`, `pnpm` |
| **Bun** | GitHub releases (oven-sh/bun) | `.bun-version`, `package.json` engines.bun | `bun`, `bunx` |

Both use checksum-verified downloads and share the npm-oriented supply-chain
verifier (`runVerifyInstall`) for install and executor commands.

## Adding a runtime

1. Implement `RuntimeProvider` in a new `provider_<name>.go` file.
2. Register it in `Providers`.
3. Add tests in `remediation_test.go` (parse spec, shims, detect config).
4. Document network allowlist defaults and Docker image naming.

If the runtime uses npm for packages (like Bun), wire install detection in
`detectShimPackagesForVerification` (`env.go`). If it uses another registry,
build a separate verifier or document sandbox-only containment.
