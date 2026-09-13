import type { Branding, Meta } from './types';

export const meta: Meta = {
  domain: 'https://nvx.run',
  // Leads with the job, not the security layer: PRODUCT.md settles that the
  // version manager is the main thing and containment the second.
  title: 'nvx — a Node.js and Bun version manager with a sandbox',
  description:
    'Install, switch and pin Node.js and Bun on Windows, macOS and Linux. One static binary, no dependencies — and every install runs inside an OS sandbox that cannot read your SSH keys or reach hosts you did not allow.',
  ogDescription:
    'A Node.js and Bun version manager for Windows, macOS and Linux that contains what it installs.',
  siteName: 'nvx',
  author: { name: 'Felix Stubner', url: 'https://github.com/fstubner' },
  ogImage: 'https://nvx.run/assets/hero.png',
  ogImageAlt: 'nvx switching Node.js versions and containing an npm install in a terminal',
  faviconPath: '/favicon.svg',
  themeColor: '#111',
};

export const branding: Branding = {
  wordmark: '/assets/wordmark.png',
  wordmarkAlt: 'nvx',
  accentGradient: 'linear-gradient(90deg,#16a34a,#22c55e 50%,#1edcff)',
  bg: '#111',
  fg: '#d4d4d4',
};
