# Packaging

Manifest templates and submission notes for getting nvx into the OS package
managers.

The live updates are automated from `.github/workflows/publish.yml` once a
GitHub release is published. The files here are the reviewed source of those
manifests, so they should stay accurate enough to review.

The publish jobs download each release asset, re-hash the bytes, and check
the result against the uploaded `.sha256` sidecar before pushing anything
downstream. A sidecar that disagrees with its asset fails the publish rather
than propagating.

That ordering is the point, and it is easy to get backwards. Reading the
hash out of the sidecar and writing it into a manifest verifies nothing. The
sidecar comes from the same origin as the asset, so it can only ever detect
corruption in transit, never a substituted artifact. The sidecar is the
claim. The bytes are the evidence.

## npm is not here

`.github/workflows/release.yml` publishes `@fstubner/nvx` and its five
per-platform packages as part of the build, because the wrapper ships the
binaries that build has just produced. Nothing in this directory touches
npm, and publish.yml has no npm job.

## How the release pipeline feeds these

Pushing a `v*` tag triggers `.github/workflows/release.yml`, which:

1. Waits for CI to pass on that commit.
2. Builds five binaries with `go build -trimpath`, CGO off:
   `nvx-linux-amd64`, `nvx-linux-arm64`, `nvx-darwin-amd64`,
   `nvx-darwin-arm64`, `nvx.exe`.
3. Writes a `.sha256` next to each one plus a combined `SHASUMS256.txt`.
4. Generates a build provenance attestation for all five binaries via
   `actions/attest-build-provenance`.
5. Publishes the release as a **draft** with every asset attached.

Publishing that draft by hand is what fires publish.yml. By then the assets
exist, which is why the manifests can reference the release asset URL plus
the SHA256 directly.

The provenance attestation lets anyone check that a binary came from this
repo's release workflow without trusting GitHub's asset storage:

```bash
gh attestation verify nvx-linux-amd64 --repo fstubner/nvx
```

## Submission targets

| Registry | Dir | Publish path |
|---|---|---|
| Homebrew tap | [`homebrew/`](./homebrew/) | `scripts/release/publish-homebrew.sh` |
| Scoop bucket | [`scoop/`](./scoop/) | `scripts/release/publish-scoop.sh` |
| Winget (microsoft/winget-pkgs) | [`winget/`](./winget/) | `publish.yml` Winget job |

Deliberately absent:

| Not published | Why |
|---|---|
| crates.io | nvx is Go. crates.io hosts Rust crates. |
| AUR | Deferred. It needs an SSH key and has no review step, so a bad push is live immediately. |
| Homebrew Cask | Casks are for macOS applications. nvx is a CLI, and the formula covers macOS. |

`fstubner/homebrew-tap` and `fstubner/scoop-bucket` both also carry
netscli's manifests. Each publish script writes only its own file, so the
projects share the repos without touching each other's manifests.

## Platform-specific checks

These are the parts that need more than "URL and SHA256 changed":

| Target | Nuance | Validate with |
|---|---|---|
| Homebrew formula | Installs a prebuilt binary rather than building from source, and one formula covers four platforms. | `brew audit --strict --online`; `brew install --formula`; `brew test nvx` |
| Scoop | The asset is already named `nvx.exe`, so the manifest needs no `#/nvx.exe` rename fragment. | `scoop install`; `nvx --version`; `scoop update` |
| Winget | The asset is a bare executable, not an installer, so the manifest must stay `InstallerType: portable`. | `winget validate`; install from the generated PR manifest |

Windows Authenticode signing and macOS notarization are not solved by these
manifests. macOS users will hit Gatekeeper on an unsigned binary. Both are
separate release-trust work.

## Release-day checklist

1. Run the `Publish preflight` workflow. It checks the three publishing
   credentials and publishes nothing.
2. Publish the GitHub release draft for `vX.Y.Z`.
3. Confirm publish.yml's summary job reports all three registries as
   `success`, and re-run any single job that did not.
4. Watch for Winget moderator comments on the PR. Homebrew and Scoop land
   without review, Winget does not.
5. Run the platform-specific checks above for any target the release
   touched.
