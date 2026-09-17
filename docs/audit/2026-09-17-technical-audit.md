# nvx technical audit — 2026-09-17

Auditor pass against `enterprise-controls` @ `2567852`, on Windows 11 with Go
1.26.4 and Node v24.14.1. Read-only: no source file was modified. Every finding
cites `file:line` or pasted command/probe output. Probes were built and run
against a scratchpad copy of the package; they are not in the repository.

## 1. Scope

In scope and read: the whole of `internal/nvx/` (403 files; every non-test file
read or skimmed), `cmd/nvx/main.go`, `install.ps1`, `install.sh`,
`.github/workflows/*`, `scripts/release/*`, `packaging/*`,
`docs/enforcement-matrix.md`, `SECURITY.md`, `go.mod`. `site/` confirmed to build
only. Historical design docs and `docs/superpowers/` not read.

Priority areas as briefed: sandbox correctness and escape surface, supply-chain
check correctness, fail-open audit, the policy engine, error-classification by
string, concurrency and state, install scripts, the release pipeline, code
health.

## 2. Environment

- `go version go1.26.4 windows/amd64`; `node v24.14.1`; go.mod declares `go 1.23`.
- `staticcheck`: not installed. `govulncheck`: installed (`~/go/bin`). `gosec`: installed.
- The live machine carries the PATH accumulation described in the brief:
  sixteen `\nvxNNNNNNNN\bin` entries in `HKCU\Environment\Path` (see finding H5).

## 3. What I ran (verbatim, informative lines)

```
$ go build ./...
exit=0

$ go vet ./...
exit=0

$ gofmt -l .
exit=0

$ go test -count=1 ./...
?   	github.com/fstubner/nvx/cmd/nvx	[no test files]
ok  	github.com/fstubner/nvx/internal/nvx	52.943s
[exited with code 0]

$ staticcheck ./...
staticcheck: not installed

$ govulncheck ./...
=== Symbol Results ===
Vulnerability #1: GO-2026-6218  net/url resolvePath quadratic complexity
    Found in: net/url@go1.26.4  Fixed in: net/url@go1.26.6
      internal/nvx/download.go:54:37: nvx.DownloadFile calls http.Client.Do -> url.URL.Parse
Vulnerability #2: GO-2026-6090  crypto/tls post-handshake message limit
    Found in: crypto/tls@go1.26.4  Fixed in: crypto/tls@go1.26.6
Vulnerability #3: GO-2026-5972  encoding/asn1 recursion depth
    Found in: encoding/asn1@go1.26.4  Fixed in: encoding/asn1@go1.26.6
Vulnerability #4: GO-2026-5856  crypto/tls ECH privacy leak
    Found in: crypto/tls@go1.26.4  Fixed in: crypto/tls@go1.26.5
Vulnerability #5: GO-2026-5026  x/net/idna ASCII punycode
    Found in: net/http@go1.26.4  Fixed in: net/http@go1.26.6
Your code is affected by 5 vulnerabilities from the Go standard library.
(all are stdlib; go.sum is empty, no third-party module is called)

$ cd site && npm run build
... ✓ Completed in 1.38s.
[starlight:pagefind] Building search index with Pagefind...
[starlight:pagefind] Found 6 HTML files.
thread 'main' (46292) panicked at pagefind\src\output\mod.rs:422:46:
called `Result::unwrap()` on an `Err` value: Os { code: 5, kind: PermissionDenied, "Access is denied." }
exit=0     # astro reports success; pagefind search index panicked (see note)

$ bash scripts/release/lib_test.sh
  ok   - retry path engaged (the condition that triggered the original bug)
  ok   - verified_sha output is exactly one 64-char hex digest
  ok   - rejects 'v0.6.0; rm -rf /'
  ... all lib.sh tests passed
exit=0
```

Note on the site build: astro exits 0, but the pagefind step panics with
`PermissionDenied` writing the search index. It reproduced whether run inside or
outside the tool sandbox. Site is out of scope beyond "builds"; the astro build
itself completes, so this is recorded, not scored.

## 4. Findings table (by severity)

| # | Sev | Area | One line | Evidence |
|---|-----|------|----------|----------|
| C1 | Critical | Supply-chain | `npm install` from any project SUBDIRECTORY runs every pre-install check against an empty package list, so typosquat/OSV/release-age/blocklist all silently pass | e2e run below; env.go:1656, env.go:643 |
| H1 | High | Egress / loopback | `0.0.0.0` (and `::`) bypass the deliberate loopback-prompt refusal and dial 127.0.0.1 loopback services | probe P2/P8; egress_proxy.go:388, egress_resolve.go:129 |
| H2 | High | Supply-chain | yarn / pnpm / bun installs verify only package.json direct deps, with no versions, so OSV never runs on them and transitive deps are unchecked | probe P4; env.go:1042-1057 |
| H3 | High | macOS sandbox | Seatbelt allows `file-read*`, so a contained install reads `~/.ssh`, `~/.aws`, `~/.npmrc`, `~/.config/gh` by absolute path; no deny-list follows the broad allow | sandbox_seatbelt.go:192; documented, but see feasibility note |
| M1 | Medium | Policy engine | An enforced org baseline does not stop a project file pinning an older/known-vulnerable runtime version | probe P5; policy.go:996-1003 |
| M2 | Medium | Write scope | On macOS/Linux the working directory becomes a writable root with no guard against it being `$HOME`; Windows guards this, the shared declaration does not | probe P1; sandbox_write_scope.go:145, sandbox.go |
| M3 | Medium | Install scripts | On Windows, `nvx doctor --fix` / installer PATH repair only removes the CURRENT home's nvx dirs; foreign `\nvxNNN\bin` entries persist forever | probe P6; doctor.go:429, live registry |
| M4 | Medium | Egress env | `NODE_OPTIONS`, `NODE_EXTRA_CA_CERTS`, `NPM_CONFIG_*` and per-registry auth env are not in `sensitiveEnvPrefixes`, so a project `allow` list can pass them into contained code | probe P7; sandbox.go:46-68 |
| M5 | Medium | Release/build | Local/dev builds carry 5 known stdlib CVEs (go1.26.4); go.mod pins only `go 1.23`; only release.yml's `1.26.6` clears them | govulncheck; go.mod:3, release.yml:24 |
| L1 | Low | Error classify | `runInstall` classifies a version-not-found by `strings.Contains(err.Error(),"no release found")` | main.go:914 |
| L2 | Low | Build hygiene | Built `nvx`/`nvx.exe` binaries sit in the repo root (gitignored, not tracked) | git ls-files |
| I1 | Info | Positive | Two-read TOCTOU on policy files is closed; hash and parse come from one read | policy.go:756 |
| I2 | Info | Positive | Egress proxy authenticates every client with a per-session token, constant-time compared | egress_proxy.go:526-637 |
| I3 | Info | Positive | Fail-closed discipline is real and tested across the tree (relay, seccomp, seatbelt-absent, supervisor) | many |

## 5. Findings in detail

### C1 (Critical) — pre-install checks verify nothing when run from a subdirectory

`detectShimPackagesForVerification` → `packagesFromPackageLock` / `packagesFromPackageJSON`
(env.go:1042-1057, 1656) read `package-lock.json` and `package.json` from the
process's current directory only (`os.ReadFile("package-lock.json")`,
env.go:643). npm itself walks UP to find the nearest manifest and will install
from a subdirectory, but nvx reads only `./`. So `cd src/deep && nvx npm install`
resolves zero packages, and `runVerifyInstall` is handed an empty list, so the
blocklist, typosquat, OSV and release-age gates all pass by having nothing to
check.

Reproduced end to end with the real 0.6.0 binary (NVX_YES=1, --no-sandbox to keep
the run visible, --dry-run so nothing installs):

```
===== from project root
ℹ Verifying package "left-pad"...
ℹ Scanning OSV database for known vulnerabilities...
ℹ Vulnerability scan clean. No active CVEs found.
ℹ Running directly (not sandboxed): npm install --dry-run
add left-pad 1.3.0
===== from src/deep      (a subdirectory of the same project)
ℹ Running directly (not sandboxed): npm install --dry-run
add left-pad 1.3.0
```

From the subdirectory there is no "Verifying package" and no OSV scan. The install
proceeds. This is the product's headline supply-chain guarantee silently absent
for a completely ordinary invocation. `npm ci` from a subdirectory has the same
hole (probe P3: both `install` and `ci` returned `[]`). Sandboxing itself is
unaffected — the command is still contained — but the pre-install checks are the
layer that is meant to stop a malicious package before its install script ever
runs contained. Rated Critical: a security check is silently disabled by normal
use, matching the brief's Critical definition.

Fix direction: resolve the manifest/lockfile from `findProjectRoot(cwd)` (already
used elsewhere in the same function's callers), not from `.`.

### H1 (High) — `0.0.0.0` / `::` bypass the loopback-prompt refusal

`EgressProxy.allowed` (egress_proxy.go:388) refuses to offer a loopback
destination at the prompt, on purpose: untrusted contained code must not be able
to ask the developer for access to their local Postgres. The refusal fires when
`isLoopback(hp.host) || anyLoopback(ips)`. But `isLoopback` (egress_proxy.go:447)
matches only literal loopback spellings, and `anyLoopback` (egress_resolve.go:129)
checks `ip.IsLoopback()` — and `0.0.0.0` / `::` are the *unspecified* address, not
loopback, so both return false. The request then falls through to the ordinary
unknown-host prompt. Meanwhile `dialVetted` connects to `0.0.0.0:<port>`, which
the OS routes to `127.0.0.1`.

Probe P2/P8 output:

```
host=0.0.0.0    validEgressHost=true  isLoopback=false anyLoopback=false
host=::         validEgressHost=true  isLoopback=false anyLoopback=false
dialVetted(0.0.0.0:58221) connected to the 127.0.0.1 listener and read "hello-from-loopback"

allowed(127.0.0.1:5432) -> "Blocked egress to a local service ... nvx does not offer local services through a prompt"
allowed(0.0.0.0:5432)   -> "Allow outbound connection to 0.0.0.0:5432 for the rest of this run?"   (prompt path)
```

So a contained postinstall that CONNECTs `0.0.0.0:5432` reaches the developer's
local Postgres if the prompt is answered yes (or `NVX_TRUST_YES` is set), the exact
outcome the loopback refusal exists to prevent. Non-interactive denies, so the
window is an interactive developer approving a prompt that reads as an unfamiliar
external host rather than "your local database". High: it defeats a deliberate
containment control the code goes to real lengths to enforce, and `0.0.0.0` binds
every local service. Fix: treat unspecified addresses (`ip.IsUnspecified()`) as
loopback-equivalent in the refusal, on both the text and the resolved-IP checks.

### H2 (High) — yarn / pnpm / bun installs verify a fraction of the tree, with no versions

`detectShimPackagesForVerification` (env.go:1042) only reads a lockfile for `npm`
(`packagesFromPackageLock`). For a bare `pnpm install` / `yarn install` /
`bun install` it falls back to `packagesFromPackageJSON`, which returns direct
dependency NAMES with no versions (env.go:850-880). Probe P4:

```
pnpm install -> ["left-pad"]
yarn install -> ["left-pad"]
bun install  -> ["left-pad"]
```

Two consequences: (1) transitive dependencies — where supply-chain compromises
actually land — are never scanned, because `pnpm-lock.yaml` / `yarn.lock` /
`bun.lockb` are not parsed; (2) OSV cannot run at all, because
`resolveVersionQuery` is handed a bare name and the query needs an exact version,
so the release-age and OSV checks silently do nothing for pnpm/yarn/bun installs.
The npm lockfile parser was fixed on 2026-09-16 (the v2/v3 `packages` shape); the
sibling lockfiles were never added. High: pnpm and yarn are mainstream, and this
is the supply-chain scan reduced to direct-dep names for the majority of the
ecosystem.

### H3 (High, known) — macOS reads credential files; a targeted deny-list is feasible

`buildSeatbeltProfile` emits `(allow file-read*)` (sandbox_seatbelt.go:192) so the
dynamic linker can reach the dyld cache. SECURITY.md and enforcement-matrix note 2
both disclose that credential reads are not contained on macOS. This is real: a
contained install reads `~/.ssh/id_rsa`, `~/.aws/credentials`, `~/.npmrc`,
`~/.config/gh/hosts.yml` by absolute path.

Per the brief, the question is feasibility of a deny-list after the broad allow.
Seatbelt supports later rules overriding earlier ones, so appending explicit
`(deny file-read* (subpath "..."))` after the broad `file-read*` is a supported
construction — the profile already relies on rule ordering for the network
grants. Candidate paths: `~/.ssh`, `~/.aws`, `~/.npmrc`, `~/.config/gh`,
`~/.config/gcloud`, `~/.docker/config.json`, `~/.kube`, `~/.netrc`. The cost is a
curated deny-list that is incomplete by construction (the same tradeoff
`notableEnvKeys` already accepts), and one risk: a deny on a path the linker or a
legitimate tool needs would break launch, so it must be scoped to credential
directories, not broad prefixes. It closes the largest reads and is far better
than the current all-or-nothing. Recommend it as a real mitigation rather than
re-reporting the gap. This cannot be exercised here (no Mac); the profile TEXT is
what would be asserted, which the existing profile tests already do on Windows/Linux.

### M1 (Medium) — enforced baseline does not pin the runtime version

`policyLoosenings` (policy.go:1015) deliberately omits runtime version, documented
at policy.go:996-1003 as "Runtime pins which version is used. It grants no access
and restricts none." Under a non-enforced baseline that is defensible. Under an
ENFORCED org baseline it is a gap: a project `.nvx-policy.json` can pin an older,
known-vulnerable runtime and `MergeUnderBaseline` accepts it. Probe P5:

```
runtime pin 22 -> 16.0.0 under enforced baseline: err=<nil> merged.node=16.0.0 cmd="node"
```

An organisation that sets `"enforced": true` to hold a floor cannot hold a runtime
floor. Medium: it is a policy-completeness gap in a new feature, not an escape;
the fix is to treat a runtime downgrade as a loosening when (and only when) a
baseline is enforced, or to document that runtime is out of the baseline's remit.
The identical/trailing-dot `default_allow` restatement and `isolation.enabled:false`
were both handled correctly (P5: the enabled=false case is refused).

### M2 (Medium) — no home-directory guard on the writable working root (macOS/Linux)

`sandboxWritableRoots` (sandbox_write_scope.go:145) returns `[guestHome, workDir]`
with no check that `workDir` is the user's home. On Windows `prepareAppContainerFilesystem`
has `isProfileRoot` (sandbox_appcontainer_windows.go:100, 305) and treats the
profile root specially; the shared declaration and the Landlock/Seatbelt readers
have no equivalent. Probe P1, with HOME as the working directory:

```
sandboxWritableRoots(guest, HOME) = ["/Users/felix/.nvx/sandbox_home/abc" "/Users/felix"]
seatbelt: (subpath "/Users/felix")            <- HOME granted file-write*
```

So `cd ~ && nvx npm install` on macOS/Linux makes the entire home directory a
writable root for the contained process. A stray `package.json` in `$HOME` (the
SECURITY.md "collapsed scope" case) makes this the ordinary outcome. Contained
code cannot read most of home on Linux (Landlock denies it) but it CAN write
whatever the workDir grant covers, which here is all of `$HOME`. Medium: it
requires the user to run an install from their home directory, and Linux read
containment still holds, but a write root of `$HOME` lets a postinstall drop a
`.bashrc`, an `~/.ssh/authorized_keys`, or a shim on PATH. Fix: refuse (or narrow)
a working root that is the real home, matching the Windows guard.

### M3 (Medium) — PATH repair leaves other nvx installs' dead dirs forever

`rebuildUserPath` (doctor.go:429) drops only entries equal to or within
`nvxRuntimeDirs(nvxHome)` — the CURRENT home's runtime dirs. A `\nvxNNN\bin` entry
from a different (temp) NVX_HOME is not matched, so it survives every `doctor --fix`.
Probe P6 against a representative PATH:

```
before: 2 dead entries; after doctor rebuild: 2 dead entries
CleanAndBuildPath keeps 2 dead entries
underTempDir("\nvx143587690\bin") = false
```

This is the live machine's condition: sixteen `\nvxNNN\bin` entries in
`HKCU\Environment\Path` (confirmed by `reg query`). The brief's observation is
reproduced: session env does not accumulate these (`CleanAndBuildPath` and
`emitSessionEnv` replace, they do not append — the sixteen are all in the PERSISTENT
User PATH, not re-added per session), so the accumulation source is many past
`doctor --fix` / installer runs under throwaway NVX_HOMEs, each of which wrote its
own shim dir and none of which is cleaned by a later run under a different home.
`underTempDir` returns false for a bare `\nvxNNN\bin` (no drive, so it fails the
temp-prefix test), which is why the temp guard did not stop them being written in
the first place. Medium: these are dead relative-path entries (search-path
weirdness, minor attack surface — a relative PATH entry resolves against the
process cwd), not a live escape, but they accumulate without bound. Fix: recognise
and prune any `nvx`-shim directory (the `directoryHoldsNvxShims` predicate already
exists in env.go:290) rather than only the configured home's.

### M4 (Medium) — the env scrub allowlist leaks tool-influencing and auth-adjacent variables

`sensitiveEnvPrefixes` (sandbox.go:46-68) covers cloud and VCS token families but
not several variables a project `isolation.environment.allow` list can pass
straight into contained install code. Probe P7:

```
NODE_OPTIONS               refused=false
NODE_EXTRA_CA_CERTS        refused=false
NPM_CONFIG_USERCONFIG      refused=false
NPM_CONFIG__AUTH           refused=false
npm_config_//registry...:_authToken  refused=false
CLOUDFLARE_API_TOKEN       refused=false
VERCEL_TOKEN               refused=false
NETLIFY_AUTH_TOKEN         refused=false
DATABASE_URL               refused=false
```

`allow` requires a trust prompt to take effect, which is the mitigating control,
and the sandbox drops these by default (they are only reachable if a project names
them). But the refusal list is meant to be the backstop that a checked-in file
cannot pass a credential, and it misses whole families: `NPM_CONFIG_*` /
`npm_config_*` include registry auth tokens, `CLOUDFLARE_/VERCEL_/NETLIFY_` are
provider tokens, `NODE_OPTIONS` and `NODE_EXTRA_CA_CERTS` change how contained
node behaves (a `--require` preload, a trusted CA). Medium: the trust prompt gates
it, so this is defense-in-depth rather than a direct leak. Fix: extend
`sensitiveEnvPrefixes` with `NPM_CONFIG`, `NPM_`, provider token prefixes, and
consider refusing `NODE_OPTIONS`/`NODE_EXTRA_CA_CERTS` from `allow`.

### M5 (Medium) — dev builds ship 5 known stdlib CVEs; go.mod floor is far below the build toolchain

`govulncheck` reports five stdlib vulnerabilities reachable from `DownloadFile`,
`ScanVulnerabilitiesBatch`, `ResolveNpmPackageDetails` and `LogError`, all fixed
in go1.26.5/1.26.6. The local build uses go1.26.4; `go.mod:3` declares only
`go 1.23`. `release.yml:24` pins `go-version: '1.26.6'`, which clears all five for
the SHIPPED artifact — so users are not exposed if every release is built by that
workflow. Medium because the toolchain floor and the build reality are three minor
versions apart and nothing enforces the release toolchain locally; a hand-built
binary (the `UseLocalBinary` / `NVX_USE_LOCAL_BINARY` install paths) inherits the
developer's Go. Fix: bump the `go` directive / add a `toolchain` line so a local
build cannot silently use an affected stdlib.

### L1 (Low) — one surviving error-classification-by-string

The `isUnsupportedRange` rework (semver_range.go:322-339) is now sentinel-based
via `errors.Is`, correct. The remaining string-classification in the tree is
`main.go:914`:

```go
if strings.Contains(err.Error(), "no release found") {
```

Low impact: it only decides whether to print an extra hint after an install
failure; a message change degrades the hint, not correctness. The other two
`err.Error()` string matches (sandbox_appcontainer_launch_windows.go:107, the
`cannot find the file` cmd.exe fallback) are Windows launch-retry heuristics with
a documented rationale, `%w`-wrapped elsewhere; acceptable but worth a note that a
localized Windows would not match `"cannot find the file"` (the launch code
already comments this for the corrupt-image path but not here).

### L2 (Low) — built binaries in the repo root

`nvx` (10.9 MB) and `nvx.exe` (11.4 MB) exist at the repo root. Both are gitignored
(`.gitignore:4,6`) and `git ls-files` confirms neither is tracked, so this is
local build litter, not a shipped artifact. Noted only so a future `git add -A`
cannot sweep an 11 MB binary in.

### Positives (Info)

- **I1**: the policy-file TOCTOU is closed — `readAndHashProjectPolicyFile`
  (policy.go:756) reads once and both parses and hashes the same bytes, so
  contained code that can write the working directory cannot have one version
  hashed and another parsed. This is exactly the class the brief flagged.
- **I2**: the egress proxy requires a per-session 128-bit token on both HTTP
  (egress_proxy.go:526) and SOCKS (egress_proxy.go:563-607), constant-time
  compared, closing the sibling-borrows-the-allowlist hole. The CONNECT header
  read is bounded (`maxProxyRequestHeaderBytes`, egress_proxy.go:29) then lifted
  for the tunnel — correct.
- **I3**: fail-closed is consistently implemented and, more importantly, tested at
  the executed layer: the Linux egress relay, seccomp filter, seatbelt-absent
  refusal and Windows supervisor all refuse rather than fall through, and
  `seccompFilterForMode` / `networkModeRequiresNamespace` were hardened against
  the trailing-space fall-through the brief worried about (both `TrimSpace` now,
  sandbox_network_seccomp_linux.go:66, sandbox_network_linux.go:227).
- The `bin-resolve` cache is defended against a contained macOS process forging an
  entry: `cachedPathIsResolvable` (bin_cache.go:76) re-derives the entry's
  directory must be on PATH, not trusting the PathHash. Good, though it has NO
  direct test (see Coverage Gaps).

## 6. Unconfirmed (suspected, not proven here)

- **U1 — Windows AppContainer inbound handle inheritance beyond the three stdio
  handles.** `launchAppContainerProcessOnce` pins inheritance to a handle list
  (sandbox_appcontainer_launch_windows.go:190) and documents a measured case where
  an unrelated inheritable pipe from the launching agent crossed into the
  container when the list was absent. The code path looks correct, but I cannot
  drive a real AppContainer launch here to confirm the handle list is always
  populated (`attrCount==2`) when stdio is inheritable. Proof would be the probe
  in `sandbox_fd_inject_crt_probe_windows_test.go` run on a machine, checking the
  contained process's handle table.
- **U2 — pnpm/yarn transitive verification (H2) severity in `--frozen-lockfile`
  installs.** H2 is proven for a bare install. Whether a CI `pnpm install
  --frozen-lockfile` is any better is unconfirmed; the code path is the same
  `packagesFromPackageJSON` fallback, so I expect not, but did not drive pnpm.
- **U3 — the loopback-prompt bypass (H1) reaching a REAL contained process.** H1 is
  proven at the `allowed()`/`dialVetted` layer. End-to-end, a contained process on
  Windows reaches the proxy over the relay; I did not stand up a real contained
  postinstall issuing `CONNECT 0.0.0.0:5432` through it. The proxy decision is the
  security boundary and it is proven bypassable; the plumbing to it is well-tested
  elsewhere, so I rate H1 High on the unit evidence.
- **U4 — macOS deny-list (H3) actually loading.** The feasibility argument is
  sound but a `(deny file-read*)` appended after `(allow file-read*)` must be
  confirmed on real macOS not to break dyld; unprovable here.

## 7. Strengths

- The write-containment declaration is genuinely single-sourced
  (`sandboxWritableRoots`) and Windows fails the launch closed if it names a root
  it cannot implement (sandbox_appcontainer_windows.go:77-82) — a real guard
  against the two-callers-disagree class.
- The egress resolve/dial path resolves once and dials the vetted IPs
  (egress_resolve.go:99-186), closing DNS-rebinding between check and dial; the
  link-local (metadata endpoint) refusal is present and correct.
- Concurrency on the grants ledger is serialised under a cross-process file lock
  (`updateProjectGrants`, policy_persist_update.go:366) with atomic temp+rename
  saves and unreadable-file quarantine — the "permission with no record" hazard is
  taken seriously.
- Audit-log rotation is a single atomic rename (run_trace.go:364) and every logged
  field is flattened to defend against a contained process forging a row via a
  newline in a hostname (audit.go:154).
- The trust-boundary prompt correctly refuses `-y`/`--agent-mode`/`NVX_YES` and
  only honours `NVX_TRUST_YES` (main.go:1496), and a bare Enter denies
  (promptAnswerApproves, main.go:1609) — both are the safe direction and both are
  tested.
- Release pipeline: `verified_sha` (scripts/release/lib.sh:77) downloads and
  re-hashes the asset rather than trusting the sidecar; the tag is validated
  before interpolation; the winget version is asserted `MAJOR.MINOR.PATCH`. The
  publish/preflight split is sensible and the npm publish uses os/cpu
  optionalDependencies rather than a postinstall download.
- CHANGELOG/release cadence honoured: last tag `v0.6.0` dated 2026-09-09 per its
  commit; nothing here proposes a release.

## 8. Priority order for the maintainer

1. **C1** — resolve manifest/lockfile from the project root, not cwd. One-function
   fix, restores the product's headline guarantee for subdirectory installs.
2. **H2** — parse `pnpm-lock.yaml` / `yarn.lock` (and bun) or, at minimum, run OSV
   against resolved versions from them; otherwise state plainly that non-npm
   installs are name-only.
3. **H1** — treat `ip.IsUnspecified()` as loopback-equivalent in the prompt
   refusal (both text and resolved-IP checks).
4. **H3** — add the macOS credential deny-list after the broad `file-read*`.
5. **M2** — refuse/narrow a working root equal to the real home on macOS/Linux.
6. **M4/M1/M3/M5** — env-scrub prefix additions; runtime-floor under enforced
   baseline; prune foreign nvx shim dirs; raise the go.mod toolchain floor.

## 9. Coverage gaps

- **Platforms not exercisable here.** The Linux (Landlock + netns + seccomp) and
  macOS (Seatbelt) sandboxes cannot be run on this Windows host. Everything about
  them was read and reasoned about; the profile/ruleset TEXT was probed
  (buildSeatbeltProfile, landlockReadOnlyRules) but kernel enforcement was not
  observed. H3's deny-list feasibility (U4) and the exact behaviour of the Linux
  loopback iptables redirect are read-only conclusions.
- **Real AppContainer launch not driven.** The ~20 `sandbox_*_probe_windows_test.go`
  files are `NVX_PROBE`-gated and the brief says they are run by hand before a
  release; I ran the ordinary `go test` suite (green, 52.9s) but did not set
  `NVX_PROBE=1`, so the actual containment probes (handle inheritance U1,
  named-pipe broker, cross-project refusal) were not observed this pass — they are
  the evidence for several enforcement-matrix rows.
- **Functions with NO direct test** (from a name scan of `*_test.go`): `MergeUnderBaseline`
  (0 — only reached indirectly via policy_baseline_test.go, which exercises the
  behaviour but not the function name), `cachedPathIsResolvable` (0 — the forged-entry
  defense of I2, tested only through `lookupBinCache`), `refusedPassEnv` (0),
  `anyLoopback` (0 — part of the H1 gap), `expandPathVars` (0 — the `%VAR%`
  self-reference loop guard), `isProfileRoot` (0 — the Windows home guard M2 relies
  on), `isCreateProcessMissingFile` (0), `ScanVulnerabilitiesBatch` (0 — only the
  short-answer and pagination cases via security_degradation_test.go),
  `findInstallVerbIndex` (0 direct — behaviour covered via callers),
  `bareFileIsNvxPolicy` (0 direct — via policy_bare_filename_test.go),
  `proxyCredentialFromEnv` (0), `fetchExpectedShasum` (0). Most have indirect
  coverage; `anyLoopback` and `cachedPathIsResolvable` are the security-critical
  ones whose direct behaviour I would want pinned.
- **Not read in depth.** The large Windows stdio broker
  (sandbox_stdio_broker_windows.go, 496 lines) and the many `_probe_windows_test.go`
  files were skimmed for structure, not line-audited. `main.go` (1834 lines) was
  read for the security-relevant paths (prompts, verify, runAuto, emitSessionEnv);
  its command-dispatch surface (`Main`, help text) was not audited for injection.
- **Biggest single gap:** the whole macOS and Linux enforcement story is
  believed-and-read, not observed, on this host — so H3's fix and every "Yes⁵/Yes⁸"
  cell in the enforcement matrix rests on CI and prior measurement, which I could
  not independently reproduce.
