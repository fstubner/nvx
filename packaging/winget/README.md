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
publishing `Moniker: nvx`. Nothing has been submitted yet, so treat the
canonical id as the only form that is known to work and check the catalog
before documenting the short one anywhere user-facing.

The package is **portable**. The release asset is a single `nvx.exe`, not an
installer EXE, so the manifest must use:

```yaml
InstallerType: portable
Commands:
  - nvx
```

## The first submission

`publish.yml`'s Winget job uses `vedantmgoyal9/winget-releaser`, which
updates an existing package. It looks for the package's directory in
winget-pkgs and does nothing useful if there is not one, so the FIRST
version has to be submitted by hand with `wingetcreate`:

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
- `PackageVersion` is `X.Y.Z` with no leading `v`, see below
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

## PackageVersion carries no `v`

Winget sorts versions naturally, so `v0.6.1` and `0.6.0` do not compare as
an upgrade. Nothing downstream catches this: winget-pkgs accepts a leading
`v` without complaint, and a merged manifest is permanent. netscli published
five CLI versions that way before anyone noticed.

`publish.yml` strips the `v` and then asserts on the result, which is why
the assertion is there rather than just the strip. If that check ever fires,
the tag is wrong, not the job.

## No reference manifests are checked in here

netscli's equivalent directory keeps copies of manifests that are live in
the catalog, fetched from the catalog unedited, purely so a generated PR can
be compared against a real accepted one. A hand-written or speculative copy
is worse than none, because comparing against it teaches the wrong shape.

So there is nothing to copy in until `fstubner.nvx` has an accepted version.
After the first submission lands, copy that version's directory out of the
catalog verbatim if a reference is wanted.
