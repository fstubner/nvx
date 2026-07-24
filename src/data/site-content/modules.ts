import type { Modules } from './types';

// Toggle optional sections on/off for this product. Turning `docs` off
// also needs the Starlight integration removed from astro.config.mjs (see
// the comment there) — this flag alone only controls nav/footer/404 links
// and in-copy references, not whether the Starlight build step runs.
export const modules: Modules = {
  docs: true,
  changelog: true,
};
