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
  heading: 'Node.js and Bun versions, with a sandbox around every install',
  subhead:
    'Install, switch and pin runtimes per project, and auto-switch on cd. Every npm install runs inside an OS sandbox that confines writes to the project and blocks hosts you did not allow.',
  quickInstall: 'irm https://raw.githubusercontent.com/fstubner/nvx/main/install.ps1 | iex',
  quickInstallAlt: 'curl -fsSL https://raw.githubusercontent.com/fstubner/nvx/main/install.sh | sh',
  installLinkLabel: 'More install options ↓',
  // Every line is what the command line prints at DEFAULT verbosity, from one
  // machine on 2026-09-17, after the auto-install fix in 9fcc19a.
  //
  // Until that fix the flow shown here could not happen. Arriving in a project
  // pinned to a version that was not installed printed a warning and stopped,
  // because a classifier read the error's sentence and took the wrong branch.
  // So the page showed the degraded path, `cd` then a warning then a manual
  // `nvx install` then a manual `nvx use`, as though that were the product. The
  // product asks, installs, verifies, and switches. Four commands became two.
  //
  // The prompt line is exactly what promptConsoleYesNo writes to the tty: a
  // yellow `?`, the question runAuto builds, and ` [y/N]: `. The `y` is the
  // reader's keystroke. Subtractive edits only: the install's "Installing" and
  // "URL" lines, the download bar, the extract timing and the install path are
  // dropped, as are npm's deprecation warning for left-pad and its upgrade
  // notice. Nothing is reworded or invented.
  heroTerminalHtml: `<span class="t-dim">$</span> cd new-project
<span class="t-warn">?</span> Directory requires Node.js 22 (from .nvmrc), but it is not installed. Install it now? <span class="t-dim">[y/N]:</span> y
<span class="t-info">&#8505;</span> Verifying checksum for node-v22.23.2-win-x64.zip...
<span class="t-ok">&#10004;</span> <span class="t-hi">Checksum verified successfully.</span>
<span class="t-ok">&#10004;</span> Node.js v22.23.2 installed successfully
<span class="t-info">&#8505;</span> [nvx] Found .nvmrc: switching to Node.js v22.23.2

<span class="t-dim">$</span> npm install left-pad
<span class="t-info">&#8505;</span> <span class="t-hi">Running in native sandbox: npm install left-pad</span>
added 1 package, and audited 2 packages in 977ms
found 0 vulnerabilities`,
  heroTerminalLabel:
    'A terminal entering a project pinned to a Node.js version that is not installed. nvx asks whether to install it, verifies the checksum, installs it and switches to it, then a package installs inside the native sandbox',
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
