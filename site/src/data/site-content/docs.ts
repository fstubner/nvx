import type { DocsSection } from './types';

export const docsTitle = 'nvx docs';

export const docsDescription =
  'Documentation for nvx, a Node.js and Bun version manager that runs installs inside an OS sandbox on Windows, macOS and Linux.';

export const docsLogo = './public/assets/wordmark.png';

export const docsSidebar: DocsSection[] = [
  {
    label: 'Start',
    items: [
      { label: 'Overview', link: '/docs/' },
      { label: 'Installation', link: '/docs/install/' },
    ],
  },
  {
    label: 'Reference',
    items: [{ label: 'Commands', link: '/docs/commands/' }],
  },
];
