// Serve the install scripts from the site's own domain.
//
// The documented one-liner points at raw.githubusercontent.com, which is 76
// characters and gets ellipsised in the hero. Copying the scripts into public/
// lets the command become `irm https://<domain>/install.ps1 | iex`.
//
// Copied at build time rather than committed, so the served copy cannot drift
// from the one in the repository root. Nothing here rewrites the documented
// URLs: that switch waits until the domain is confirmed serving them, because
// a shortened command that 404s is worse than a long one that works.
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const siteRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const repoRoot = path.resolve(siteRoot, '..');

for (const name of ['install.ps1', 'install.sh']) {
  const from = path.join(repoRoot, name);
  if (!fs.existsSync(from)) {
    console.error(`copy-install-scripts: ${name} is not at the repository root.`);
    process.exit(1);
  }
  fs.copyFileSync(from, path.join(siteRoot, 'public', name));
  console.log(`copy-install-scripts: public/${name}`);
}
