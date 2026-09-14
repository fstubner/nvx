import type { Hero, HeroCommands, HeroDownload } from './types';

export const hero: Hero = {
  badge: 'Windows · macOS · Linux',
  heading: 'Node.js and Bun versions, with a sandbox around every install',
  subhead:
    'Install, switch and pin runtimes per project, and auto-switch on cd. One static binary with no dependencies. Every npm install runs inside an OS sandbox, with a throwaway HOME, writes confined to the project, and an allowlist for anything it tries to contact. On Windows and Linux it cannot read your SSH keys or npm token either.',
  quickInstall: 'irm https://raw.githubusercontent.com/fstubner/nvx/main/install.ps1 | iex',
  quickInstallAlt: 'curl -fsSL https://raw.githubusercontent.com/fstubner/nvx/main/install.sh | sh',
  installLinkLabel: 'More install options ↓',
  heroImage: '/assets/hero.png',
  heroImageAlt: 'A terminal showing nvx list and nvx doctor: installed Node.js and Bun versions, and every shim reporting OK',
  heroImageWidth: 1200,
  heroImageHeight: 600,
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
