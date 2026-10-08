import type { DocsSection } from './types';

export const docsTitle = 'nvx docs';

export const docsDescription =
  'Documentation for nvx, which runs npm install and npx inside an OS sandbox on Windows, macOS and Linux, so a package cannot read your credentials or reach a host you did not allow.';

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
    label: 'Use',
    items: [
      { label: 'Agents and CI', link: '/docs/agents/' },
      { label: 'When nvx stops something', link: '/docs/blocked/' },
    ],
  },
  {
    label: 'Security',
    items: [
      { label: 'Containment', link: '/docs/containment/' },
      { label: 'Policy', link: '/docs/policy/' },
      { label: 'Known limitations', link: '/docs/limitations/' },
    ],
  },
  {
    label: 'Reference',
    items: [
      { label: 'Commands', link: '/docs/commands/' },
    ],
  },
];
