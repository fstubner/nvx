import type { Hero } from './types';

export const hero: Hero = {
  badge: 'Open source · MIT · Rust · Windows, Linux, macOS',
  heading: 'A modern network scanner',
  subhead:
    'Discover LAN devices, scan TCP ports, query DNS, trace routes, inspect hosts, and capture packets from the desktop app, terminal UI, CLI, or MCP server. Each interface calls the same Rust core, so results stay consistent.',
  quickInstall:
    'curl -fsSL https://raw.githubusercontent.com/fstubner/netscli/main/scripts/install.sh | bash',
  installLinkLabel: 'More install options ↓',
  heroImage: '/assets/tui-discover.png',
  heroImageWebp: '/assets/tui-discover.webp',
  heroImageAlt:
    'netscli terminal UI running /discover with sanitized lab hostnames, vendors, and response times',
  heroImageWidth: 1640,
  heroImageHeight: 930,
  sourceUrl: 'https://github.com/fstubner/netscli',
  downloadsLabel: 'Desktop app',
  downloads: [
    {
      label: 'Windows',
      hint: 'AMD64',
      url: 'https://github.com/fstubner/netscli/releases/latest/download/netscli-gui-windows-x86_64.msi',
    },
    {
      label: 'macOS',
      hint: 'Apple silicon',
      url: 'https://github.com/fstubner/netscli/releases/latest/download/netscli-gui-macos-aarch64.dmg',
    },
    {
      label: 'macOS',
      hint: 'Intel x86_64',
      url: 'https://github.com/fstubner/netscli/releases/latest/download/netscli-gui-macos-x86_64.dmg',
    },
    {
      label: 'Linux',
      hint: 'x86_64 portable',
      url: 'https://github.com/fstubner/netscli/releases/latest/download/netscli-gui-linux-x86_64.AppImage',
    },
    {
      label: 'Debian / Ubuntu',
      hint: 'AMD64',
      url: 'https://github.com/fstubner/netscli/releases/latest/download/netscli-gui-linux-x86_64.deb',
    },
  ],
  packageManagerInstall: 'winget install fstubner.netscli',
};
