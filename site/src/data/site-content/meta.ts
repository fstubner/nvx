import type { AppSchema, Branding, Meta } from './types';

export const meta: Meta = {
  domain: 'https://nvx.run',
  // Leads with the job, not the security layer: PRODUCT.md settles that the
  // version manager is the main thing and containment the second.
  title: 'nvx · a Node.js and Bun version manager with a sandbox',
  // Kept under 160 characters so Google shows it whole. The longer version
  // this replaced ran to 271 and was truncated mid-clause in the SERP.
  description:
    'Install, switch and pin Node.js and Bun on Windows, macOS and Linux. One static binary, and every install runs inside an OS sandbox.',
  ogDescription:
    'A Node.js and Bun version manager for Windows, macOS and Linux that contains what it installs.',
  siteName: 'nvx',
  author: { name: 'Felix Stubner', url: 'https://github.com/fstubner' },
  ogImage: 'https://nvx.run/assets/hero.png',
  ogImageAlt: 'nvx switching Node.js versions and containing an npm install in a terminal',
  faviconPath: '/favicon.svg',
  themeColor: '#16161a',
};

// Taken from nvx's own logo, not the template's sample. The shipped
// assets/nvx_logo.svg declares "Deep royal purple to electric magenta":
// #8A2BE2 -> #FF007F on #16161a. The template arrives carrying netscli's
// green-to-cyan (#16a34a -> #22c55e -> #1edcff) on #111, which is the site
// this shell was generalised from -- leaving it would have made nvx's landing
// page netscli's in a different typeface.
// Facts about the product for the JSON-LD. Go, not Rust: these were
// literals in the layout until 2026-09-14, and the language was the
// template's rather than this product's.
export const appSchema: AppSchema = {
  applicationCategory: 'DeveloperApplication',
  applicationSubCategory: 'Runtime version manager',
  operatingSystem: 'Windows, macOS, Linux',
  license: 'https://opensource.org/licenses/MIT',
  programmingLanguage: 'Go',
  price: '0',
  priceCurrency: 'USD',
};

export const branding: Branding = {
  wordmark: '/assets/wordmark.png',
  wordmarkLight: '/assets/wordmark-light.png',
  wordmarkAlt: 'nvx',
  accentGradient: 'linear-gradient(90deg,#8A2BE2,#B1329F 50%,#FF007F)',
  bg: '#16161a',
  fg: '#d4d4d4',
};
