# Packaging

Manifest templates and submission notes for getting nvx into the OS package
managers.

`.github/workflows/publish.yml` automates the live updates once you publish a
GitHub release. The files here are the reviewed source of those
manifests, so they should stay accurate enough to review.

The publish jobs download each release asset, re-hash the bytes, and check
the result against the uploaded `.sha256` sidecar before pushing anything
downstream. A sidecar that disagrees with its asset fails the publish instead
of propagating. The npm, Homebrew, Scoop and Winget jobs then check the asset's
build provenance from `release.yml` too (`verified_sha` in
`scripts/release/lib.sh`). The Winget job then checks that the manifests Komac
writes carry the digest it verified, and sends those same files.

That ordering is the point, and it is easy to get backwards. Reading the
hash out of the sidecar and writing it into a manifest verifies nothing. The
sidecar comes from the same origin as the asset, so it can only ever detect
corruption in transit, never a substituted artifact. The sidecar is the
claim. The bytes are the evidence.

## npm lives in `npm/`

publish.yml's npm job publishes `@fstubner/nvx` and its five per-platform
packages with `scripts/release/publish-npm.sh`. Their `package.json` files
are in `npm/` at the repository root instead of in this directory.

## How the release pipeline feeds these

Pushing a `v*` tag triggers `.github/workflows/release.yml`, which:

1. Waits for CI to pass on that commit.
2. Builds five binaries with `go build -trimpath` and CGO off, named
   `nvx-linux-amd64`, `nvx-linux-arm64`, `nvx-darwin-amd64`,
   `nvx-darwin-arm64` and `nvx.exe`.
3. Writes a `.sha256` next to each one and a combined `SHASUMS256.txt`.
4. Generates a build provenance attestation for all five binaries via
   `actions/attest-build-provenance`.
5. Publishes the release as a **draft** with every asset attached.

Publishing that draft by hand is what fires publish.yml. By then the assets
exist. That is why the manifests can reference the release asset URL and
the SHA256 directly.

The provenance attestation lets anyone check that a binary came from this
repo's release workflow without trusting GitHub's asset storage:

```bash
gh attestation verify nvx-linux-amd64 --repo fstubner/nvx --signer-workflow fstubner/nvx/.github/workflows/release.yml
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
| Homebrew formula | Installs a prebuilt binary instead of building from source, and one formula covers four platforms. | `brew audit --strict --online`; `brew install --formula`; `brew test nvx` |
| Scoop | The asset is already named `nvx.exe`, so the manifest needs no `#/nvx.exe` rename fragment. | `scoop install`; `nvx --version`; `scoop update` |
| Winget | The asset is a bare executable, not an installer, so the manifest must stay `InstallerType: portable`. | `winget validate`; install from the generated PR manifest |

macOS notarization is not solved by these manifests. macOS users will hit
Gatekeeper on an unsigned binary. That is separate release-trust work.

## Release checklist

The order matters. `CHANGELOG.md` has to name the version before the tag, and has
to leave it undated until the tag exists.

1. On a branch, make the release commit. In `CHANGELOG.md`, rename
   `## [Unreleased]` to `## [X.Y.Z]` with **no date**, and put a new empty
   `## [Unreleased]` above it. In the same commit, set `appVersion` in
   `internal/nvx/version.go` and `productVersion` in
   `site/src/data/site-content/version.ts` to `X.Y.Z`.
   `TestAppVersionMatchesNewestChangelogEntry` fails unless `appVersion` equals
   the newest heading.
2. Open the pull request and merge it when CI is green. Do not date the heading
   yet. Site CI's changelog check (`site/scripts/changelog-dates.mjs`) fails a
   dated heading whose `vX.Y.Z` tag does not exist, and says "The date goes on
   with the tag, not with the version bump."
3. Tag the merge commit `vX.Y.Z` and push the tag. `release.yml` checks that the
   commit is on main and waits up to 20 minutes for the newest `ci.yml` run of
   that commit to pass. A newest run that failed or was cancelled stops the
   release, and an older one does not. Then it builds, signs and attests the
   binaries and leaves a **draft** release.
4. Run the `Publish preflight` workflow. It checks the three publishing
   credentials (Homebrew, Scoop and Winget) and publishes nothing. npm needs
   none, because it publishes through trusted publishing.
5. Publish the GitHub release draft for `vX.Y.Z`. That fires `publish.yml`.
6. Confirm publish.yml's summary job reports all four registries as
   `success`, and re-run any single job that did not.
7. Watch for Winget moderator comments on the PR. Homebrew and Scoop land
   without review, Winget does not. Until `fstubner.nvx` has merged into
   winget-pkgs once, the Winget job stops with a message that says so. Submit
   that first version by hand ([`winget/`](./winget/)).
8. Run the platform-specific checks above for any target the release touched.
9. Date the heading in a second pull request, as `## [X.Y.Z] - YYYY-MM-DD` with
   the day the release was published. Site CI passes now, because the `vX.Y.Z`
   tag exists and the job checks out with the tags.
