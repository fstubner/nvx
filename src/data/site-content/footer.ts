import type { Analytics, BuiltWithEntry, SocialProof } from './types';

// The "Built with" row in the footer. Credit what the product is made of,
// not what the site is made of.
export const builtWith: BuiltWithEntry[] = [
  { name: 'Rust', url: 'https://www.rust-lang.org/' },
  { name: 'clap', url: 'https://docs.rs/clap/' },
];

// Star and download counts are fetched from this repo at runtime. A repo
// that does not exist simply leaves the counters hidden.
export const social: SocialProof = { repo: 'your-org/example' };

// No analytics token: the beacon is left out of every build. Add
// `cloudflareToken` to turn it on.
export const analytics: Analytics = {};
