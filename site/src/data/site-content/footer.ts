import type { Analytics, BuiltWithEntry, SocialProof } from './types';

// What the product is made of, not what the site is made of.
export const builtWith: BuiltWithEntry[] = [
  { name: 'Go', url: 'https://go.dev/' },
  { name: 'AppContainer', url: 'https://learn.microsoft.com/en-us/windows/win32/secauthz/appcontainer-isolation' },
  { name: 'Landlock', url: 'https://landlock.io/' },
  // macOS's own sandbox primitive, named everywhere else on the page
  // (docs/enforcement-matrix.md, the reach section). Missing here made this
  // list read as Windows and Linux only, which is not what nvx supports.
  { name: 'Seatbelt', url: 'https://developer.apple.com/documentation/security/app-sandbox' },
];

export const social: SocialProof = { repo: 'fstubner/nvx' };

export const analytics: Analytics = {};
