import type { Platform, PlatformInstall, SectionCopy, TryCommand } from './types';

export const installCopy: SectionCopy = {
  heading: 'Install',
  leadHtml:
    'One command installs nvx on Windows, macOS or Linux. It is also on npm as <code>@fstubner/nvx</code>.',
};

const RELEASE = 'https://github.com/fstubner/nvx/releases/latest/download';

export const installByPlatform: Record<Platform, PlatformInstall> = {
  windows: {
    cli: [
      {
        label: 'PowerShell',
        command: 'irm https://nvx.run/install.ps1 | iex',
      },
      { label: 'Binary', href: `${RELEASE}/nvx.exe`, hint: 'x64 and signed. SmartScreen may still ask while the certificate is new.' },
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

// One list for every platform, so each command has to parse in bash, zsh and
// Windows PowerShell 5.1 alike. `&&` is a parse error in PowerShell 5.1, the
// shell a Windows reader most likely pastes into, and `;` works in all three.
export const tryCommands: TryCommand[] = [
  { comment: 'Install a runtime and use it in this shell', command: 'nvx install 22; nvx use 22' },
  { comment: 'Install packages, contained, with no change to how you type it', command: 'npm install' },
  { comment: 'Check that nvx is intercepting, and that nothing weakens it', command: 'nvx doctor' },
];

export const installBinariesNote =
  'Every release attaches prebuilt binaries with SHA-256 sidecars for Windows x64, macOS on Apple silicon and Intel, and Linux on x86_64 and arm64.';

export const installFromSource = 'go build -o nvx ./cmd/nvx';

// install.sh writes a three-line block (a comment, the PATH line and
// `eval "$(nvx env)"`) to the profile of the shell it finds, and for bash to
// both .bashrc and the login profile. install.ps1 sets the user PATH and adds
// two lines to $PROFILE.
export const installNotes = [
  'One static binary, no runtime to install alongside it. The install script',
  'puts nvx on PATH and adds a short nvx block to your shell profile (for bash,',
  'to .bashrc and your login profile). nvx doctor reports whether both worked.',
];
