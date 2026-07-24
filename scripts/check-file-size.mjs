import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const maxLines = 300;
const transitionExceptions = new Map([
  [
    'src/scripts/docs-header.ts',
    {
      maxLines: 360,
      reason: 'docs header/search/toc behaviors; split search vs toc vs tables next',
    },
  ],
  [
    'src/styles/starlight/20-docs-shell-closeout.css',
    {
      maxLines: 320,
      reason:
        'single cohesive docs shell close-out pass split from starlight.css; not further split to avoid fragmenting one cascade-ordered concern',
    },
  ],
]);
const roots = ['src'];
const extensions = new Set(['.css', '.mjs', '.rs', '.ts', '.tsx']);
const ignoredDirs = new Set(['node_modules', 'dist']);

function walk(dir, files = []) {
  if (!fs.existsSync(dir)) return files;
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    if (entry.isDirectory()) {
      if (!ignoredDirs.has(entry.name)) {
        walk(path.join(dir, entry.name), files);
      }
      continue;
    }
    if (entry.isFile() && extensions.has(path.extname(entry.name))) {
      files.push(path.join(dir, entry.name));
    }
  }
  return files;
}

const oversized = [];
for (const root of roots) {
  for (const file of walk(path.join(repoRoot, root))) {
    const lineCount = fs.readFileSync(file, 'utf8').split(/\r?\n/).length;
    if (lineCount > maxLines) {
      const relativeFile = path.relative(repoRoot, file).replaceAll(path.sep, '/');
      const exception = transitionExceptions.get(relativeFile);
      if (exception && lineCount <= exception.maxLines) {
        continue;
      }
      oversized.push({
        lines: lineCount,
        file: relativeFile,
        reason: exception
          ? `transition cap ${exception.maxLines} exceeded: ${exception.reason}`
          : 'no transition exception',
      });
    }
  }
}

if (oversized.length > 0) {
  oversized.sort((a, b) => b.lines - a.lines);
  console.error(`Files over ${maxLines} lines:`);
  for (const item of oversized) {
    console.error(`  ${item.lines.toString().padStart(4)}  ${item.file} (${item.reason})`);
  }
  process.exitCode = 1;
}
