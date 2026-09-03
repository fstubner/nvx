import type { Hero, HeroCommands, HeroDownload } from './types';

export const hero: Hero = {
  // Platforms, licence, language — whatever a visitor cannot get from the
  // headline. Keep it to three or four items.
  badge: 'Windows · macOS · Linux',
  // The strongest on-page signal after the title. Say what the thing is and
  // who it is for; "modern" and "powerful" are not what anyone searches for.
  heading: 'A small command-line tool for doing one thing well',
  subhead:
    'Example runs the same way on every platform, prints human-readable output by default and JSON when a program is reading, and has no runtime to install.',
  // The two commands the hero shows before any script runs. os-tabs.ts
  // replaces both once the visitor's platform is known — see heroCommands
  // below — so these are the defaults a crawler and a no-JavaScript visitor
  // get. Make them the ones that work for the widest audience.
  quickInstall: 'brew install example',
  quickInstallAlt: 'curl -fsSL https://example.com/install.sh | bash',
  installLinkLabel: 'More install options ↓',
  heroImage: '/assets/hero.png',
  heroImageAlt: 'The Example command-line tool running in a terminal',
  heroImageWidth: 1640,
  heroImageHeight: 930,
  sourceUrl: 'https://github.com/your-org/example',
  downloadLabel: 'Desktop app',
  downloadMenuLabel: 'Choose desktop installer',
};

// The hero's two command rows, per platform. `packageManager` goes in the
// narrower row, `script` in the wide row beside the download button — keyed
// by which row they suit, not by which is "recommended".
export const heroCommands: HeroCommands = {
  windows: {
    packageManager: 'winget install example',
    script: 'iwr -useb https://example.com/install.ps1 | iex',
  },
  macos: {
    packageManager: 'brew install example',
    script: 'curl -fsSL https://example.com/install.sh | bash',
  },
  linux: {
    packageManager: 'apt install example',
    script: 'curl -fsSL https://example.com/install.sh | bash',
  },
};

// The installers behind the hero's "Desktop app" button, in menu order. The
// first is what the button points at before any script runs.
//
// A product with no desktop build leaves this empty: the button, its caption
// and the menu are then not rendered at all, and the install command takes
// the whole row.
const RELEASE = 'https://github.com/your-org/example/releases/latest/download';

export const heroDownloads: HeroDownload[] = [
  {
    os: 'windows',
    name: 'Windows',
    meta: 'AMD64',
    ext: 'msi',
    href: `${RELEASE}/example-windows-x86_64.msi`,
    cue: 'Windows · .msi installer',
    preferred: true,
  },
  {
    os: 'macos',
    name: 'macOS',
    meta: 'Apple silicon',
    ext: 'dmg',
    href: `${RELEASE}/example-macos-aarch64.dmg`,
    cue: 'macOS · Apple silicon .dmg',
    // Offered only when the browser reports an arm64 machine. The Intel
    // build below stays the default because Rosetta 2 runs it either way.
    appleSilicon: true,
  },
  {
    os: 'macos',
    name: 'macOS',
    meta: 'Intel x86_64',
    ext: 'dmg',
    href: `${RELEASE}/example-macos-x86_64.dmg`,
    cue: 'macOS · Intel .dmg',
    preferred: true,
  },
  {
    os: 'linux',
    name: 'Linux',
    meta: 'x86_64 portable',
    ext: 'AppImage',
    href: `${RELEASE}/example-linux-x86_64.AppImage`,
    cue: 'Linux · .AppImage',
    preferred: true,
  },
];
