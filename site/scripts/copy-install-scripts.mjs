// Serve the install scripts from the site's own domain.
//
// A documented one-liner that points at raw.githubusercontent.com runs to
// about 76 characters and gets ellipsised in the hero. Copying the scripts
// into public/ lets the command become `irm https://<domain>/install.ps1 | iex`.
//
// Copied at build time rather than committed, so the served copy cannot drift
// from the one in the repository. Nothing here rewrites the documented URLs:
// that switch waits until the domain is confirmed serving them, because a
// shortened command that 404s is worse than a long one that works.
//
//   node ./scripts/copy-install-scripts.mjs
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const siteRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

// The names a repository conventionally gives these. A product that ships
// only one of them, or neither, is not an error: this is an optimisation of a
// URL, not a build step anything depends on.
const NAMES = ['install.ps1', 'install.sh'];

// The site is the repository in the template and a `site/` subtree inside a
// product, so the scripts sit either beside the site or one level up. Both are
// searched for the same reason changelog-dates.mjs searches both: a path in
// config is wrong for whichever layout it was not written for.
const roots = [siteRoot, path.join(siteRoot, '..')];

const copied = [];
for (const name of NAMES) {
  const from = roots.map((root) => path.join(root, name)).find((p) => fs.existsSync(p));
  if (!from) continue;
  fs.copyFileSync(from, path.join(siteRoot, 'public', name));
  copied.push(name);
}

if (!copied.length) {
  // Said out loud rather than passed over in silence. A product that expects
  // its installer to be served would otherwise get a 404 at the shortened URL
  // with nothing in the build log pointing at why.
  console.log(
    `copy-install-scripts: none of ${NAMES.join(', ')} found beside the site or above it; nothing copied.`,
  );
} else {
  for (const name of copied) console.log(`copy-install-scripts: public/${name}`);
}
