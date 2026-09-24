import type { Platform, PlatformInstall, SectionCopy, TryCommand } from './types';

export const installCopy: SectionCopy = {
  heading: 'Install',
  leadHtml:
    'One command on every platform. nvx is not yet on winget, Scoop, Homebrew or npm, so the install script and the release binaries are the two routes that exist.',
};

const RELEASE = 'https://github.com/fstubner/nvx/releases/latest/download';

export const installByPlatform: Record<Platform, PlatformInstall> = {
  windows: {
    cli: [
      {
        label: 'PowerShell',
        command: 'irm https://nvx.run/install.ps1 | iex',
      },
      { label: 'Binary', href: `${RELEASE}/nvx.exe`, hint: 'x64, unsigned, so SmartScreen will ask before it runs.' },
    ],
    desktop: [],
  },
  macos: {
    cli: [
      {
        label: 'Shell',
        command: 'curl -fsSL https://nvx.run/install.sh | sh',
      },
      { label: 'Apple silicon', href: `${RELEASE}/nvx-darwin-arm64` },
      { label: 'Intel', href: `${RELEASE}/nvx-darwin-amd64` },
    ],
    desktop: [],
  },
  linux: {
    cli: [
      {
        label: 'Shell',
        command: 'curl -fsSL https://nvx.run/install.sh | sh',
      },
      { label: 'x86_64', href: `${RELEASE}/nvx-linux-amd64` },
      { label: 'arm64', href: `${RELEASE}/nvx-linux-arm64` },
    ],
    desktop: [],
  },
};

export const tryCommands: TryCommand[] = [
  { comment: 'Install a runtime and use it in this shell', command: 'nvx install 22 && nvx use 22' },
  { comment: 'Install packages, contained, with no change to how you type it', command: 'npm install' },
  { comment: 'Check that nvx is intercepting, and that nothing weakens it', command: 'nvx doctor' },
];

export const installBinariesNote =
  'Every release attaches prebuilt binaries with SHA-256 sidecars for Windows x64, macOS on Apple silicon and Intel, and Linux on x86_64 and arm64.';

export const installFromSource = 'go build -o nvx ./cmd/nvx';

export const installNotes = [
  'One static binary, no runtime to install alongside it. The install script',
  'puts nvx on PATH and adds a line to your shell profile. nvx doctor reports',
  'whether both worked.',
];
