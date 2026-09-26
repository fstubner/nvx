---
title: Installation
description: Install nvx on Windows, macOS or Linux, and check that it worked.
---

## Windows

```powershell
irm https://nvx.run/install.ps1 | iex
```

## macOS and Linux

```sh
curl -fsSL https://nvx.run/install.sh | sh
```

Read a script before piping it to a shell. Each creates `~/.nvx` and puts a
single binary in `~/.nvx/bin`. `install.sh` then adds a three-line block to your
shell profile: a comment, a line putting `~/.nvx/bin` on `PATH`, and
`eval "$(nvx env)"`. For bash it writes the block to `~/.bashrc` and to your
login profile, `~/.zshrc` for zsh, and `~/.profile` otherwise. `install.ps1`
adds `~/.nvx/bin` to your user `PATH` and one integration line, with a comment
above it, to your PowerShell `$PROFILE`.

## Prebuilt binaries

nvx is not yet published to winget, Scoop, Homebrew or npm. The other route is a
binary: every release attaches one per platform with a SHA-256 sidecar, for
Windows x64, macOS on Apple silicon and Intel, and Linux on x86_64 and arm64.

## From source

```sh
go build -o nvx ./cmd/nvx
```

## What the installer changes

- Puts `~/.nvx/bin` at the front of your user `PATH`.
- Adds the shell integration line to your profile. That line is what makes
  `nvx use` affect your shell and what switches runtimes on `cd`; without it
  nvx still installs runtimes, but switching does nothing.
- On Windows, **asks before changing your PowerShell execution policy.**
  Declining still installs nvx — the shell integration then needs
  `Set-ExecutionPolicy RemoteSigned -Scope CurrentUser` before a profile can
  load at all.

## Check it worked

Open a new terminal — an installer that changed `PATH` cannot change it for a
shell that is already running — then:

```sh
nvx install 22
nvx use 22
nvx doctor
```

`nvx doctor` reports whether the shims are intercepting, whether your shell loads
the integration, and whether anything on the machine weakens containment. It
exits non-zero when something needs your attention, and `nvx doctor --fix`
repairs what it safely can.

## Uninstall

1. If you ever ran `nvx setup` on Windows, run `nvx setup --undo` from an
   Administrator terminal to remove the drive-root grants it added.
2. Run `nvx grants reset --all` to withdraw the read and execute permissions
   granted for `allow_read_exec` entries, and to forget approved grants.
3. Delete `~/.nvx`.
4. Remove the nvx lines from your shell profile, or from `$PROFILE` on Windows.
5. On Windows, remove `%USERPROFILE%\.nvx\bin` from your user `Path` variable.
