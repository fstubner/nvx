import type { ReachNote, ReachRow, SectionCopy } from './types';

// What a package install can and cannot open, before and after nvx.
//
// Every row is a claim about enforcement, so every row has to match
// docs/enforcement-matrix.md. That file states which cells are measured on real
// hardware and which are read off a generated profile; this table is a summary
// of it and must not be stronger than it.
//
// The macOS caveat below is not a footnote to be trimmed. The matrix records
// READ_OUTSIDE=ALLOWED there, pinned by CI so that tightening the profile fails
// the build rather than quietly making this page right.
export const reachCopy: SectionCopy = {
  heading: 'What an install can actually reach',
  leadHtml:
    'A package install runs code you did not write, as you, with whatever you can open. Every Node version manager leaves that unchanged, because managing versions and containing what they install are different jobs. The <a href="https://github.com/fstubner/nvx/blob/main/docs/enforcement-matrix.md">enforcement matrix</a> says which rows are measured and which are read off a profile.',
};

/** Column headings. The others share one column because they behave the same:
 *  none of them contains an install, so the answer is npm's answer. */
export const reachColumns = {
  target: 'What an install can open',
  others: 'nvm · fnm · volta · asdf',
  nvx: 'nvx',
};

export const reachRows: ReachRow[] = [
  { target: '~/.ssh/id_ed25519', what: 'your private keys', plain: 'readable', contained: 'denied' },
  { target: '~/.npmrc', what: 'your publish token', plain: 'readable', contained: 'denied' },
  { target: '~/.aws/credentials', what: 'cloud keys', plain: 'readable', contained: 'denied' },
  { target: '~/', what: 'everything else you own', plain: 'readable', contained: 'denied' },
  { target: 'any host on the internet', what: 'wherever it wants to send them', plain: 'reachable', contained: 'allowlist only' },
  { target: './ and ./node_modules', what: 'the project it is installing into', plain: 'read and write', contained: 'read and write' },
  { target: '$HOME', what: 'where the install thinks home is', plain: 'yours', contained: 'a throwaway profile' },
];

// Stated on the page, not in a tooltip. Someone reading this on a Mac is
// looking at a narrower product than someone on Windows, and finding that out
// later is the outcome this section exists to prevent.
export const reachNote: ReachNote = {
  heading: 'On macOS, reads are not contained',
  bodyHtml:
    'The Seatbelt profile has to allow filesystem reads, because the dynamic linker loads system libraries whose locations move between macOS versions, and a strict read allowlist stops a process launching at all. So on macOS the first four rows can still be read by absolute path. Write containment, egress control and environment scrubbing are enforced there.',
  measuredHtml:
    'Linux is measured on real hardware, where a contained process reports <code>READ_OUTSIDE=DENIED</code> and <code>EGRESS=DENIED</code>, with <code>WRITE_INSIDE=ALLOWED</code> as the positive control that tells enforcement from a sandbox that failed to start. Windows is asserted by probe tests run before a release.',
};
