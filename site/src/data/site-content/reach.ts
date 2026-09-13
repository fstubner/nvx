import type { ReachNote, ReachRow, SectionCopy } from './types';

// What a package install can and cannot open, before and after nvx.
//
// Every row here is a claim about enforcement, so every row has to match
// docs/enforcement-matrix.md. That file states which cells are measured on
// real hardware and which are read off a generated profile; this page is a
// summary of it and must not be stronger than it.
//
// The macOS caveat below is not a footnote to be trimmed. The matrix records
// READ_OUTSIDE=ALLOWED there, pinned by CI so that tightening the profile
// fails the build rather than quietly making this page right.
export const reachCopy: SectionCopy = {
  heading: 'What an install can actually reach',
  leadHtml:
    'A package install runs code you did not write, as you, with whatever you can open. The difference nvx makes is a list, not an adjective — and the <a href="https://github.com/fstubner/nvx/blob/main/docs/enforcement-matrix.md">enforcement matrix</a> says which rows are measured and which are read off a profile.',
};

// Left column is what any postinstall script can open on a normal machine;
// right column is the same target from inside a contained install.
export const reachRows: ReachRow[] = [
  {
    target: '~/.ssh/id_ed25519',
    what: 'your private keys',
    plain: 'readable',
    contained: 'denied',
  },
  {
    target: '~/.npmrc',
    what: 'your publish token',
    plain: 'readable',
    contained: 'denied',
  },
  {
    target: '~/.aws/credentials',
    what: 'cloud keys',
    plain: 'readable',
    contained: 'denied',
  },
  {
    target: '~/',
    what: 'everything else you own',
    plain: 'readable',
    contained: 'denied',
  },
  {
    target: 'any host on the internet',
    what: 'wherever it wants to send them',
    plain: 'reachable',
    contained: 'refused by name',
  },
];

// What it keeps, so the section does not read as though nothing works.
export const reachAllowed: ReachRow[] = [
  { target: './', what: 'the project directory', plain: '', contained: 'read and write' },
  { target: './node_modules', what: 'and the lockfile', plain: '', contained: 'read and write' },
  { target: '$HOME', what: 'a throwaway profile, not yours', plain: '', contained: 'redirected' },
  {
    target: 'registry.npmjs.org',
    what: 'and the OSV vulnerability API',
    plain: '',
    contained: 'allowed',
  },
];

// Stated on the page, not in a tooltip. A visitor on a Mac is reading a
// narrower product than a visitor on Windows, and finding that out later is
// the outcome this section exists to prevent.
export const reachNote: ReachNote = {
  heading: 'macOS contains writes and egress, not reads',
  bodyHtml:
    'The Seatbelt profile has to allow filesystem reads: the dynamic linker loads system libraries whose locations move between macOS versions, and a strict read allowlist stops a process launching at all. So on macOS the four rows above can still be read by absolute path. Write containment, egress control and environment scrubbing are enforced there.',
  measuredHtml:
    'Linux is measured on real hardware — a contained process reports <code>READ_OUTSIDE=DENIED</code> and <code>EGRESS=DENIED</code>, with <code>WRITE_INSIDE=ALLOWED</code> as the positive control that tells enforcement from a sandbox that failed to start. Windows is asserted by probe tests run before a release.',
};
