#!/usr/bin/env node
'use strict';

// Locate the platform binary and hand the process over to it.
//
// No postinstall script, deliberately. nvx exists because `npm install` runs
// code from strangers before you have looked at it; shipping a postinstall that
// downloads a binary would be the same trick this tool is here to stop. The
// binaries ship as optionalDependencies with `os`/`cpu` set, so npm resolves the
// one that matches and installs nothing else -- and a machine npm has no build
// for gets a clear error rather than a silent half-install.

const { spawnSync } = require('node:child_process');
const path = require('node:path');

const PKGS = {
  'linux-x64': '@fstubner/nvx-linux-x64',
  'linux-arm64': '@fstubner/nvx-linux-arm64',
  'darwin-x64': '@fstubner/nvx-darwin-x64',
  'darwin-arm64': '@fstubner/nvx-darwin-arm64',
  'win32-x64': '@fstubner/nvx-win32-x64',
};

const key = `${process.platform}-${process.arch}`;
const pkg = PKGS[key];
if (!pkg) {
  console.error(
    `nvx: no prebuilt binary for ${key}.\n` +
    `Supported: ${Object.keys(PKGS).join(', ')}.\n` +
    `Build from source instead: https://github.com/fstubner/nvx`);
  process.exit(1);
}

let binary;
try {
  // Resolve the package's manifest rather than a path inside it: the manifest is
  // the one file whose location is guaranteed by the package layout.
  const manifest = require.resolve(`${pkg}/package.json`);
  binary = path.join(path.dirname(manifest), 'bin', process.platform === 'win32' ? 'nvx.exe' : 'nvx');
} catch (e) {
  console.error(
    `nvx: the binary package ${pkg} is not installed.\n` +
    `This happens when a lockfile was made on another platform, or when install ran with\n` +
    `--no-optional. Reinstall on this machine, or install ${pkg} directly.`);
  process.exit(1);
}

const r = spawnSync(binary, process.argv.slice(2), { stdio: 'inherit' });
if (r.error) {
  console.error(`nvx: could not run ${binary}: ${r.error.message}`);
  process.exit(1);
}
// Signals are reported as a name, not a code; 128+n is the shell convention.
if (r.signal) process.exit(1);
process.exit(r.status === null ? 1 : r.status);
