import type { SectionCopy, SurfaceCard } from './types';

// "Two jobs" until 2026-09-16, which left the middle one unexplained. The hero
// terminal shows nvx verifying a package and scanning for advisories before it
// installs anything, and nothing on the page said what that was. Checking a
// package is not the same job as containing it: one decides whether the code
// should run, the other decides what it can reach once it does.
export const surfacesCopy: SectionCopy = {
  heading: 'Three jobs, one binary',
  leadHtml:
    'Switching runtimes is the job you notice. Checking what you are about to install, and containing it once it runs, are the two you do not.',
};

// Code panels rather than screenshots, deliberately: a picture of a terminal
// would have to be staged, and these are the commands as they actually run.
export const surfaces: SurfaceCard[] = [
  {
    title: 'Versions, per project',
    body: 'Install and pin Node.js or Bun, and switch on <code>cd</code> from a <code>.nvmrc</code>, <code>.node-version</code> or <code>package.json</code>. Session-scoped, so a new terminal is unaffected until it reads the same pin.',
    codeHtml: `<span style="color:var(--ui-code-comment)">$</span> nvx install 22
<span style="color:var(--ui-code-comment)">✔</span> Node.js v22.23.2 installed
<span style="color:var(--ui-code-comment)">$</span> nvx use 22
<span style="color:var(--ui-code-comment)">✔</span> Now using Node.js v22.23.2 in this terminal.
<span style="color:var(--ui-code-comment)">$</span> cd ../other-project   <span style="color:var(--ui-code-comment)"># pinned to 20</span>
<span style="color:var(--ui-code-comment)">✔</span> Switched to Node.js v20.19.5`,
  },
  {
    title: 'Checked before it runs',
    body: 'Every package is checked against the OSV advisory database, against a typosquat heuristic, and against how recently it was published. A version that appeared hours ago is the window a compromised release is usually caught in, so nvx stops and asks rather than installing it quietly.',
    flip: true,
    // Captured on 2026-09-16 from a real `npm ci` in this repository's site
    // directory, which is how the check was found doing its job. Subtractive
    // edits only: the exact publish timestamp, the sentence naming the
    // approval flags, and a lockfile parse warning that was a defect since
    // fixed. Wrapped for the panel width. Nothing reworded.
    codeHtml: `<span style="color:var(--ui-code-comment)">$</span> npm ci
<span style="color:var(--ui-code-comment)">⚠</span> Non-interactive environment: denying prompt.
  Prompt was: Package @astrojs/starlight@0.42.1 was published
  only 12.9 hours ago. Supply chain compromises are often
  caught within 24 hours. Proceed?
<span style="color:var(--ui-code-comment)">✘</span> Installation aborted: the release-age warning was not approved.`,
  },
  {
    title: 'Installs, contained',
    body: 'You type the same command. nvx runs it inside the platform sandbox (AppContainer, Landlock, or Seatbelt) with a throwaway <code>HOME</code>, writes confined to the project, and an egress allowlist the contained process cannot talk its way past.',
    // Captured from a real contained install on 2026-09-14 rather than composed.
    // What this replaced showed a blocked egress against an invented host, and
    // was the only output on the page nvx had not actually printed.
    codeHtml: `<span style="color:var(--ui-code-comment)">$</span> npm install left-pad
<span style="color:var(--ui-code-comment)">ℹ</span> Running in native sandbox: npm install left-pad
added 1 package, and audited 2 packages in 2s
found 0 vulnerabilities`,
  },
];
