import type { ComparisonColumn, ComparisonRow, SectionCopy } from './types';

// The comparison matrix. Every cell describes an out-of-the-box default.
//
// Rechecked against sources on 2026-09-16, which corrected four cells. Three
// of them were wrong in a competitor's favour and one hid a split:
//
//   fnm    checksums: was a tick. At 86adc96 there is no hashing crate in
//          Cargo.toml, no occurrence of SHASUMS anywhere in the repository,
//          and src/downloader.rs streams the response from http::get straight
//          into the archive extractor.
//   volta  checksums: was a tick. No hashing crate in any Cargo.toml, and
//          `// ISSUE(#134) - verify checksum` sits in four fetch files, node,
//          npm, pnpm and yarn. Issue 134 was opened in 2018.
//   asdf   was described as a shell script. It was rewritten in Go for 0.16.0
//          in January 2025.
//   asdf and mise shared a column and no longer belong in one. They differ on
//          language, on Windows, on how a switch is scoped, and on integrity.
//
// uv had a column until 2026-09-16, on the grounds that it sets the bar for a
// single fast binary doing more than versions. It is a Python tool, nobody
// choosing between these is weighing it, and it won exactly one row. Its row,
// package resolution, went with it once every remaining cell was a dash.
export const compareCopy: SectionCopy = {
  heading: 'How nvx compares',
  // No lead: the heading says what the table is, and the date it was checked
  // belongs with the other notes under it rather than between the heading
  // and the thing it qualifies.
  leadHtml: '',
};

// Column order is the cell order in every row below. The highlighted column is
// this product, and it gets the accent outline.
export const compareColumns: ComparisonColumn[] = [
  { name: 'nvx', highlight: true },
  { name: 'nvm' },
  { name: 'fnm' },
  { name: 'volta' },
  { name: 'asdf' },
  { name: 'mise' },
];

// nvm's cells below are nvm-sh/nvm, for macOS and Linux. The FAQ covers NVM
// for Windows, so the table carries no footnote for it. A two-column
// split (nvm vs NVM for Windows) was tried on 2026-09-18 and reverted the
// same day: the table is cross-platform to begin with, most rows would have
// carried the same answer twice, and a reader on any platform other than
// Windows had to scroll past a whole column for a tool that could never
// apply to them. NVM for Windows is a separate, Windows-only project with no
// shared code. Checked against its
// v2.0.0 release (2026-09-02) and the "What's new in v2" page at
// docs.nvm-windows.com/features/newv2.
export const compareRows: ComparisonRow[] = [
  // Labels cut to a few words on 2026-09-24; the rows read as a list of
  // sentences. "Single static binary" and "Switch affects only this shell"
  // went: both describe how a tool is built, and neither is what someone
  // choosing a version manager, or a safer way to run installs, is asking.
  // "Policy file in repo" came in, checked against mise's sandboxing page the
  // same day: its sandbox settings can live in a project's mise.toml, are off
  // unless set, and are not enforced on Windows.
  { feature: 'Windows, macOS, Linux', cells: ['✓', 'macOS, Linux', '✓', '✓', 'macOS, Linux', '✓'] },
  { feature: 'Runtimes', cells: ['Node.js, Bun', 'Node.js', 'Node.js', 'Node.js', 'many, via plugins', 'many, via backends'] },
  // volta and asdf resolve the version inside a shim when the command runs,
  // rather than hooking cd. Same result, and it is why a debugger or an IDE
  // launching node outside a project sees the wrong version.
  { feature: 'Auto-switch per project', cells: ['✓', 'shell hook', '✓', 'on invocation', 'on invocation', '✓'] },
  // Renamed from "Verified downloads" on 2026-10-08. nvx checks a SHA-256 that
  // comes from the same publisher as the archive, which is a checksum and not a
  // signature, and the old label put it level with tools that verify more.
  { feature: 'Checksummed downloads', cells: ['✓', '✓', '—', '—', 'varies by plugin', '✓'] },
  { feature: 'Supply-chain checks', cells: ['✓', '—', '—', '—', '—', '—'] },
  // mise shipped sandboxing in April 2026, so these are no longer dashes for
  // it. Every sandbox.deny_* setting defaults to false, it covers `mise run`
  // and `mise exec` rather than an install, and mise's own docs say it is
  // unavailable on Windows.
  //
  // nvx's ticks on Sandboxed installs and Secrets hidden share one mark and
  // one note below, because each has a platform exception on the limitations
  // page. The first is bun on Windows. The second is reads on macOS.
  { feature: 'Sandboxed installs', cells: ['✓*', '—', '—', '—', '—', 'opt-in, not Windows'] },
  { feature: 'Network allowlist', cells: ['✓', '—', '—', '—', '—', 'opt-in, not Windows'] },
  { feature: 'Secrets hidden', cells: ['✓*', '—', '—', '—', '—', 'opt-in, not Windows'] },
  { feature: 'Policy file in repo', cells: ['✓', '—', '—', '—', '—', 'opt-in, not Windows'] },
];

/** Shown under the table. Two notes and no more: the exceptions behind the
 *  marked ticks, and when the other columns were checked. Detail lives on the
 *  limitations page, which the first note links.
 *
 *  volta's status is stated flatly and left there. An earlier version added
 *  that volta's own maintainers recommend mise, which pointed readers at the
 *  one tool here that is ahead of nvx on integrity. Writing a competitor's
 *  obituary at length reads as score-settling however true it is. */
export const compareNoteHtml = [
  '<p>* With platform exceptions. On Windows, bun installs contained only on the drive Windows is installed on. On macOS, a contained install cannot read your home directory but can read files elsewhere on the disk. <a href="/docs/limitations/">Known limitations</a> has the rest.</p>',
  "<p>Out-of-the-box defaults, checked against each project on 18 September 2026. nvx's runtime downloads are checked against a checksum from the same publisher, not an independent signature. volta's maintainers announced in November 2025 that it is unmaintained.</p>",
].join('');
