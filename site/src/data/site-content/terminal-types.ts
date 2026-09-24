// The hero terminal image's shape, in its own file so types.ts stays under
// the 300-line guard.

/** Which ink a run of text takes, resolved from the `--ui-ondark-*` tokens.
 *
 *  Roles rather than hex, so the image and the page cannot disagree about
 *  what "the accent" is. `scripts/terminal-image.mjs` reads the real values
 *  out of the stylesheets at build time, so changing a token rebuilds the
 *  picture to match. */
export type TerminalInk =
  /** Body output. `--ui-ondark-text`. */
  | 'text'
  /** Secondary output, and the panel's own title. `--ui-ondark-text-muted`. */
  | 'muted'
  /** The outcome a reader scans for. `--ui-ondark-text-strong`. */
  | 'strong'
  /** The `$` or `>` before a typed command. `--ui-ondark-accent`. */
  | 'prompt'
  /** The command itself, so it reads as input rather than output. */
  | 'command'
  /** Your product's own voice in the output. `--ui-ondark-accent-bright`. */
  | 'accent'
  /** Status glyphs, matching what a real command line prints in ANSI
   *  32/33/31/36. `--ui-ondark-ok` / `-warn` / `-err` / `-info`. */
  | 'ok'
  | 'warn'
  | 'err'
  | 'info';

/** One run of same-coloured text. `['$ ', 'prompt']`. */
export type TerminalRun = [text: string, ink: TerminalInk];

/** One line: a list of runs, or `null` for a blank line. */
export type TerminalLine = TerminalRun[] | null;

/** Which window frame the panel wears.
 *
 *  Worth a moment rather than a default. The three dots on the left are the
 *  macOS traffic lights, and a panel wearing them while its output shows
 *  Windows paths is telling a reader two different things. Pick the platform
 *  the product is actually for, or 'none' to avoid the question. */
export type TerminalChrome = 'macos' | 'windows' | 'none';

export interface Terminal {
  /** Centred in the title bar. Usually the command name. */
  title: string;
  chrome: TerminalChrome;
  /**
   * The output, as it really printed.
   *
   * This is a screenshot in everything but format, so it carries a
   * screenshot's honesty requirement: paste what the command actually
   * produced and edit only by removing. Inventing a line, or recolouring one,
   * makes the image a mockup while every reader takes it for a capture.
   */
  lines: TerminalLine[];
}
