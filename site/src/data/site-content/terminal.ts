import type { Terminal } from './terminal-types';

// The hero terminal panel, rendered to public/assets/hero.png by
// `npm run assets:terminal`.
//
// Sample content, like every other file in this directory, and
// check-content.mjs fails the build while REPLACE_ME is still here.
//
// An image rather than markup on purpose. The panel is the largest thing in
// the hero on a split layout, so it is usually the Largest Contentful Paint,
// and a PNG with intrinsic dimensions paints once instead of reflowing as a
// webfont loads. It also cannot be selected, copied and pasted into a shell,
// which a block of real-looking commands invites and which is a good thing to
// prevent when the commands are an excerpt rather than a script.
//
// Two things to hold to when you replace this.
//
// Run the commands and paste what they print. Every line here should be
// output your product actually produced. Edit only by removing -- redact a
// username, drop a line that reflects the capture shell rather than the
// product -- because a reader takes this for a capture whether or not it is
// one, and a line you wrote by hand is a claim you have not checked.
//
// Keep it short enough to read at the size it renders. The panel displays at
// roughly half the canvas width in a split hero, so the type is smaller than
// it looks here. Eleven lines is about the ceiling before the last ones stop
// being read.
// nvx renders the hero terminal as text (hero.ts, heroTerminalHtml), so this
// draws only public/assets/hero.png, the social card image. Same capture as
// the text panel: nvx 0.6.0 on Windows, 2026-09-24, edited only by removing
// lines (see the note in hero.ts).
export const terminal: Terminal = {
  title: 'nvx',
  chrome: 'windows',
  lines: [
    [['$ ', 'prompt'], ['cd new-project', 'command']],
    // One printed line, wrapped where a terminal this wide would wrap it. The
    // renderer clips rather than wraps, which cut it off mid-word.
    [['? ', 'warn'], ['Directory requires Node.js 22 (from .nvmrc), but it is not installed.', 'text']],
    [['Install it now? ', 'text'], ['[y/N]: ', 'muted'], ['y', 'command']],
    [['ℹ ', 'info'], ['Verifying checksum for node-v22.23.2-win-x64.zip...', 'text']],
    [['✓ ', 'ok'], ['Checksum verified successfully.', 'strong']],
    [['✓ ', 'ok'], ['Node.js v22.23.2 installed successfully', 'text']],
    [['ℹ ', 'info'], ['[nvx] Found .nvmrc: switching to Node.js v22.23.2', 'text']],
    null,
    [['$ ', 'prompt'], ['npm install sample-package', 'command']],
    [['ℹ ', 'info'], ['Running in native sandbox: npm install sample-package', 'strong']],
    [['added 1 package, and audited 2 packages in 1s', 'text']],
    [['found 0 vulnerabilities', 'text']],
  ],
};
