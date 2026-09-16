import type { Hero, HeroCommands, HeroDownload } from './types';

export const hero: Hero = {
  badge: 'Windows · macOS · Linux',
  heading: 'Node.js and Bun versions, with a sandbox around every install',
  subhead:
    'Install, switch and pin runtimes per project, and auto-switch on cd. Every npm install runs inside an OS sandbox that confines writes to the project and blocks hosts you did not allow.',
  quickInstall: 'irm https://raw.githubusercontent.com/fstubner/nvx/main/install.ps1 | iex',
  quickInstallAlt: 'curl -fsSL https://raw.githubusercontent.com/fstubner/nvx/main/install.sh | sh',
  installLinkLabel: 'More install options ↓',
  // Real output from one machine on 2026-09-16, shown as one session. Every
  // line is something nvx printed. NVX_VERBOSE=1 for the install, so the
  // checks it runs are visible rather than silent.
  //
  // Edits are subtractive only. Dropped: the download progress bar, the
  // install path, the OSV scan's progress line, the sandbox session id, the
  // advisory ancestor-permission notices, and npm's upgrade notice. Nothing is
  // reworded, re-coloured or invented.
  //
  // The arc is the product in nine lines. Arriving in a project pinned to a
  // runtime that is not installed is the moment a version manager earns its
  // place, and it is also where nvx's checksum verification shows up, which
  // fnm and volta both lack. The install that follows is the second job.
  heroTerminalHtml: `<span class="t-dim">$</span> cd new-project
<span class="t-warn">&#9888;</span> [nvx] Node.js in .nvmrc: no installed version matches query '22'

<span class="t-dim">$</span> nvx install 22
<span class="t-info">&#8505;</span> Verifying checksum for node-v22.23.2-win-x64.zip...
<span class="t-ok">&#10004;</span> <span class="t-hi">Checksum verified successfully.</span>
<span class="t-ok">&#10004;</span> Node.js v22.23.2 installed successfully

<span class="t-dim">$</span> npm install left-pad
<span class="t-info">&#8505;</span> <span class="t-hi">Vulnerability scan clean. No active CVEs found.</span>
<span class="t-info">&#8505;</span> <span class="t-hi">Windows AppContainer isolation active</span>
found 0 vulnerabilities`,
  heroTerminalLabel:
    'A terminal entering a project pinned to a Node.js version that is not installed, installing it with its checksum verified, then installing a package with no advisories against it inside a Windows AppContainer',
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
