import type { ChangelogSurface, SectionCopy } from './types';
import { meta } from './meta';

// The changelog page's own copy, and one plain-language summary per release.
//
// The page reads GitHub Releases at runtime and falls back to CHANGELOG.md,
// so the version list itself is never written here. These summaries are the
// part a person writes: what a release meant for someone using the product,
// in a sentence, above the generated notes. A tag with no entry here simply
// renders without one.
export const changelogCopy: SectionCopy = {
  heading: `${meta.siteName} release notes`,
  leadHtml: `What each ${meta.siteName} release changed for someone using it, above the generated notes.`,
};

/** Shown when the page is shared. */
export const changelogOgDescription = `Versioned ${meta.siteName} release notes and shipped changes.`;

// Keyed by tag, exactly as the release is named on GitHub. Each line is taken
// from that version's own CHANGELOG entry rather than written from memory.
//
// 0.5.5 and 0.5.7 are deliberately absent: CHANGELOG.md records both as "cut,
// never published", so there is no release for a summary to sit above.
// The surfaces this product actually has, for the auto-summary.
//
// The template shipped the map inline in scripts/changelog/summarize.ts, and
// it described the product the template was generalised from: a desktop app,
// a terminal UI, an MCP server and a "Rust core". A Go CLI's releases were
// therefore summarised as spanning surfaces it does not have. The generic
// half of that summariser -- feat/fix/refactor/docs/ci/deps -- stays in code,
// because those apply to any project.
export const changelogSurfaces: ChangelogSurface[] = [
  { pattern: /cli|command|subcommand/, label: 'the CLI' },
  { pattern: /sandbox|contain|isolat/, label: 'the sandbox' },
  { pattern: /core|runtime|version manager/, label: 'the Go core' },
  { pattern: /docs?|site|changelog/, label: 'the docs site' },
];

export const releaseSummaries: Record<string, string> = {
  'v0.6.0':
    'Per-project sandbox identity, and the first ways for a contained tool to reach named things outside its box: --connect for one service, loopback mode, read-and-execute grants, and named environment variables. The wsl, wslc and systemd-nspawn providers were removed.',
  'v0.5.6':
    'Commands that fetch and run untrusted package code were not being contained. They are now.',
  'v0.5.4': 'The Linux sandbox could not run anything. Fixed.',
  'v0.5.3':
    'nvx audit: a local record of what nvx did, so questions that only show up across many runs have evidence behind them.',
  'v0.5.2':
    'The async-pipe limitation is documented for tool runners, not only for installs.',
  'v0.5.1':
    'npm install esbuild hung forever inside the sandbox, and now does not.',
  'v0.5.0':
    'One sandbox could borrow another sandbox egress allowlist. Each project now gets its own identity.',
  'v0.4.0': 'Linux containment did not work at all. This is the release where it does.',
  'v0.2.0-beta': 'The isolation policy schema, in its first form.',
  'v0.1.0':
    'The first release: swapping Node.js versions across Windows, macOS and Linux from one static binary.',
};
