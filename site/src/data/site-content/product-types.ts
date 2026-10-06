// Content types for the parts of this site the template does not have: the
// changelog's surface map and the "what an install can reach" section.
//
// Kept out of types.ts for two reasons. types.ts has a 300-line guard
// (scripts/check-file-size.mjs), and the template's own types filled it to
// 296 in the 2026-10-04 sync. And types.ts merges with the template on every
// sync, so a file only this site has is one that never conflicts.

/** One product surface the changelog summariser can name. */
export interface ChangelogSurface {
  /** Matched against the release body. */
  pattern: RegExp;
  /** How the summary names it. */
  label: string;
}

/** One target in the "what an install can reach" comparison. */
export interface ReachRow {
  /** The path or destination, rendered as code. */
  target: string;
  /** What it is, in plain words. */
  what: string;
  /** Its state under any other version manager, which is npm's own answer. */
  plain: string;
  /** Its state from inside a contained install. */
  contained: string;
}

/** The platform caveat that sits beside the comparison, not under it. */
export interface ReachNote {
  heading: string;
  /** HTML allowed. */
  bodyHtml: string;
  /** Where the evidence for the rows comes from. HTML allowed. */
  measuredHtml: string;
}
