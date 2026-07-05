# Extending nvx: custom runtime & isolation providers

nvx has two open extension points, both backed by registries you can add to
from a single self-contained file. Nothing else in the codebase needs to change:

| You want to add… | Implement | Register with | Selected by |
|---|---|---|---|
| A new **runtime** (Deno, Python, Go, a private toolchain) | `RuntimeProvider` | `RegisterRuntimeProvider` | `nvx install <runtime>@<version>` |
| A new **isolation backend** (gVisor, Firejail, a remote sandbox) | `IsolationProvider` | `RegisterIsolationProvider` | `isolation.provider` in policy, or `--isolation-provider=` |

Run `nvx doctor` at any time to see every registered runtime and isolation
provider and whether it is available on the current host.

---

## 1. Adding a runtime provider

### The interface (`version.go`)

```go
type RuntimeProvider interface {
    Name() string                                       // registry key, e.g. "deno"
    Install(version, nvxHome string) error              // download + verify + place under versions/<name>/v<ver>
    Uninstall(version, nvxHome string) error
    ResolveVersion(query string) (string, error)        // "latest"/"1.2"/"1.2.3" -> canonical "v1.2.3"
    ListRemote() ([]string, error)                      // available versions
    ListLocal(nvxHome string) ([]string, error)         // installed versions (dir names under versions/<name>)
    DetectConfig(dir string) (version, sourceFile string, err error) // e.g. read .deno-version

    ShimCommands() []string                             // commands this runtime owns, e.g. ["deno"]
    ResolveBinary(cmd, nvxHome, pinnedVer string) string // absolute path to the real binary, or ""
    DefaultNetworkAllow() []string                      // egress allowlist inside the sandbox
}
```

### Contract / conventions

- **Install layout:** place binaries under `versions/<Name()>/v<version>/bin/<cmd>`.
  The shared helpers (`resolveLocalVersion`, `getLatestLocal`, `CompareVersions`)
  expect `v`-prefixed version directory names (`v1.2.3`).
- **Verify what you download.** Always check an integrity artifact (checksums or
  signatures) before extraction — see `verifyBunChecksum` / `VerifyNodeChecksum`.
  Reuse `DownloadFile`, `ComputeSHA256`, `ExtractZip`, `ExtractTarGz` (they
  already guard against zip-slip / tar-slip / symlink escape).
- **`ResolveBinary` returns `""`** when the command isn't provided by an
  installed version — nvx then falls back to project `node_modules/.bin` and the
  ambient `PATH`.
- **`ShimCommands` must be unique across runtimes.** They populate the shim
  router (`runtimeForShim`); the last registration wins on a conflict.

### Worked example

`runtime_bun.go` is a complete, ~250-line reference implementation (Bun via the
`oven-sh/bun` GitHub releases: version resolution over the API, `SHASUMS256.txt`
checksum verification, zip extraction, binary relocation). Copy it as a starting
point. The **entire wiring** is one line:

```go
func init() { RegisterRuntimeProvider(BunProvider{}) }
```

That is the only hook — no switch statements, no CLI edits. Once registered:

```
nvx install deno@1.44     # ResolveRuntimeSelector routes to your provider
nvx use deno@1.44
nvx list                  # shows your runtime's installed versions
nvx which deno
nvx doctor                # lists your runtime + its shims
```

### How selection works

`ResolveRuntimeSelector(query)` (in `version.go`) maps a CLI token to a provider:

| Query | Provider | Version |
|---|---|---|
| `20` | `node` (default) | `20` |
| `deno@1.44` | `deno` | `1.44` |
| `deno` | `deno` | `latest` |

A bare version with no `<runtime>@` prefix uses the default runtime (`node`), so
existing `nvx install 20` usage is unchanged.

---

## 2. Adding an isolation provider

Isolation backends used to be a fixed `switch`; they are now an open registry so
you can add e.g. a gVisor, Firejail, bubblewrap, or remote-sandbox backend. Each
provider enforces the filesystem, network, and process boundaries for the
sandboxed command.

### The interface (`isolation_providers.go`)

```go
type IsolationProvider interface {
    Names() []string                 // canonical name first, then aliases
    Description() string             // one line shown by `nvx doctor`
    Available() bool                 // can this run on the current host?
    Launch(ctx SandboxLaunchContext) int // run the command confined; return exit code
}

type SandboxLaunchContext struct {
    Config    SandboxConfig        // Command, Args, NvxHome, WorkDir, …
    Policy    Policy               // effective merged policy
    Egress    *EgressProxy         // started loopback proxy (may be nil)
    Network   NetworkLaunchContext // mode + proxy host/ports
    PinnedVer string               // resolved runtime version, if any
}
```

### Contract / conventions

- **Fail closed.** If your isolation cannot be established, return non-zero and
  do **not** exec the command. Every built-in native path does this — a sandbox
  that silently degrades to "unsandboxed" is worse than an error.
- **Scrub the environment** you pass to the child with `scrubEnvironment(guestHome)`
  (allowlist / deny-by-default) rather than forwarding `os.Environ()`.
- **Honor the network mode.** In `proxy`/`offline`/`loopback`, route or block
  egress; `ctx.Egress` gives you the loopback proxy's host/ports via
  `HTTPListenHostPort()` / `SOCKSListenHostPort()`, and `applyProxyEnv` injects
  the proxy vars into a child env.
- **`Available()` is advisory** (used by `nvx doctor`); `Launch` should still
  re-check and fail closed if prerequisites are missing at run time.

### Worked example (minimal)

```go
// sandbox_firejail.go
package main

import "os/exec"

type firejailProvider struct{}

func (firejailProvider) Names() []string     { return []string{"firejail"} }
func (firejailProvider) Description() string { return "Linux Firejail sandbox" }
func (firejailProvider) Available() bool     { return commandExists("firejail") }

func (firejailProvider) Launch(ctx SandboxLaunchContext) int {
    guestHome, _ := createGuestProfile(ctx.Config.NvxHome, mustSandboxID())
    env := scrubEnvironment(guestHome)
    env = applyProxyEnv(env, ctx.Egress) // honor the egress proxy

    args := []string{"--quiet", "--private=" + guestHome, "--", ctx.Config.Command}
    args = append(args, ctx.Config.Args...)
    cmd := exec.Command("firejail", args...)
    cmd.Env = env
    cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
    if err := cmd.Run(); err != nil {
        if ee, ok := err.(*exec.ExitError); ok {
            return ee.ExitCode()
        }
        LogError("firejail sandbox failed (fail-closed): %v", err)
        return 1
    }
    return 0
}

func init() { RegisterIsolationProvider(firejailProvider{}) }
```

Then:

```jsonc
// ~/.nvx/policy.json  (or a per-project .nvx-policy.json)
{ "isolation": { "provider": "firejail" } }   // legacy: isolation.filesystem.provider still works
```

or per-invocation: `npm --isolation-provider=firejail install`.

`nvx doctor` will now list `firejail` with its availability, and the sandbox
dispatcher (`runSandbox` in `sandbox.go`) will route to it automatically — no
core edits required.

---

## Building & testing your provider

```bash
go build ./...                    # compile
go vet ./...
go test ./...                     # add a _test.go for your parsing/resolution logic
GOOS=windows GOARCH=amd64 go build ./...   # if your provider is cross-platform
```

Register your provider in an `init()` in its own file, add a focused unit test
(see `providers_test.go` for the registry/selector tests), and you're done —
extension is purely additive.
