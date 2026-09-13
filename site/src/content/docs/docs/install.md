---
title: Installation
description: How to install Example on Windows, macOS and Linux, and how to check it worked.
---

A sample page, showing how code blocks render in these docs.

## Package managers

The recommended route on each platform:

```sh
# Windows
winget install example

# macOS
brew install example

# Linux
apt install example
```

## Install script

If you would rather not add a package source:

```sh
curl -fsSL https://example.com/install.sh | bash
```

Read a script before piping it to a shell. This one writes a single binary to `/usr/local/bin` and nothing else.

## Check it worked

```sh
example --version
```

If the shell reports that the command is not found, the install location is not on your `PATH`. Open a new terminal first — an installer that changed `PATH` cannot change it for a shell that is already running.

## Uninstall

```sh
brew uninstall example      # or: winget uninstall example
rm -f /usr/local/bin/example
```
