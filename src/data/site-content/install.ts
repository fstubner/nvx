import type { Platform, PlatformInstall, SectionCopy, TryCommand } from './types';

export const installCopy: SectionCopy = {
  heading: 'Install',
  leadHtml: 'Pick a platform. Position 0 in each list is the recommended route and renders as the card; the rest are alternatives.',
};

const RELEASE = 'https://github.com/your-org/example/releases/latest/download';

export const installByPlatform: Record<Platform, PlatformInstall> = {
  windows: {
    cli: [
      { label: 'winget', command: 'winget install example' },
      { label: 'Scoop', command: 'scoop install example' },
      { label: 'Install script', command: 'iwr -useb https://example.com/install.ps1 | iex' },
    ],
    desktop: [
      {
        label: 'Windows installer',
        href: `${RELEASE}/example-windows-x86_64.msi`,
        hint: 'Unsigned: SmartScreen will ask before it runs.',
      },
    ],
  },
  macos: {
    cli: [
      { label: 'Homebrew', command: 'brew install example' },
      { label: 'Install script', command: 'curl -fsSL https://example.com/install.sh | bash' },
    ],
    desktop: [
      { label: 'Apple silicon', href: `${RELEASE}/example-macos-aarch64.dmg` },
      { label: 'Intel', href: `${RELEASE}/example-macos-x86_64.dmg` },
    ],
  },
  linux: {
    cli: [
      { label: 'apt', command: 'apt install example' },
      { label: 'Install script', command: 'curl -fsSL https://example.com/install.sh | bash' },
      { label: 'Binary', href: `${RELEASE}/example-linux-x86_64.tar.gz`, hint: 'x86_64' },
    ],
    desktop: [{ label: 'AppImage', href: `${RELEASE}/example-linux-x86_64.AppImage` }],
  },
};

// The "try it" block: the first few commands someone runs after installing.
export const tryCommands: TryCommand[] = [
  { comment: 'Run it once', command: 'example run' },
  { comment: 'Read the output from a script', command: 'example run --json' },
  { comment: 'See every flag', command: 'example --help' },
];

export const installBinariesNote =
  'Prebuilt binaries are attached to every GitHub release for Windows, macOS and Linux.';

// Two things /llms.txt says that no page does: a build-from-source route and
// any caveat a reader acting on the install list needs. One line each.
export const installFromSource = 'cargo install example';

export const installNotes = [
  'Example needs no runtime: the binary is self-contained on all three',
  'platforms.',
];
