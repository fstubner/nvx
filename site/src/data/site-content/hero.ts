import type { Hero, HeroCommands, HeroDownload } from './types';

export const hero: Hero = {
  badge: 'Windows · macOS · Linux',
  // The badge becomes `v0.6.0 · What changed →` once the release lookup that
  // already runs for the download counter confirms a version, and the
  // version leaves the metrics line below the headline. The platform list
  // above stays as the fallback: no extra request is made for this, but the
  // one it rides on can be rate-limited or fail, and a badge that renders as
  // nothing is worse than the string it replaced. The platform claim itself
  // is not lost -- the install section's lead line and the comparison table
  // both restate it, in the two places a reader actually deciding on
  // platform support looks.
  releaseLink: '/changelog/',
  heading: 'npm install and npx, inside an OS sandbox',
  subhead:
    'nvx runs npm install and npx inside an OS sandbox on Windows, macOS and Linux, so a package cannot read your credentials or reach a host you did not allow. You and your agent type the same commands. It also manages your Node.js and Bun versions.',
  quickInstall: 'irm https://nvx.run/install.ps1 | iex',
  quickInstallAlt: 'curl -fsSL https://nvx.run/install.sh | sh',
  installLinkLabel: 'More install options ↓',
  // Every line is what nvx 0.8.0 printed at default verbosity on Windows on
  // 2026-10-09, in a fresh nvx home and a scratch project. Subtractive edits
  // only: the download bar, the extract timing, the install path at the end of
  // the "installed successfully" line, and the first "Running in native
  // sandbox" line, which is npm working out what the install brings in so nvx
  // can check it. Nothing is reworded. sample-package is a real package with no
  // dependencies and no install scripts.
  heroTerminalHtml: `<span class="t-dim">$</span> nvx install 22
<span class="t-info">&#8505;</span> Verifying checksum for node-v22.23.3-win-x64.zip...
<span class="t-ok">&#10004;</span> <span class="t-hi">Checksum verified successfully.</span>
<span class="t-ok">&#10004;</span> Node.js v22.23.3 installed successfully

<span class="t-dim">$</span> npm install sample-package
<span class="t-info">&#8505;</span> <span class="t-hi">Running in native sandbox: npm install sample-package@1.0.1</span>
added 1 package, and audited 2 packages in 2s
found 0 vulnerabilities`,
  // Windows, because the output is: a win-x64 zip and an AppContainer run.
  heroTerminalChrome: 'windows',
  heroTerminalLabel:
    'A terminal running nvx install 22, which verifies the checksum and installs Node.js 22, then npm install sample-package, which runs inside the native sandbox',
  heroImage: '/assets/hero.png',
  heroImageAlt: 'A terminal running nvx install 22, then npm install sample-package inside the native sandbox',
  heroImageWidth: 1200,
  heroImageHeight: 501,
  sourceUrl: 'https://github.com/fstubner/nvx',
  downloadLabel: 'Desktop app',
  downloadMenuLabel: 'Choose desktop installer',
};

export const heroCommands: HeroCommands = {
  // Both rows carry the install script. nvx is on npm as @fstubner/nvx, but
  // that route needs Node.js already installed, which a version manager's first
  // command should not assume. It is not on winget, Scoop or Homebrew yet.
  windows: {
    packageManager: 'irm https://nvx.run/install.ps1 | iex',
    script: 'irm https://nvx.run/install.ps1 | iex',
  },
  macos: {
    packageManager: 'curl -fsSL https://nvx.run/install.sh | sh',
    script: 'curl -fsSL https://nvx.run/install.sh | sh',
  },
  linux: {
    packageManager: 'curl -fsSL https://nvx.run/install.sh | sh',
    script: 'curl -fsSL https://nvx.run/install.sh | sh',
  },
};

// nvx is a command-line tool with no desktop build, so the hero's download
// button, its caption and its menu are not rendered at all and the install
// command takes the whole row.
export const heroDownloads: HeroDownload[] = [];
