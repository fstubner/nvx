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

const W = 1200, H = 720;
const BG = '#111111';          // --ui-bg
const PANEL = '#10151b';       // --ui-code-bg
const BORDER = 'rgba(255,255,255,0.10)';
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
  [['$ ', ACCENT], ['nvx doctor', '#ffffff']],
  [['nvx doctor \u2014 shim interception', FG]],
  [['  shim dir: C:\\Users\\you\\.nvx\\bin', DIM]],
  [['  [OK]   shim dir is on PATH at position 63, with no raw-runtime dir ahead of it', FG]],
  [['  commands:', DIM]],
  [['    [OK]  node -> C:\\Users\\you\\.nvx\\bin\\node.exe', FG]],
  [['    [OK]  npm -> C:\\Users\\you\\.nvx\\bin\\npm.exe', FG]],
  [['    [OK]  npx -> C:\\Users\\you\\.nvx\\bin\\npx.exe', FG]],
  [['    [OK]  bun -> C:\\Users\\you\\.nvx\\bin\\bun.exe', FG]],
  [['    [OK]  bunx -> C:\\Users\\you\\.nvx\\bin\\bunx.exe', FG]],
];

const FONT = "Consolas, 'Cascadia Mono', 'DejaVu Sans Mono', monospace";
const FS = 21, LH = 29, PAD = 40;
const panelTop = 90, panelLeft = 60, panelW = W - 120, panelH = H - 160;
const textTop = panelTop + 70;

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
  <defs>
    <linearGradient id="glow" x1="0%" y1="0%" x2="100%" y2="100%">
      <stop offset="0%" stop-color="#a855f7" stop-opacity="0.30"/>
      <stop offset="100%" stop-color="#ff007f" stop-opacity="0.18"/>
    </linearGradient>
  </defs>
  <rect width="${W}" height="${H}" fill="${BG}"/>
  <rect x="${panelLeft - 10}" y="${panelTop - 10}" width="${panelW + 20}" height="${panelH + 20}"
        rx="18" fill="url(#glow)"/>
  <rect x="${panelLeft}" y="${panelTop}" width="${panelW}" height="${panelH}"
        rx="12" fill="${PANEL}" stroke="${BORDER}"/>
  <rect x="${panelLeft}" y="${panelTop}" width="${panelW}" height="44"
        rx="12" fill="rgba(255,255,255,0.04)"/>
  <circle cx="${panelLeft + 26}" cy="${panelTop + 22}" r="6" fill="#6b7280"/>
  <circle cx="${panelLeft + 48}" cy="${panelTop + 22}" r="6" fill="#6b7280"/>
  <circle cx="${panelLeft + 70}" cy="${panelTop + 22}" r="6" fill="#6b7280"/>
  <text x="${panelLeft + panelW / 2}" y="${panelTop + 28}" text-anchor="middle"
        font-family="${FONT}" font-size="15" fill="${DIM}">nvx</text>
${body}</svg>`;

await sharp(Buffer.from(svg)).png().toFile(out);
const m = await sharp(out).metadata();
console.log(`  wrote ${out} ${m.width}x${m.height}`);
