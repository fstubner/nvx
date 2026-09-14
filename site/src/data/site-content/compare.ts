import type { ComparisonColumn, ComparisonRow, SectionCopy } from './types';

// The comparison matrix, carried over from README's "How nvx compares".
//
// Every cell is that table's claim, unchanged. Two things are worth knowing
// before editing it: the rows describe out-of-the-box defaults at the time of
// writing, and the supply-chain row was checked against volta, fnm and asdf
// directly -- none of them intercepts an install or queries OSV. uv was not
// verified independently, so its cell reflects README rather than a check.
export const compareCopy: SectionCopy = {
  heading: 'How nvx compares',
  leadHtml:
    'Against the version managers it replaces, and against <code>uv</code>, which sets the bar for a single fast binary that does more than versions. Out-of-the-box defaults, at the time of writing.',
};

export const compareColumns: ComparisonColumn[] = [
  { name: 'nvx', highlight: true },
  { name: 'nvm' },
  { name: 'fnm' },
  { name: 'volta' },
  { name: 'asdf / mise' },
  { name: 'uv' },
];

export const compareRows: ComparisonRow[] = [
  { feature: 'Windows / macOS / Linux', cells: ['all three', 'macOS, Linux', 'all three', 'all three', 'mise only', 'all three'] },
  { feature: 'Single static binary', cells: ['✓ Go', 'shell script', '✓ Rust', '✓ Rust', '✓ mise', '✓ Rust'] },
  { feature: 'Runtimes managed', cells: ['Node.js, Bun', 'Node', 'Node', 'Node', 'many, via plugins', 'Python'] },
  { feature: 'Auto-switch on cd', cells: ['✓', 'shell hook', '✓', '✓', '✓', 'project pin'] },
  { feature: 'Session-scoped switching', cells: ['✓', '✓', '✓', 'shims', 'shims', '—'] },
  { feature: 'Checksum-verified downloads', cells: ['✓', '✓', '✓', '✓', 'varies', '✓'] },
  { feature: 'Package resolution / lockfiles', cells: ['—', '—', '—', '—', '—', '✓'] },
  { feature: 'Typosquat / OSV / release-age checks', cells: ['✓', '—', '—', '—', '—', '—'] },
  { feature: 'OS sandbox for install and run', cells: ['✓', '—', '—', '—', '—', '—'] },
  { feature: 'Egress allowlist for install scripts', cells: ['✓', '—', '—', '—', '—', '—'] },
  { feature: 'Environment secrets scrubbed', cells: ['✓', '—', '—', '—', '—', '—'] },
];

/** Shown under the table. asdf is Unix-only; mise is what adds Windows. */
export const compareNoteHtml =
  'asdf is Unix-only, and mise is what adds Windows support. nvx is not a package manager: it does not resolve dependencies or write lockfiles.';
