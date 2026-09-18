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
  leadHtml:
    'Against the version managers it replaces. Out-of-the-box defaults, checked against each project on 18 September 2026.',
};

export const compareColumns: ComparisonColumn[] = [
  { name: 'nvx', highlight: true },
  // Split into two columns on 2026-09-18. nvm and NVM for Windows are
  // unrelated projects that happen to share a name -- different owners,
  // different codebases, no shared lineage -- and NVM for Windows' own
  // README says so under a heading reading "NVM for Windows is not the
  // same thing as nvm!". One column captioned "macOS, Linux" used to carry
  // both; a Windows reader who has NVM for Windows installed has no reason
  // to read that as a hedge rather than an omission of their own tool.
  { name: 'nvm (macOS, Linux)' },
  { name: 'NVM for Windows' },
  { name: 'fnm' },
  { name: 'volta' },
  { name: 'asdf' },
  { name: 'mise' },
];

// NVM for Windows' cells below are checked against its v2.0.0 release
// (2026-09-02) and the "What's new in v2" page at
// docs.nvm-windows.com/features/newv2, not against v1.
export const compareRows: ComparisonRow[] = [
  { feature: 'Windows / macOS / Linux', cells: ['✓', 'macOS, Linux', 'Windows', '✓', '✓', 'macOS, Linux', '✓'] },
  // Split from the language on 2026-09-16. One row asking "is it a single
  // binary" whose cells answered "yes, in Rust" was doing two jobs, and
  // volta's answer, "Rust, 3 binaries", read as neither a yes nor a no. It
  // ships volta, volta-shim and volta-migrate plus a symlink per managed
  // tool, so the honest answer to this row is no.
  // The implementation language had its own row for part of 2026-09-16 and
  // was dropped the same day. The argument for it was that it explains nvm's
  // shell startup cost, but the row above already answers that: nvm is the
  // one entry that is not a binary. Naming the language added almost nothing
  // on top, and no reader chooses a version manager by it.
  { feature: 'Single static binary', cells: ['✓', '—', '—', '✓', '—', '✓', '✓'] },
  { feature: 'Runtimes managed', cells: ['Node.js, Bun', 'Node.js', 'Node.js', 'Node.js', 'Node.js', 'many, via plugins', 'many, via backends'] },
  // volta and asdf resolve the version inside a shim when the command runs,
  // rather than hooking cd. Same result, and it is why a debugger or an IDE
  // launching node outside a project sees the wrong version.
  // NVM for Windows' default "shim" mode (v2) resolves .nvmrc etc. when a
  // shim intercepts a command, not on cd itself -- the same mechanism as
  // volta and asdf, not a shell hook or an actual cd hook. Its "link" mode
  // has no auto-switch at all.
  { feature: 'Auto-switch on cd', cells: ['✓', 'shell hook', 'on invocation', '✓', 'on invocation', 'on invocation', '✓'] },
  // Stated as the property rather than the mechanism. nvx puts shims on PATH
  // too, but for interception: the version comes from env that `nvx use`
  // emits for this shell only. volta has no session-scoped command at all,
  // and `asdf shell` was removed in the 0.16 rewrite with no replacement.
  // nvm use in NVM for Windows sets the machine/user default (link mode's
  // target, or shim mode's config), stored in the registry -- no per-shell
  // override exists in its docs.
  { feature: 'Switch affects only this shell', cells: ['✓', '✓', '—', '✓', '—', 'removed in 0.16', '✓'] },
  { feature: 'Checksum-verified downloads', cells: ['✓', '✓', '✓', '—', '—', 'varies by plugin', '✓'] },
  { feature: 'Typosquat / OSV / release-age checks', cells: ['✓', '—', '—', '—', '—', '—', '—'] },
  // mise shipped sandboxing in April 2026, so these are no longer dashes for
  // it. Every sandbox.deny_* setting defaults to false, it covers `mise run`
  // and `mise exec` rather than an install, and mise's own docs say it is
  // unavailable on Windows.
  // "Lockdown Node.js/V8 permissions" is a shim-mode option in NVM for
  // Windows v2 -- Node's own in-process permission model, not an OS
  // sandbox around the install. Worth naming rather than a bare dash,
  // same as mise's row below.
  { feature: 'OS sandbox for install and run', cells: ['✓', '—', 'V8 permissions, optional', '—', '—', '—', 'opt-in, not Windows'] },
  { feature: 'Egress allowlist for install scripts', cells: ['✓', '—', '—', '—', '—', '—', 'opt-in, not Windows'] },
  { feature: 'Environment secrets scrubbed', cells: ['✓', '—', '—', '—', '—', '—', 'opt-in, not Windows'] },
];

/** Shown under the table. The volta line leads because it changes a reader's
 *  decision more than any feature row does.
 *
 *  Stated flatly and left there. An earlier version added that volta's own
 *  maintainers recommend mise, which pointed readers at the one tool here
 *  that is ahead of nvx on integrity, and explained that volta was listed
 *  because people still run it, which nobody asked. Writing a competitor's
 *  obituary at length reads as score-settling however true it is. */
export const compareNoteHtml =
  "volta's maintainers announced in November 2025 that it is unmaintained. nvx is not a package manager, so it does not resolve dependencies or write lockfiles. It runs the one you already use.";
