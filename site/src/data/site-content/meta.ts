import type { AppSchema, Branding, Meta } from './types';

export const meta: Meta = {
  domain: 'https://nvx.run',
  // Leads with the sandbox: PRODUCT.md records that the owner chose "sandbox
  // first" on 2026-10-07. The version manager is the second sentence.
  title: 'nvx · npm install and npx inside an OS sandbox',
  // Kept under 160 characters so Google shows it whole. The longer version
  // this replaced ran to 271 and was truncated mid-clause in the SERP.
  description:
    'nvx runs npm install and npx inside an OS sandbox on Windows, macOS and Linux. A package cannot read your credentials or reach a host you did not allow.',
  ogDescription:
    'nvx runs npm install and npx inside an OS sandbox on Windows, macOS and Linux, so a package cannot read your credentials or reach a host you did not allow. It also manages Node.js and Bun versions.',
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
  applicationSubCategory: 'Package install sandbox and runtime version manager',
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
