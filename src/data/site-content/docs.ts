import type { DocsSection } from './types';

// The docs section's own copy and structure. Starlight reads these from
// astro.config.mjs; they live here because a product's docs are content.
//
// Nothing checks that a `link` resolves: Starlight builds an entry pointing
// at a page that does not exist without complaining, so an entry left behind
// after deleting its page becomes a dead link in every page's sidebar.
export const docsTitle = 'Example docs';

export const docsDescription =
  'Documentation for Example, a small command-line tool for Windows, macOS and Linux.';

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
