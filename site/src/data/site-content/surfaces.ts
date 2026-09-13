import type { SectionCopy, SurfaceCard } from './types';

export const surfacesCopy: SectionCopy = {
  heading: 'Two jobs, one binary',
  leadHtml:
    'Switching runtimes is the job you notice. Containing what they install is the one you do not.',
};

// Both cards are code panels rather than screenshots, deliberately: a picture
// of a terminal would have to be staged, and these are the commands as they
// actually run.
export const surfaces: SurfaceCard[] = [
  {
    title: 'Versions, per project',
    body: 'Install and pin Node.js or Bun, and switch on <code>cd</code> from a <code>.nvmrc</code>, <code>.node-version</code> or <code>package.json</code>. Session-scoped: a new terminal is unaffected until it reads the same pin.',
    codeHtml: `<span style="color:var(--ui-code-comment)">$</span> nvx install 22
<span style="color:var(--ui-code-comment)">✔</span> Node.js v22.23.2 installed
<span style="color:var(--ui-code-comment)">$</span> nvx use 22
<span style="color:var(--ui-code-comment)">✔</span> Now using Node.js v22.23.2 in this terminal.
<span style="color:var(--ui-code-comment)">$</span> cd ../other-project   <span style="color:var(--ui-code-comment)"># pinned to 20</span>
<span style="color:var(--ui-code-comment)">✔</span> Switched to Node.js v20.19.5`,
  },
  {
    title: 'Installs, contained',
    body: 'You type the same command. nvx runs it inside the platform sandbox — AppContainer, Landlock, or Seatbelt — with a throwaway <code>HOME</code>, writes confined to the project, and an egress allowlist the contained process cannot talk its way past.',
    flip: true,
    codeHtml: `<span style="color:var(--ui-code-comment)">$</span> npm install some-package
<span style="color:var(--ui-code-comment)">ℹ</span> Running in native sandbox: npm install
<span style="color:var(--ui-code-comment)">⚠</span> Blocked egress: telemetry.example.net:443
<span style="color:var(--ui-code-comment)">ℹ</span> Allow it in .nvx-policy.json if you meant to.
<span style="color:var(--ui-code-comment)">✔</span> added 42 packages`,
  },
];
