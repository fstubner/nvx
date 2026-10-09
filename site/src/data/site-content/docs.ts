import type { DocsSection } from './types';

export const docsTitle = 'nvx docs';

export const docsDescription =
  'Documentation for nvx, which runs npm install and npx inside an OS sandbox on Windows, macOS and Linux, so a package cannot read your credentials or reach a host you did not allow.';

export const docsLogo = './public/assets/wordmark.png';

// Configuration lives at /docs/policy/ and How it works at /docs/containment/.
// Those were the pages' names before the 2026-10-09 rewrite, and links already
// published point at them. The site has no redirects, so renaming the files
// would break those links.
export const docsSidebar: DocsSection[] = [
  {
    label: 'Start',
    items: [
      { label: 'Get started', link: '/docs/' },
      { label: 'Install, upgrade and uninstall', link: '/docs/install/' },
    ],
  },
  {
    label: 'Use',
    items: [
      { label: 'When something is blocked', link: '/docs/blocked/' },
      { label: 'AI agents and MCP', link: '/docs/agents/' },
      { label: 'Node.js and Bun versions', link: '/docs/versions/' },
      { label: 'Configuration', link: '/docs/policy/' },
    ],
  },
  {
    label: 'Understand',
    items: [
      { label: 'How it works', link: '/docs/containment/' },
      { label: 'Limitations', link: '/docs/limitations/' },
    ],
  },
  {
    label: 'Reference',
    items: [
      { label: 'Commands', link: '/docs/commands/' },
    ],
  },
];
