// Build public/assets/wordmark.png and wordmark-light.png from public/mark.svg
// and the site name.
//
//   node ./scripts/wordmark.mjs
//
// The mark is a file, not code. Drawing one in a script means a script that
// knows about hexagons or squares or whatever the next product's logo is, and
// the only way to parameterise that is to invent a drawing language. An SVG
// already is one. So the bespoke half lives in public/mark.svg and everything
// here is the part that is the same for every product: set the mark at a
// height, put the name beside it, emit one variant per theme, and measure the
// result.
//
// Rasterised rather than shipped as SVG. The lettering is a system font stack,
// and an <img> pointing at an SVG renders it with whatever font the viewer
// happens to have, which is not a decision to hand to the reader. sharp bakes
// the glyphs.
//
// Two variants because the bar is dark in one theme and light in the other,
// and only the LETTERING changes: the mark is composited unchanged into both,
// which is why public/mark.svg says not to draw it in a single-theme ink.
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import sharp from 'sharp';

const siteRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const asset = (name) => path.join(siteRoot, 'public', 'assets', name);

/* ---- What varies per product -------------------------------------------- */

const markFile = path.join(siteRoot, 'public', 'mark.svg');
if (!fs.existsSync(markFile)) {
  console.error(`wordmark: no mark at ${markFile}.`);
  process.exit(1);
}

// The name, from the same file every page reads it from, so the wordmark and
// the <title> cannot disagree.
const meta = fs.readFileSync(path.join(siteRoot, 'src/data/site-content/meta.ts'), 'utf8');
const nameMatch = meta.match(/siteName:\s*'([^']*)'/) ?? meta.match(/siteName:\s*"([^"]*)"/);
if (!nameMatch) {
  console.error('wordmark: no `siteName` in src/data/site-content/meta.ts.');
  process.exit(1);
}
const NAME = nameMatch[1];

/* ---- Typography ---------------------------------------------------------- */

// A 480x207 canvas at roughly 3x the 160x32 the bars render it at, so it stays
// sharp on a high-DPI display without being a large file. MARK_H drives the
// canvas height: the mark is the tallest thing in the lockup.
const MARK_H = 190;
const GAP = 26;
const PAD = 4; // room for the mark's own stroke to not clip at the edge
const FONT = "'Segoe UI Semibold','Segoe UI',Inter,system-ui,-apple-system,sans-serif";
const FONT_SIZE = 178;
const LETTER_SPACING = -1;

// Light-bar ink and dark-bar ink. Not tokens: these are the two extremes of
// the ramp rather than a themed value, and a wordmark rendered in the brand
// accent competes with the mark beside it.
const VARIANTS = [
  { file: 'wordmark.png', ink: '#ffffff' },   // for the dark bar
  { file: 'wordmark-light.png', ink: '#111827' }, // for the light bar
];

const esc = (s) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

/* ---- Compose ------------------------------------------------------------- */

const mark = await sharp(markFile).resize({ height: MARK_H }).png().toBuffer();
const markMeta = await sharp(mark).metadata();

// Render the lettering on a canvas wide enough for anything, then trim to its
// real bounds. This is why there is no hand-tuned baseline nudge here: a name
// with no ascender or descender sits high in its em box, so centring on the em
// box puts the lettering above the mark's centre line. Trimming measures where
// the glyphs actually are, which is correct for every string rather than for
// the one it was tuned against.
const TEXT_CANVAS_W = 4000;
const TEXT_CANVAS_H = Math.round(FONT_SIZE * 2);
const textSvg = `<svg xmlns="http://www.w3.org/2000/svg" width="${TEXT_CANVAS_W}" height="${TEXT_CANVAS_H}">
  <text x="0" y="${TEXT_CANVAS_H / 2}" dominant-baseline="central"
        font-family="${FONT}" font-size="${FONT_SIZE}" font-weight="600"
        letter-spacing="${LETTER_SPACING}" fill="INK">${esc(NAME)}</text>
</svg>`;

/** Opaque bounding box, so alignment is measured rather than assumed. */
function bounds(data, info, x0, x1) {
  let minX = info.width, maxX = -1, minY = info.height, maxY = -1;
  for (let y = 0; y < info.height; y++) {
    for (let x = x0; x < x1; x++) {
      if (data[(y * info.width + x) * info.channels + 3] > 8) {
        if (x < minX) minX = x;
        if (x > maxX) maxX = x;
        if (y < minY) minY = y;
        if (y > maxY) maxY = y;
      }
    }
  }
  return { minX, maxX, minY, maxY };
}

for (const { file, ink } of VARIANTS) {
  const text = await sharp(Buffer.from(textSvg.replace('INK', ink)))
    .png()
    .trim({ threshold: 1 })
    .toBuffer();
  const textMeta = await sharp(text).metadata();

  const markLeft = PAD;
  const textLeft = markLeft + markMeta.width + GAP;
  const W = textLeft + textMeta.width + PAD;
  const H = MARK_H + PAD * 2;

  await sharp({
    create: { width: W, height: H, channels: 4, background: { r: 0, g: 0, b: 0, alpha: 0 } },
  })
    .composite([
      { input: mark, left: markLeft, top: PAD },
      // Centred against the mark, which is what the trim above makes possible.
      { input: text, left: textLeft, top: Math.round((H - textMeta.height) / 2) },
    ])
    .png()
    .toFile(asset(file));

  // Reported rather than trusted. scripts/wordmark-inset.mjs checks the LEFT
  // edge only, so trailing transparency is invisible to it and silently
  // shrinks the mark at the bar's fixed width, and nothing at all checks that
  // the lettering shares a centre line with the mark.
  const { data, info } = await sharp(asset(file)).ensureAlpha().raw()
    .toBuffer({ resolveWithObject: true });
  const all = bounds(data, info, 0, info.width);
  const markBox = bounds(data, info, 0, markLeft + markMeta.width + 1);
  const textBox = bounds(data, info, textLeft, info.width);
  const markMid = (markBox.minY + markBox.maxY) / 2;
  const textMid = (textBox.minY + textBox.maxY) / 2;

  console.log(`wordmark: wrote public/assets/${file} ${info.width}x${info.height}`);
  console.log(`  content ${all.maxX - all.minX + 1}x${all.maxY - all.minY + 1}` +
    `, leading ${all.minX} col(s), trailing ${info.width - 1 - all.maxX} col(s)`);
  console.log(`  mark mid y ${markMid.toFixed(1)}, text mid y ${textMid.toFixed(1)}` +
    `, off by ${(textMid - markMid).toFixed(1)}px`);
}
