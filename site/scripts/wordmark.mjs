// Build public/assets/wordmark.png and wordmark-light.png.
//
// The mark is two concentric hexagon rings, which is the logo as it has always
// been drawn. public/favicon.svg reduces that to ONE ring on purpose, because
// an inner ring closes up at 16px, so the favicon is not the shape to copy at
// 480px. What is taken from the favicon is its colour ramp, so the tab icon
// and the wordmark stop disagreeing.
//
// Geometry measured from the asset this replaces rather than guessed: outer
// ring drawn x 2..177 and y 1..205, inner x 23..156 and y 26..180, stroke 8.
// That yields a centre-to-vertex radius of 98 outside and 73 inside. The
// favicon's stroke is 5 units on a 64 viewBox, which scales here to 23 and
// would swallow the gap between the rings entirely.
//
// Rasterised rather than shipped as SVG. The lettering is a system font, and
// an <img> pointing at an SVG renders it with whatever font the viewer has,
// which is not a decision to hand to the reader. sharp bakes the glyphs, the
// same way scripts/hero-image.mjs does.
//
// NO transparent column down the left edge. --ui-mark-inset-ratio is 0 and
// scripts/wordmark-inset.mjs fails the build if the asset grows a margin the
// token does not compensate for.
//
//   node ./scripts/wordmark.mjs
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import sharp from 'sharp';

const siteRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const out = (name) => path.join(siteRoot, 'public', 'assets', name);

const W = 480;
const H = 207;

// favicon.svg's ramp, unchanged. Blue into purple.
const FROM = '#5b9bd5';
const TO = '#c34fd0';

const STROKE = 8;
const R_OUT = 98;
const R_IN = 73;
// The favicon's hexagon is 34 wide by 40 tall, so half-width is 0.85 of the
// centre-to-vertex radius. Keeping that ratio keeps the same silhouette.
const ASPECT = 34 / 40;
const CX = STROKE / 2 + R_OUT * ASPECT;
const CY = H / 2;

/** Pointy-top hexagon, the same orientation as the favicon's. */
function hexagon(r) {
  const w = r * ASPECT;
  const pts = [
    [CX, CY - r],
    [CX + w, CY - r / 2],
    [CX + w, CY + r / 2],
    [CX, CY + r],
    [CX - w, CY + r / 2],
    [CX - w, CY - r / 2],
  ];
  return `M${pts.map(([x, y]) => `${x.toFixed(2)} ${y.toFixed(2)}`).join(' L')} Z`;
}

const MARK_RIGHT = CX + R_OUT * ASPECT + STROKE / 2;
const FONT = "'Segoe UI Semibold','Segoe UI',Inter,system-ui,-apple-system,sans-serif";
const GAP = 26;
const TEXT_X = MARK_RIGHT + GAP;
const FONT_SIZE = 178;
// Nudge, because `dominant-baseline` centres on the em box and "nvx" is all
// x-height: no ascender, no descender, so the glyphs sit high in that box and
// the lettering rides above the hexagon's centre line. Measured from the
// rendered bounds this script reports, not eyeballed.
const TEXT_DY = -28;

function svg(letterFill) {
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${W}" height="${H}" viewBox="0 0 ${W} ${H}">
  <defs>
    <linearGradient id="m" x1="0%" y1="0%" x2="100%" y2="100%">
      <stop offset="0%" stop-color="${FROM}"/>
      <stop offset="100%" stop-color="${TO}"/>
    </linearGradient>
  </defs>
  <g fill="none" stroke="url(#m)" stroke-width="${STROKE}" stroke-linejoin="round">
    <path d="${hexagon(R_OUT)}"/>
    <path d="${hexagon(R_IN)}"/>
  </g>
  <text x="${TEXT_X.toFixed(1)}" y="${(CY + TEXT_DY).toFixed(1)}"
        dominant-baseline="central"
        font-family="${FONT}" font-size="${FONT_SIZE}" font-weight="600"
        letter-spacing="-1" fill="${letterFill}">nvx</text>
</svg>`;
}

const variants = [
  ['wordmark.png', '#ffffff'],
  ['wordmark-light.png', '#111827'],
];

/** Opaque bounding box of a region, so alignment is measured not assumed. */
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

for (const [name, fill] of variants) {
  await sharp(Buffer.from(svg(fill))).png().toFile(out(name));
  const { data, info } = await sharp(out(name)).ensureAlpha().raw()
    .toBuffer({ resolveWithObject: true });
  // Reported rather than trusted. The inset guard checks the LEFT edge only,
  // so trailing transparency is invisible to it and silently shrinks the mark
  // at the bar's fixed width, and nothing at all checks that the lettering
  // shares a centre line with the hexagon.
  const all = bounds(data, info, 0, info.width);
  const mark = bounds(data, info, 0, Math.round(MARK_RIGHT) + 1);
  const text = bounds(data, info, Math.round(TEXT_X) - 4, info.width);
  const markMid = (mark.minY + mark.maxY) / 2;
  const textMid = (text.minY + text.maxY) / 2;
  console.log(`  wrote public/assets/${name} ${info.width}x${info.height}`);
  console.log(`    content ${all.maxX - all.minX + 1}x${all.maxY - all.minY + 1}` +
    ` trailing ${info.width - 1 - all.maxX} col(s)`);
  console.log(`    mark mid y ${markMid.toFixed(1)}, text mid y ${textMid.toFixed(1)}` +
    `, off by ${(textMid - markMid).toFixed(1)}px`);
}
