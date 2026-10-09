---
title: Install, upgrade and uninstall
description: Every way to install nvx, how to verify a download, how to upgrade, and how to remove it.
---

Install nvx with the one-line installer, from npm, or as a single binary.

## Requirements

- **Windows** on x64.
- **macOS** on Apple silicon or Intel.
- **Linux** on x86_64 or arm64, with kernel 5.13 or later, Landlock enabled,
  unprivileged user namespaces and the `ip` command from iproute2. Alpine and
  other musl systems are not supported.

When the sandbox cannot start, nvx refuses to run the command. It never runs it
uncontained instead. `nvx doctor` names the cause.

## The installer

```powershell
irm https://nvx.run/install.ps1 | iex
```

```sh
curl -fsSL https://nvx.run/install.sh | sh
```

The installer:

- puts the binary and the `node`, `npm`, `npx`, `corepack`, `pnpm`, `yarn`,
  `bun` and `bunx` shims in `~/.nvx/bin`
- puts `~/.nvx/bin` first on your user `PATH`
- adds a short block to your shell profile (bash, zsh, fish or PowerShell), so
  `nvx use` and the switch on `cd` work
- on Windows, asks before it changes your PowerShell execution policy

`install.ps1` stops unless `nvx.exe` is signed by the nvx publisher. Early
signed releases can still show "Windows protected your PC" while the
certificate builds up reputation.

`install.sh` checks a SHA-256 checksum. It also checks the build attestation
when the GitHub CLI 2.51 or newer is installed and signed in. Otherwise it
prints "Provenance check skipped".

## From npm

```sh
npm install -g @fstubner/nvx
```

Type the scoped name. The unscoped `nvx` on npm is a different project. This
route writes no shims and edits no profile, so run `nvx doctor --fix` next.

nvx is not on winget, Scoop or Homebrew yet.

## A single binary

Every [release](https://github.com/fstubner/nvx/releases) has a binary for
Windows x64, macOS (Apple silicon and Intel) and Linux (x86_64 and arm64). Check
one before you run it.

```sh
gh attestation verify nvx-linux-amd64 --repo fstubner/nvx --signer-workflow fstubner/nvx/.github/workflows/release.yml
```

On Windows you can also read the signature with no other tool. `Status` should
be `Valid`, signed by "Open Source Developer Felix Stubner".

```powershell
Get-AuthenticodeSignature nvx.exe | Format-List Status, @{n='Signer'; e={$_.SignerCertificate.Subject}}
```

The `.sha256` file beside each binary catches a damaged download. It comes from
the same page, so it does not prove who built the file.

To build from source, run `go build -o nvx ./cmd/nvx`.

## Upgrade

Run the installer again, or `npm install -g @fstubner/nvx`. Then run
`nvx doctor`. If it names `nvx init-shims`, run that.

If you ever ran `nvx setup` on Windows with a version before 0.8.0, run
`nvx setup` once from an Administrator terminal. It removes the folder access
that older versions added. The [changelog](/changelog/) lists what changed.

## Uninstall

1. On Windows, if you ever ran `nvx setup` before 0.8.0, run `nvx setup` from
   an Administrator terminal.
2. Run `nvx grants reset --all`. It withdraws the folder permissions nvx
   granted, forgets what you trusted, and on Windows puts back the permissions
   of the `.env` files nvx hid.
3. Delete `~/.nvx`.
4. Remove the nvx lines from your shell profile, or from `$PROFILE` on Windows.
5. On Windows, remove `%USERPROFILE%\.nvx\bin` from your user `Path`.
