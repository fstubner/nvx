// Build public/assets/wordmark.png and wordmark-light.png.
//
// The hexagon and its gradient are lifted verbatim from public/favicon.svg,
// which is the point of this script. The shipped wordmark carried a purple
// ramp while the tab icon was blue to purple, so the two disagreed, and a tab
// icon that does not match the site reads as a different product.
//
// Rasterised rather than shipped as SVG on purpose. The lettering is a system
// font, and an <img> pointing at an SVG renders it with whatever font the
// viewer's machine has, which is not a decision to hand to the reader. sharp
// bakes the glyphs here, the same way scripts/hero-image.mjs does.
//
// Two files because the lettering has to invert. The bars swap them by theme
// via `branding.wordmarkLight`; the hexagon is identical in both.
//
// NO transparent column down the left edge. --ui-mark-inset-ratio is 0 and
// scripts/wordmark-inset.mjs fails the build if the asset grows a margin the
// token does not compensate for, so the hexagon's outer stroke sits at x=0.
//
//   node ./scripts/wordmark.mjs
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import sharp from 'sharp';

const siteRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const out = (name) => path.join(siteRoot, 'public', 'assets', name);

// Matches the existing asset, so the bars' width tokens and the inset guard
// need no adjustment.
const W = 480;
const H = 207;

// favicon.svg's ramp, unchanged.
const FROM = '#5b9bd5';
const TO = '#c34fd0';

// favicon.svg's hexagon, in its own 64-unit viewBox.
const HEX = 'M32 12 L49 22 L49 42 L32 52 L15 42 L15 22 Z';
const HEX_STROKE = 5;

// The path spans x 15..49 and y 12..52, and the stroke adds half its width on
// every side, so the drawn shape is x 12.5..51.5 and y 9.5..54.5.
const BOX = { x0: 12.5, y0: 9.5, x1: 51.5, y1: 54.5 };
// Full bleed vertically. The asset this replaced filled its canvas (content
// 479x206 of 480x207); a first pass at 150 left 133 empty columns on the right
// and rendered the mark 28% smaller at the bar's fixed 160px width.
const MARK_H = H;
const SCALE = MARK_H / (BOX.y1 - BOX.y0);
const MARK_W = (BOX.x1 - BOX.x0) * SCALE;
// x: left edge flush with 0. y: centred in the canvas.
const TX = -BOX.x0 * SCALE;
const TY = (H - MARK_H) / 2 - BOX.y0 * SCALE;

const FONT = "'Segoe UI Semibold','Segoe UI',Inter,system-ui,-apple-system,sans-serif";
const GAP = 30;
const TEXT_X = MARK_W + GAP;
// Sized so the lettering reaches the right edge. Checked by the bounds report
// this script prints, not by eye.
const FONT_SIZE = 168;

function svg(letterFill) {
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${W}" height="${H}" viewBox="0 0 ${W} ${H}">
  <defs>
    <linearGradient id="m" x1="0%" y1="0%" x2="100%" y2="100%">
      <stop offset="0%" stop-color="${FROM}"/>
      <stop offset="100%" stop-color="${TO}"/>
    </linearGradient>
  </defs>
  <g transform="translate(${TX.toFixed(2)} ${TY.toFixed(2)}) scale(${SCALE.toFixed(4)})">
    <path d="${HEX}" fill="none" stroke="url(#m)"
          stroke-width="${HEX_STROKE}" stroke-linejoin="round"/>
  </g>
  <text x="${TEXT_X.toFixed(1)}" y="${H / 2}" dominant-baseline="central"
        font-family="${FONT}" font-size="${FONT_SIZE}" font-weight="600"
        letter-spacing="-2" fill="${letterFill}">nvx</text>
</svg>`;
}

// White on the dark bar, near-black on the light one. The hexagon is the same
// in both: its ramp clears 3:1 on either ground, and a logotype is exempt from
// the text floor anyway.
const variants = [
  ['wordmark.png', '#ffffff'],
  ['wordmark-light.png', '#111827'],
];

for (const [name, fill] of variants) {
  await sharp(Buffer.from(svg(fill))).png().toFile(out(name));
  // Bounds reported rather than trusted. The inset guard only checks the LEFT
  // edge, so trailing transparency is invisible to it and silently shrinks the
  // rendered mark at the bar's fixed width.
  const { data, info } = await sharp(out(name)).ensureAlpha().raw()
    .toBuffer({ resolveWithObject: true });
  let minX = info.width, maxX = -1, minY = info.height, maxY = -1;
  for (let y = 0; y < info.height; y++) {
    for (let x = 0; x < info.width; x++) {
      if (data[(y * info.width + x) * info.channels + 3] > 8) {
        if (x < minX) minX = x;
        if (x > maxX) maxX = x;
        if (y < minY) minY = y;
        if (y > maxY) maxY = y;
      }
    }
  }
  console.log(`  wrote public/assets/${name} ${info.width}x${info.height}` +
    ` content ${maxX - minX + 1}x${maxY - minY + 1}` +
    ` trailing ${info.width - 1 - maxX} col(s)`);
}
