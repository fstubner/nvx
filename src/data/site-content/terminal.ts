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
export const terminal: Terminal = {
  title: 'REPLACE_ME',
  // 'macos' draws the three traffic lights on the left, 'windows' the
  // minimise/maximise/close marks on the right. Match the platform your
  // output is from: a Mac frame around Windows paths is a small lie that
  // costs a reader a second of confusion.
  chrome: 'macos',
  lines: [
    [['$ ', 'prompt'], ['REPLACE_ME --version', 'command']],
    [['REPLACE_ME 1.0.0', 'text']],
    null,
    [['$ ', 'prompt'], ['REPLACE_ME run', 'command']],
    [['ℹ ', 'info'], ['Starting up', 'accent']],
    [['✓ ', 'ok'], ['Did the thing', 'text']],
    [['⚠ ', 'warn'], ['Something worth knowing', 'text']],
    [['Done in 2s', 'strong']],
  ],
};
