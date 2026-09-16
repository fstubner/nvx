import type { Hero, HeroCommands, HeroDownload } from './types';

export const hero: Hero = {
  badge: 'Windows · macOS · Linux',
  heading: 'Node.js and Bun versions, with a sandbox around every install',
  subhead:
    'Install, switch and pin runtimes per project, and auto-switch on cd. Every npm install runs inside an OS sandbox that confines writes to the project and blocks hosts you did not allow.',
  quickInstall: 'irm https://raw.githubusercontent.com/fstubner/nvx/main/install.ps1 | iex',
  quickInstallAlt: 'curl -fsSL https://raw.githubusercontent.com/fstubner/nvx/main/install.sh | sh',
  installLinkLabel: 'More install options ↓',
  // Every line is what the command line actually prints, at DEFAULT
  // verbosity, captured on one machine on 2026-09-16.
  //
  // An earlier version showed "Vulnerability scan clean. No active CVEs found."
  // and "Windows AppContainer isolation active". Both are real, and both appear
  // only under NVX_VERBOSE=1, so the page was advertising output a reader
  // following along would not get. The sandbox line below is the one a default
  // run prints, and the checksum pair is the security beat that needs no flag.
  //
  // Edits are subtractive only. Dropped from the install: the "Installing" and
  // "URL" lines, the download progress bar, the extract timing, and the
  // trailing install path. Dropped from npm: a deprecation warning for the
  // package and npm's own upgrade notice. Nothing is reworded or invented.
  heroTerminalHtml: `<span class="t-dim">$</span> cd new-project
<span class="t-warn">&#9888;</span> [nvx] Node.js in .nvmrc: no installed version matches query '22'

<span class="t-dim">$</span> nvx install 22
<span class="t-info">&#8505;</span> Verifying checksum for node-v22.23.2-win-x64.zip...
<span class="t-ok">&#10004;</span> <span class="t-hi">Checksum verified successfully.</span>
<span class="t-ok">&#10004;</span> Node.js v22.23.2 installed successfully

<span class="t-dim">$</span> nvx use 22
<span class="t-ok">&#10004;</span> Now using Node.js v22.23.2 in this terminal.

<span class="t-dim">$</span> npm install left-pad
<span class="t-info">&#8505;</span> <span class="t-hi">Running in native sandbox: npm install left-pad</span>
added 1 package, and audited 2 packages in 977ms
found 0 vulnerabilities`,
  heroTerminalLabel:
    'A terminal entering a project pinned to a Node.js version that is not installed, installing it with its checksum verified, switching to it, then installing a package inside the native sandbox',
  heroImage: '/assets/hero.png',
  heroImageAlt: 'A terminal showing nvx list, then npm install running inside the native sandbox and finishing with no vulnerabilities',
  heroImageWidth: 1200,
  heroImageHeight: 573,
  sourceUrl: 'https://github.com/fstubner/nvx',
  downloadLabel: 'Desktop app',
  downloadMenuLabel: 'Choose desktop installer',
};

export const heroCommands: HeroCommands = {
  // No package-manager route yet on any platform: nvx is not on winget, Scoop,
  // Homebrew or npm. Both rows therefore carry the install script rather than
  // advertising a channel that would 404.
  windows: {
    packageManager: 'irm https://raw.githubusercontent.com/fstubner/nvx/main/install.ps1 | iex',
    script: 'irm https://raw.githubusercontent.com/fstubner/nvx/main/install.ps1 | iex',
  },
  macos: {
    packageManager: 'curl -fsSL https://raw.githubusercontent.com/fstubner/nvx/main/install.sh | sh',
    script: 'curl -fsSL https://raw.githubusercontent.com/fstubner/nvx/main/install.sh | sh',
  },
  linux: {
    packageManager: 'curl -fsSL https://raw.githubusercontent.com/fstubner/nvx/main/install.sh | sh',
    script: 'curl -fsSL https://raw.githubusercontent.com/fstubner/nvx/main/install.sh | sh',
  },
};

// nvx is a command-line tool with no desktop build, so the hero's download
// button, its caption and its menu are not rendered at all and the install
// command takes the whole row.
export const heroDownloads: HeroDownload[] = [];
