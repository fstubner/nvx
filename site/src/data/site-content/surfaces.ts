import type { SectionCopy, SurfaceCard } from './types';

// "Two jobs" until 2026-09-16, which left the middle one unexplained. The hero
// terminal shows nvx verifying a package and scanning for advisories before it
// installs anything, and nothing on the page said what that was. Checking a
// package is not the same job as containing it: one decides whether the code
// should run, the other decides what it can reach once it does.
export const surfacesCopy: SectionCopy = {
  heading: 'Three jobs, one binary',
  leadHtml:
    'Switching runtimes is the job you notice. Checking what you are about to install, and containing it once it runs, are the two you do not. One file in the repo governs all three.',
};

// Code panels rather than screenshots, deliberately: a picture of a terminal
// would have to be staged, and these are the commands as they actually run.
export const surfaces: SurfaceCard[] = [
  {
    title: 'Versions, per project',
    body: 'Install and pin Node.js or Bun, and switch on <code>cd</code> from a <code>.nvmrc</code>, <code>.node-version</code> or <code>package.json</code>. Session-scoped, so a new terminal is unaffected until it reads the same pin.',
    codeHtml: `<span class="t-dim">$</span> nvx install 22
<span class="t-ok">&#10004;</span> Node.js v22.23.2 installed
<span class="t-dim">$</span> nvx use 22
<span class="t-ok">&#10004;</span> Now using Node.js v22.23.2 in this terminal.
<span class="t-dim">$</span> cd ../other-project   <span class="t-dim"># pinned to 20</span>
<span class="t-ok">&#10004;</span> Switched to Node.js v20.19.5`,
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
    codeHtml: `<span class="t-dim">$</span> npm ci
<span class="t-warn">&#9888;</span> Non-interactive environment: denying prompt.
  Prompt was: Package @astrojs/starlight@0.42.1 was published
  only 12.9 hours ago. Supply chain compromises are often
  caught within 24 hours. Proceed?
<span class="t-err">&#10008;</span> Installation aborted: the release-age warning was not approved.`,
  },
  {
    title: 'Installs, contained',
    body: 'You type the same command. nvx runs it inside the platform sandbox (AppContainer, Landlock, or Seatbelt) with a throwaway <code>HOME</code>, writes confined to the project, and an egress allowlist the contained process cannot talk its way past.',
    // Captured from a real contained install rather than composed: on
    // 2026-09-14 with left-pad, re-captured 2026-09-24 with sample-package.
    // What this replaced showed a blocked egress against an invented host, and
    // was the only output on the page nvx had not actually printed.
    codeHtml: `<span class="t-dim">$</span> npm install sample-package
<span class="t-info">&#8505;</span> <span class="t-hi">Running in native sandbox: npm install sample-package</span>
added 1 package, and audited 2 packages in 1s
found 0 vulnerabilities`,
  },
  {
    title: 'Governed by a file in your repo',
    body: 'The rules live in <code>.nvx-policy.json</code>, next to the code they cover, reviewed in a pull request and enforced on the machine. An org can set <code>"enforced": true</code> globally and a project may then only tighten it. <code>-y</code>, <code>--agent-mode</code> and <code>NVX_YES</code> cannot widen the sandbox, <code>nvx policy check</code> gives CI a distinct exit code per failure, and <code>nvx audit export</code> turns every block into evidence.',
    flip: true,
    // This was a section of its own with four paragraphs and nothing shown,
    // which made it the one part of the page that asserted instead of
    // demonstrating. The file below is what `nvx policy init` wrote on
    // 2026-09-17, trimmed to the keys that carry a decision: every value
    // still shown is byte-for-byte from the real file. Dropped are the
    // empty objects and arrays (blocked_packages, trusted_packages,
    // install_scripts, vulnerabilities, isolation.environment), the runtime
    // block, and the scalar keys still on their installed default
    // (enforce_ignore_scripts, isolation.filesystem.provider, isolation.level,
    // environment.isolated_tools). Nothing shown is reworded or invented.
    codeHtml: `<span class="t-dim">$</span> nvx policy init
<span class="t-ok">&#10004;</span> Wrote project policy to .nvx-policy.json
{
  "typosquatting": { "enabled": true, "max_distance": 2 },
  "release_age":   { "enabled": true, "min_age_hours": 24 },
  "isolation": {
    "enabled": true,
    "network": {
      "mode": "proxy",
      "default_allow": ["registry.npmjs.org:443", "registry.yarnpkg.com:443", "api.osv.dev:443"],
      "prompt_unknown": true
    }
  }
}`,
  },
];
