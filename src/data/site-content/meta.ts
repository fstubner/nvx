import type { AppSchema, Branding, Meta } from './types';

// Sample content. Everything in this directory describes one fictional
// product -- "Example", a command-line tool -- so the site builds, renders
// and passes its own checks out of the box. Replace it; the shell reads
// these files and nothing else.

export const meta: Meta = {
  domain: 'https://example.com',
  // Aim for a title under about 60 characters: that is what a search result
  // shows. Lead with the product name, then what it is, in the words someone
  // would actually search for.
  title: 'Example — A Command-Line Tool for Doing One Thing',
  // 150-160 characters. Longer gets truncated mid-sentence, and the end of
  // the sentence is usually the part worth reading.
  description:
    'Example is a small command-line tool. Run it on Windows, macOS or Linux, script it from a terminal, and read its output as JSON when a program needs it.',
  // Shown when the page is shared, where there is a little more room.
  ogDescription:
    'A small command-line tool that runs on Windows, macOS and Linux, with JSON output for scripts.',
  siteName: 'Example',
  author: { name: 'Your Name', url: 'https://github.com/your-org' },
  ogImage: 'https://example.com/assets/hero.png',
  ogImageAlt: 'The Example command-line tool running in a terminal',
  faviconPath: '/favicon.svg',
  themeColor: '#111',
};

// Facts about the PRODUCT for the JSON-LD, not about this site. These were
// literals in src/layouts/Page.astro until a site shipped the wrong language
// for its own program, because no gate read a value nothing listed.
export const appSchema: AppSchema = {
  applicationCategory: 'DeveloperApplication',
  applicationSubCategory: 'REPLACE_ME',
  operatingSystem: 'Windows, macOS, Linux',
  license: 'https://opensource.org/licenses/MIT',
  programmingLanguage: 'REPLACE_ME',
  price: '0',
  priceCurrency: 'USD',
};

export const branding: Branding = {
  wordmark: '/assets/wordmark.png',
  wordmarkAlt: 'Example',
  accentGradient: 'linear-gradient(90deg,#16a34a,#22c55e 50%,#1edcff)',
  bg: '#111',
  fg: '#d4d4d4',
};
