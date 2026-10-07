# Winget (microsoft/winget-pkgs)

## Submission flow

Unlike Homebrew and Scoop, which use a tap and a bucket Felix controls,
Winget packages all go to the central
[microsoft/winget-pkgs](https://github.com/microsoft/winget-pkgs) repo by
PR. A moderator reviews every version.

Once accepted, users install with:

```powershell
winget install fstubner.nvx  # canonical package id
```

The short `winget install nvx` form depends on the accepted manifest
publishing `Moniker: nvx`. Nobody has submitted anything yet, so treat the
canonical id as the only form you know works. Check the catalog
before documenting the short one anywhere user-facing.

The package is **portable**. The release asset is a single `nvx.exe`, not an
installer EXE, so the manifest must use:

```yaml
InstallerType: portable
Commands:
  - nvx
```

## The first submission

`publish.yml`'s Winget job downloads a pinned release of
[Komac](https://github.com/russellbanks/Komac), checks it against its SHA-256 and
runs `komac update`, which adds a version to a package that is already in
winget-pkgs. So you have to submit the FIRST version by hand with
`wingetcreate`:

```powershell
winget install Microsoft.WingetCreate

wingetcreate new `
  --urls https://github.com/fstubner/nvx/releases/download/vX.Y.Z/nvx.exe `
  --version X.Y.Z `
  fstubner.nvx
```

That opens an editor with the three generated YAML files. Verify:

- `PackageIdentifier: fstubner.nvx`
- `Moniker: nvx`
- `InstallerType: portable`
- `Commands: [nvx]`
- `PackageVersion` equals `X.Y.Z` with no leading `v`, see below
- `InstallerSha256`, compared against the `.sha256` on the release page
- `License: MIT`
- `PackageUrl: https://nvx.run`
- `ShortDescription: A Node.js and Bun version manager that runs every install inside an OS sandbox.`

Then:

```powershell
wingetcreate submit --token <gh-token> manifests/f/fstubner/nvx/X.Y.Z
```

This forks microsoft/winget-pkgs, pushes the manifests, and opens the PR.
Every release after that is automatic.

## What the job checks before it submits

Komac downloads `nvx.exe` from the release and writes the hash of what it got
into the manifest, so a binary replaced on the release page would otherwise
reach winget-pkgs under its own hash. The job therefore:

1. Verifies `nvx.exe` with `verified_sha` in `scripts/release/lib.sh`, as the
   other three jobs do. That checks the sidecar against the bytes and the
   bytes against the build provenance `release.yml` attested, and prints the
   digest.
2. Runs `komac update --dry-run --output <dir>`, which writes the manifests it
   would send and sends nothing, and fails unless they carry that digest
   (`scripts/release/check-winget-sha.ps1`).
3. Runs the real `komac update --submit --output <dir>`. Komac downloads the
   file again for that, so the manifest it sends is checked against the same
   digest once it is done. A file replaced between the two runs fails the job,
   with the pull request already open for someone to close.

## PackageVersion carries no `v`

Winget sorts versions naturally, so `v0.6.1` and `0.6.0` do not compare as
an upgrade. Nothing downstream catches this, because winget-pkgs accepts a leading
`v` without complaint, and a merged manifest is permanent. netscli published
five CLI versions that way before anyone noticed.

`publish.yml` strips the `v` and then asserts on the result, which is why
the assertion is there instead of just the strip. If that check ever fires,
the tag is wrong, not the job.

## No reference manifests are checked in here

netscli's equivalent directory keeps copies of manifests that are live in
the catalog, fetched from the catalog unedited. They exist purely so you can
compare a generated PR against a real accepted one. A hand-written or speculative copy
is worse than none, because comparing against it teaches the wrong shape.

So there is nothing to copy in until `fstubner.nvx` has an accepted version.
After the first submission lands, copy that version's directory out of the
catalog verbatim if you want a reference.
