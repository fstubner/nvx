// Build a terminal panel PNG from src/data/site-content/terminal.ts.
//
//   node ./scripts/terminal-image.mjs public/assets/hero.png
//
// Rasterised rather than shipped as SVG. The lettering is a system monospace
// stack, and an <img> pointing at an SVG renders it with whatever font the
// viewer happens to have, which is not a decision to hand to the reader.
// sharp bakes the glyphs.
//
// Colours are NOT written here. They are read out of the stylesheets at build
// time, so the panel and the page cannot drift apart: change --ui-ondark-ok
// and the next build repaints the tick. That is the whole reason the content
// file names inks by role rather than by hex.
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import sharp from 'sharp';

const siteRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const out = process.argv[2] ?? path.join(siteRoot, 'public', 'assets', 'hero.png');

/* ---- Colours, from the stylesheets ------------------------------------- */

// A regex rather than a CSS parser. These are flat `--name: #hex;` custom
// properties in a `:root` block, and a parser would be a dependency bought to
// read something that has never needed one. If a token ever becomes a
// calc() or a var() chain this will return it verbatim and the SVG will
// render nothing, which is loud enough to notice.
function readTokens(...files) {
  const found = {};
  for (const file of files) {
    const css = fs.readFileSync(path.join(siteRoot, file), 'utf8');
    for (const [, name, value] of css.matchAll(/--([\w-]+):\s*([^;]+);/g)) {
      found[name] = value.trim();
    }
  }
  return found;
}

const tokens = readTokens('src/styles/landing/tokens.css', 'src/styles/code-surface.css');

const INK = {
  text: 'ui-ondark-text',
  muted: 'ui-ondark-text-muted',
  strong: 'ui-ondark-text-strong',
  prompt: 'ui-ondark-accent',
  // Input, painted brighter than output so a reader can tell the two apart at
  // a glance without reading either.
  command: 'ui-ondark-text-strong',
  accent: 'ui-ondark-accent-bright',
  ok: 'ui-ondark-ok',
  warn: 'ui-ondark-warn',
  err: 'ui-ondark-err',
  info: 'ui-ondark-info',
};

// The panel's own surface, the same one the page paints behind inline code,
// and the surface every contrast number in tokens.css is measured against.
const panelBg = tokens['ui-code-bg'];
if (!panelBg) {
  console.error('terminal-image: --ui-code-bg is not defined in the stylesheets.');
  process.exit(1);
}

function colour(ink) {
  const name = INK[ink];
  if (!name) {
    console.error(`terminal-image: unknown ink '${ink}'. Known: ${Object.keys(INK).join(', ')}`);
    process.exit(1);
  }
  const value = tokens[name];
  if (!value) {
    console.error(`terminal-image: --${name} is not defined in the stylesheets.`);
    process.exit(1);
  }
  return value;
}

/* ---- Geometry ----------------------------------------------------------- */

// A 2x asset for a panel that displays at roughly 555px in a split hero, so
// the type has to be sized against the DISPLAYED width rather than the
// canvas. At FS=21 on a 1200px canvas the terminal renders at 21 * (555/1200)
// = 9.7px on screen, which is what makes an unadjusted panel look shrunken.
// FS=28 lands at 12.9px, matching body copy beside it.
const W = 1200;
const FS = 28, LH = 39, PAD = 53;
const BAR = 59;            // title bar height, scaled with the type
const TEXT_TOP = 117;      // first baseline, clear of the bar
const FONT = "Consolas, 'Cascadia Mono', 'DejaVu Sans Mono', monospace";

// Read the content module's object literal rather than importing it. Node
// cannot import a .ts file without a loader flag on every version this
// template supports, and the alternative -- a build step that emits JSON for
// one small file -- is more machinery than reading it costs. The literal is
// plain data: strings, arrays, null.
function readTerminalContent() {
  const file = path.join(siteRoot, 'src/data/site-content/terminal.ts');
  const src = fs.readFileSync(file, 'utf8');
  const decl = src.indexOf('export const terminal');
  if (decl === -1) {
    console.error(`terminal-image: no \`export const terminal\` in ${file}.`);
    process.exit(1);
  }
  const body = src.slice(decl);
  const literal = body.slice(body.indexOf('{'), body.lastIndexOf('};') + 1);
  return new Function(`return (${literal});`)();
}

const terminal = readTerminalContent();

// Height follows the content, so a shorter excerpt does not leave a band of
// empty panel under it.
const H = TEXT_TOP + terminal.lines.length * LH + PAD - Math.round(LH / 2);

const esc = (s) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

let body = '';
terminal.lines.forEach((runs, i) => {
  if (!runs) return;
  const y = TEXT_TOP + i * LH;
  body += `<text x="${PAD}" y="${y}" font-family="${FONT}" font-size="${FS}" xml:space="preserve">`;
  for (const [t, ink] of runs) body += `<tspan fill="${colour(ink)}">${esc(t)}</tspan>`;
  body += '</text>\n';
});

/* The window frame. macOS puts its controls on the LEFT and Windows on the
   right, which is the whole of the difference and the reason this is a choice
   rather than a constant -- see TerminalChrome in terminal-types.ts. */
const GREY = '#6b7280';
let chrome = '';
if (terminal.chrome === 'macos') {
  chrome = [35, 64, 93]
    .map((cx) => `<circle cx="${cx}" cy="29" r="8" fill="${GREY}"/>`)
    .join('\n  ');
} else if (terminal.chrome === 'windows') {
  // Minimise, maximise, close, drawn as strokes rather than glyphs so they do
  // not depend on a font carrying them.
  const right = W - 40;
  chrome = [
    `<line x1="${right - 88}" y1="30" x2="${right - 74}" y2="30" stroke="${GREY}" stroke-width="2"/>`,
    `<rect x="${right - 51}" y="23" width="14" height="14" fill="none" stroke="${GREY}" stroke-width="2"/>`,
    `<line x1="${right - 14}" y1="23" x2="${right}" y2="37" stroke="${GREY}" stroke-width="2"/>`,
    `<line x1="${right}" y1="23" x2="${right - 14}" y2="37" stroke="${GREY}" stroke-width="2"/>`,
  ].join('\n  ');
}

const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="${W}" height="${H}">
  <rect x="0" y="0" width="${W}" height="${H}" fill="${panelBg}"/>
  <rect x="0" y="0" width="${W}" height="${BAR}" fill="rgba(255,255,255,0.04)"/>
  ${chrome}
  <text x="${W / 2}" y="37" text-anchor="middle"
        font-family="${FONT}" font-size="20" fill="${colour('muted')}">${esc(terminal.title)}</text>
${body}</svg>`;

fs.mkdirSync(path.dirname(out), { recursive: true });
await sharp(Buffer.from(svg)).png().toFile(out);
const meta = await sharp(out).metadata();
console.log(`terminal-image: wrote ${out} ${meta.width}x${meta.height}`);
