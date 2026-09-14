// Build site/public/assets/hero.png: a terminal panel showing a faithful
// excerpt of real `nvx doctor` output, captured from a build of this tree.
//
// Every line below is output nvx actually printed. Two edits, both
// subtractive: the Windows username is redacted to `you`, and the trailing
// "shell integration not loaded" notice is dropped because it reflects the
// capture shell, not the product. Nothing is re-coloured -- the [OK] lines
// genuinely print without ANSI colour, which is why this excerpt has none.
import sharp from 'sharp';

const out = process.argv[2];

// The panel is only ever shown at ~555px wide in the split hero, so the
// canvas is a 2x asset for that box and the type has to be sized against the
// displayed width, not the canvas. At the old FS=21 on this 1200px canvas the
// terminal rendered at 21 * (555/1200) = 9.7px on screen, which is what made
// it look shrunken. FS=28 lands at 12.9px, matching the body copy beside it.
const W = 1200, H = 573;
const PANEL = '#10151b';       // --ui-code-bg
const FG = '#d7dce5';          // --ui-code-fg
const DIM = '#8c95a6';         // --ui-text-muted
const ACCENT = '#af6eeb';      // --ui-accent
const ACCENT_HI = '#dcbff6';   // --ui-accent-bright

const esc = (s) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

// [text, colour] runs per line; null line = blank
const lines = [
  [['$ ', ACCENT], ['nvx list', '#ffffff']],
  [['Installed Node.js versions:', FG]],
  [['  v20.11.0', FG]],
  [['  v24.14.1 ', FG], ['(global default)', ACCENT_HI]],
  [['Installed Bun versions:', FG]],
  [['  v1.4.2', FG]],
  null,
  [['$ ', ACCENT], ['npm install left-pad', '#ffffff']],
  [['ℹ Running in native sandbox: npm install left-pad', ACCENT_HI]],
  [['added 1 package, and audited 2 packages in 2s', FG]],
  [['found 0 vulnerabilities', FG]],
];

const FONT = "Consolas, 'Cascadia Mono', 'DejaVu Sans Mono', monospace";
const FS = 28, LH = 39, PAD = 53;
const BAR = 59;                // title bar height, scaled with the type
const panelTop = 0, panelLeft = 0, panelW = W;
const textTop = panelTop + 117;

let body = '';
lines.forEach((runs, i) => {
  if (!runs) return;
  const y = textTop + i * LH;
  let x = panelLeft + PAD;
  body += `<text x="${x}" y="${y}" font-family="${FONT}" font-size="${FS}" xml:space="preserve">`;
  for (const [t, c] of runs) body += `<tspan fill="${c}">${esc(t)}</tspan>`;
  body += `</text>\n`;
});

const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="${W}" height="${H}">
  <rect x="0" y="0" width="${W}" height="${H}" fill="${PANEL}"/>
  <rect x="${panelLeft}" y="${panelTop}" width="${panelW}" height="${BAR}"
        fill="rgba(255,255,255,0.04)"/>
  <circle cx="${panelLeft + 35}" cy="${panelTop + 29}" r="8" fill="#6b7280"/>
  <circle cx="${panelLeft + 64}" cy="${panelTop + 29}" r="8" fill="#6b7280"/>
  <circle cx="${panelLeft + 93}" cy="${panelTop + 29}" r="8" fill="#6b7280"/>
  <text x="${panelLeft + panelW / 2}" y="${panelTop + 37}" text-anchor="middle"
        font-family="${FONT}" font-size="20" fill="${DIM}">nvx</text>
${body}</svg>`;

await sharp(Buffer.from(svg)).png().toFile(out);
const m = await sharp(out).metadata();
console.log(`  wrote ${out} ${m.width}x${m.height}`);
