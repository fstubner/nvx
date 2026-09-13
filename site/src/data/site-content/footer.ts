import type { Analytics, BuiltWithEntry, SocialProof } from './types';

// What the product is made of, not what the site is made of.
export const builtWith: BuiltWithEntry[] = [
  { name: 'Go', url: 'https://go.dev/' },
  { name: 'AppContainer', url: 'https://learn.microsoft.com/en-us/windows/win32/secauthz/appcontainer-isolation' },
  { name: 'Landlock', url: 'https://landlock.io/' },
];

export const social: SocialProof = { repo: 'fstubner/nvx' };

export const analytics: Analytics = {};
