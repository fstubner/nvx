import type { Hero, HeroCommands, HeroDownload } from './types';

export const hero: Hero = {
  badge: 'Windows · macOS · Linux',
  heading: 'Node.js and Bun versions, with a sandbox around every install',
  subhead:
    'Install, switch and pin runtimes per project, and auto-switch on cd. Every npm install runs inside an OS sandbox that confines writes to the project and blocks hosts you did not allow.',
  quickInstall: 'irm https://raw.githubusercontent.com/fstubner/nvx/main/install.ps1 | iex',
  quickInstallAlt: 'curl -fsSL https://raw.githubusercontent.com/fstubner/nvx/main/install.sh | sh',
  installLinkLabel: 'More install options ↓',
  // Two real runs on 2026-09-16, shown as one session. Every line is output
  // nvx printed, with NVX_VERBOSE=1 for the install so the checks it runs are
  // visible rather than silent.
  //
  // Edits are subtractive only. Dropped: the OSV scan's own progress line, the
  // sandbox session id, the advisory ancestor-permission notices, and npm's
  // upgrade notice. Nothing is reworded, re-coloured or invented.
  //
  // An earlier version showed nvx blocking googlechromelabs.github.io during
  // an npm ci. That was real output and still the wrong thing to lead with.
  // The host is Chrome's own CDN and the install was legitimate, so the hero
  // advertised nvx breaking an ordinary build. This shows both jobs instead,
  // switching a runtime and installing a package with the checks passing.
  heroTerminalHtml: `<span class="t-dim">$</span> nvx use 20
<span class="t-ok">&#10004;</span> Now using Node.js v20.11.0 in this terminal.

<span class="t-dim">$</span> npm install left-pad
<span class="t-info">&#8505;</span> Verifying package "left-pad"...
<span class="t-info">&#8505;</span> <span class="t-hi">Vulnerability scan clean. No active CVEs found.</span>
<span class="t-info">&#8505;</span> <span class="t-hi">Windows AppContainer isolation active</span>
added 1 package, and audited 2 packages in 907ms
found 0 vulnerabilities`,
  heroTerminalLabel:
    'A terminal switching to Node.js 20, then installing a package while nvx verifies it, scans for known vulnerabilities and runs the install inside a Windows AppContainer',
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
