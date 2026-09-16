import type { Hero, HeroCommands, HeroDownload } from './types';

export const hero: Hero = {
  badge: 'Windows · macOS · Linux',
  heading: 'Node.js and Bun versions, with a sandbox around every install',
  subhead:
    'Install, switch and pin runtimes per project, and auto-switch on cd. Every npm install runs inside an OS sandbox that confines writes to the project and blocks hosts you did not allow.',
  quickInstall: 'irm https://raw.githubusercontent.com/fstubner/nvx/main/install.ps1 | iex',
  quickInstallAlt: 'curl -fsSL https://raw.githubusercontent.com/fstubner/nvx/main/install.sh | sh',
  installLinkLabel: 'More install options ↓',
  // Captured from one real run on 2026-09-16, not composed from several and
  // not written by hand. `npm ci` in a project whose dependency tree pulls
  // chromedriver, whose install step reaches googlechromelabs.github.io.
  //
  // Three edits, all subtractive. A `Failed to parse package-lock.json`
  // warning above this is a real defect being fixed separately and is not
  // product behaviour. The trailing npm error output and the sandbox debug-log
  // path are dropped. Nothing is re-coloured and no line is invented.
  //
  // It shows a refusal rather than a success on purpose. A contained install
  // that works looks exactly like an uncontained one, so the screenshot it
  // replaced showed nothing happening and asked the reader to take the rest on
  // trust.
  heroTerminalHtml: `<span class="t-dim">$</span> npm ci
<span class="t-info">&#8505;</span> Running in native sandbox: npm ci
<span class="t-warn">&#9888;</span> <span class="t-block">Blocked egress: googlechromelabs.github.io:443</span>
<span class="t-info">&#8505;</span> <span class="t-hi">-y</span> and <span class="t-hi">NVX_YES</span> deliberately do not approve this.`,
  heroTerminalLabel:
    'A terminal showing npm ci running inside the native sandbox, refusing to widen its trust boundary without approval, and blocking an outbound connection',
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
