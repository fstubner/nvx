---
title: Installation
description: Install nvx on Windows, macOS or Linux, and check that it worked.
---

## Requirements

- **Windows** on x64.
- **macOS** on Apple silicon or Intel. The sandbox uses `sandbox-exec`, which
  ships with macOS.
- **Linux** on x86_64 or arm64. The sandbox needs kernel 5.13 or later with
  Landlock enabled and unprivileged user namespaces. The network allowlist also
  needs the `ip` command from iproute2. When one is missing, contained commands
  refuse to run, so nothing runs uncontained. nvx downloads the glibc build of
  Node.js and Bun, so Alpine and other musl systems are refused with a message
  that names the cause. On Ubuntu 23.10 and later, AppArmor can refuse the
  sandbox its user namespaces. `nvx doctor` names the setting and the two ways
  forward, and [Known limitations](/docs/limitations/) explains them.

nvx is one static binary and needs nothing installed alongside it.

## Windows

```powershell
irm https://nvx.run/install.ps1 | iex
```

Releases from 0.7.0 are Authenticode-signed, and `install.ps1` checks that
signature before it installs the file. It stops if the file is unsigned or is
signed by anyone other than the nvx publisher. SmartScreen also judges a download
by its reputation. A certificate builds that up as people download what it
signed, so early signed releases can still show "Windows protected your PC".
Defender has also flagged unsigned builds as malware by machine learning,
because nvx rewrites permissions and creates sandbox tokens the way some malware
does.

## macOS and Linux

```sh
curl -fsSL https://nvx.run/install.sh | sh
```

Read a script before piping it to a shell. [What the installer
changes](#what-the-installer-changes) lists everything these two write.

**Without the GitHub CLI (`gh`), this one-liner checks only a checksum.** The
`.sha256` file comes from the same release page as the binary, so it catches a
damaged download and does not show who built the file. `install.sh` makes the
stronger check, a build attestation made by the release workflow, only when `gh`
2.51 or newer is installed and signed in. Otherwise it prints "Provenance check
skipped" and carries on. If you want that check, install `gh`, run `gh auth
login`, and either rerun the installer or check the installed binary yourself:

```sh
gh attestation verify ~/.nvx/bin/nvx --repo fstubner/nvx --signer-workflow fstubner/nvx/.github/workflows/release.yml
```

If it reports a failure, do not run the file. [Verify a
download](#verify-a-download) covers the other checks.

## Check it worked

Open a new terminal, because an installer that changed `PATH` cannot change it
for a shell that is already running. Then run:

```sh
nvx install lts
nvx doctor
```

The first version you install becomes the default, so `nvx use` is not needed
yet. Use it later to switch one shell to another version.

`nvx doctor` reports whether the shims are intercepting, whether your shell loads
the integration, and whether anything on the machine weakens containment. It
exits non-zero when something needs your attention, and `nvx doctor --fix`
repairs what it safely can.

## What the installer changes

- Creates `~/.nvx` and puts the binary in `~/.nvx/bin`. It then runs `nvx
  init-shims`, which writes the `node`, `npm`, `npx`, `corepack`, `pnpm`, `yarn`,
  `bun` and `bunx` shims beside it. The shims are what put nvx in front of those
  commands, and the installer stops with an error if it cannot write them.
- Puts `~/.nvx/bin` at the front of your user `PATH`.
- Adds the shell integration to your profile. That is what makes `nvx use`
  affect your shell and what switches `PATH` on `cd`. Without it the shims still
  run the version each project pins, and `nvx use` does nothing.
  - `install.sh` adds a three-line block. It holds a comment, a line putting
    `~/.nvx/bin` on `PATH`, and a line that runs `eval "$(nvx env)"` in bash and
    zsh only. A login `sh` such as dash gets the `PATH` line and skips the eval,
    because `nvx env` prints bash syntax. For bash it writes the block to
    `~/.bashrc` and to your login profile, for zsh to `~/.zshrc`, and otherwise
    to `~/.profile`. For zsh it also writes the `PATH` line alone to
    `~/.zprofile`, which `zsh -lc` reads. For fish it writes its own file,
    `~/.config/fish/conf.d/nvx.fish`, in fish syntax, and touches nothing else.
    Running it again replaces the line `eval "$(nvx env)"` that earlier versions
    wrote by itself, and keeps a copy of the file as `<profile>.nvx-backup`.
  - `install.ps1` adds one integration line, with a comment above it, to your
    PowerShell `$PROFILE`.
- On Windows, **asks before changing your PowerShell execution policy.**
  Declining still installs nvx. The shell integration then needs
  `Set-ExecutionPolicy RemoteSigned -Scope CurrentUser` before a profile can
  load at all.

cmd.exe has no profile, so nothing is written for it. With `~/.nvx/bin` on your
user `PATH` the shims run each project's pinned version in a cmd window.
`nvx use` cannot switch a cmd window by itself, and says so. `nvx default
<version>` sets the version new windows start on, and
[Commands](/docs/commands/#shells) has the one-line command that switches a
single window.

## Prebuilt binaries

nvx is on npm as `@fstubner/nvx`. It installs only the binary for your
platform and runs no install script:

```sh
npm install -g @fstubner/nvx
```

**Type the scoped name.** `npm install -g nvx` installs a different project,
which also provides an `nvx` command.

This route writes no shims and edits no profile. Next step: run `nvx doctor`,
which reports what is missing, and `nvx doctor --fix` to repair it.

It is not yet published to winget, Scoop or Homebrew. The other route is a
binary. Every release attaches one per platform, each with a SHA-256 sidecar.
The platforms are Windows x64, macOS on Apple silicon and Intel, and Linux on
x86_64 and arm64.

## Verify a download

Each of those five binaries carries a signed build attestation, made by the
release workflow in this repository. With the GitHub CLI (2.51 or newer, signed
in with `gh auth login`), check the file you downloaded:

```sh
gh attestation verify nvx-linux-amd64 --repo fstubner/nvx --signer-workflow fstubner/nvx/.github/workflows/release.yml
```

Use your own file name. If the command reports a failure, do not run the file.
The `--signer-workflow` flag limits the check to attestations made by
`release.yml`, and without it one from any workflow in the repository passes.

On Windows, `nvx.exe` also carries an Authenticode signature, and no other tool
is needed to read it:

```powershell
Get-AuthenticodeSignature nvx.exe | Format-List Status, @{n='Signer'; e={$_.SignerCertificate.Subject}}
```

`Status` should be `Valid` and the signer should be "Open Source Developer Felix
Stubner".

The `.sha256` file beside each binary holds the file's SHA-256. It comes from the
same release page as the binary, so it catches a damaged download and does not
prove who built the file. Compare it from the directory holding both files.

On Linux:

```sh
sha256sum -c nvx-linux-amd64.sha256
```

On macOS:

```sh
shasum -a 256 -c nvx-darwin-arm64.sha256
```

On Windows:

```powershell
(Get-FileHash nvx.exe -Algorithm SHA256).Hash -ieq ((Get-Content nvx.exe.sha256) -split '\s+')[0]
```

That prints `True` when they match. When the GitHub CLI is installed, signed in
and 2.51 or newer, `install.sh` and `install.ps1` run the attestation check
before they install the download. They stop if it fails. Without it they say
the check was skipped and print the command to run. `install.ps1` always checks
the Authenticode signature too, with or without the GitHub CLI.

## From source

```sh
go build -o nvx ./cmd/nvx
```

## Behind a corporate proxy or mirror

nvx reads `HTTPS_PROXY`, `HTTP_PROXY` and `NO_PROXY` like other tools, and
contained installs go through the same proxy once nvx's allowlist has approved
the host. To fetch Node.js from an internal mirror, set `NVX_NODE_MIRROR` to its
`dist` URL. An existing `NVM_NODEJS_ORG_MIRROR` or `FNM_NODE_DIST_MIRROR` works
too. The pre-install checks use the registry your `.npmrc` names. See
[Corporate networks](/docs/policy/#corporate-networks) for what each of these
does and which hosts nvx contacts.

## Upgrade

Run the installer again, or `npm install -g @fstubner/nvx`. Then run `nvx
doctor`. It fails and names `nvx init-shims` when the shims still run an older
nvx. If you ever ran `nvx setup` on Windows before 0.8.0, run `nvx setup` once
from an Administrator terminal to remove the drive-root and `Users` folder
entries that version added. The [changelog](/changelog/) lists what changed for
someone upgrading from 0.7.0.

## Uninstall

1. If you ever ran `nvx setup` on Windows with a version before 0.8.0, run `nvx
   setup` from an Administrator terminal. It removes the drive-root and `Users`
   folder entries that version added.
2. Run `nvx grants reset --all`. It withdraws the read and execute permissions
   granted for `allow_read_exec` entries, forgets trusted policy files and tools,
   and on Windows puts back the permissions of the `.env` files nvx hid.
3. Delete `~/.nvx`.
4. Remove the nvx lines from your shell profile, or from `$PROFILE` on Windows.
5. On Windows, remove `%USERPROFILE%\.nvx\bin` from your user `Path` variable.
