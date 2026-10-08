import type { SectionCopy, SurfaceCard } from './types';

// Four cards under one plain heading. A count in the heading ("Three jobs")
// sat over four cards and disagreed with the docs overview, so neither page
// counts any more. Checking a package is not the same job as containing it:
// one decides whether the code should run, the other decides what it can
// reach once it does.
export const surfacesCopy: SectionCopy = {
  heading: 'What nvx does',
  leadHtml:
    'It switches runtimes per project, checks what you are about to install, and contains the install while it runs. A policy file in the repo governs the checks and the sandbox.',
};

// Code panels rather than screenshots, deliberately: a picture of a terminal
// would have to be staged, and these are the commands as they actually run.
export const surfaces: SurfaceCard[] = [
  {
    title: 'Versions, per project',
    body: 'Install and pin Node.js or Bun, and switch on <code>cd</code> from a <code>.nvmrc</code>, <code>.node-version</code> or <code>package.json</code>. Session-scoped, so a new terminal is unaffected until it reads the same pin.',
    // The last line is what `nvx auto` printed on 2026-09-26 in a scratch
    // project whose .nvmrc read 20. It used to say "Switched to", which nvx
    // never prints.
    codeHtml: `<span class="t-dim">$</span> nvx install 22
<span class="t-ok">&#10004;</span> Node.js v22.23.2 installed
<span class="t-dim">$</span> nvx use 22
<span class="t-ok">&#10004;</span> Now using Node.js v22.23.2 in this terminal.
<span class="t-dim">$</span> cd ../other-project   <span class="t-dim"># pinned to 20</span>
<span class="t-info">&#8505;</span> [nvx] Found .nvmrc: switching to Node.js v20.19.5`,
  },
  {
    title: 'Checked before it runs',
    body: 'Before anything runs, nvx checks each package for known advisories, lookalike names and a very recent publish date. For an npm install the advisory and age checks cover every package npm resolves, dependencies included. A version published in the last 24 hours waits for your approval, because that is when a compromised release is usually caught. A package OSV lists as malicious is refused outright, and scripts you approve still run contained.',
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
    body: 'You and your agent type the same command. nvx runs it inside the platform sandbox (AppContainer, Landlock or Seatbelt) with a throwaway <code>HOME</code> and a scrubbed environment. It can write to the project and that home. Outbound connections reach only the hosts the allowlist names or you add with <code>nvx allow-host</code>.',
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
    body: 'The rules live in <code>.nvx-policy.json</code>, next to the code they cover, reviewed in a pull request and enforced on the machine. <code>-y</code>, <code>--agent-mode</code> and <code>NVX_YES</code> cannot widen the sandbox. An org baseline, set with <code>"enforced": true</code> in the global policy, is one a project may tighten and not loosen. <code>nvx policy check</code> gives CI a distinct exit code per failure, and <code>nvx audit export</code> hands the record of every block to a compliance pipeline.',
    flip: true,
    // What `nvx policy init` printed and wrote on 2026-10-05, nvx 0.7.0 built
    // from main, in a scratch project on Windows. The only edit is the middle
    // of the absolute path, elided. The project file sets nothing on purpose:
    // it shows where a project adds hosts and blocks, and every other value
    // comes from the global policy (`nvx policy init --global` writes that).
    // The panel used to show the whole default policy as though init wrote it
    // into the project, which 0.7.0 no longer does.
    codeHtml: `<span class="t-dim">$</span> nvx policy init
<span class="t-ok">&#10004;</span> Wrote project policy to C:&#92;&hellip;&#92;proj&#92;.nvx-policy.json
<span class="t-dim">$</span> cat .nvx-policy.json
{
  "blocked_packages": [],
  "isolation": {
    "network": {
      "allow_hosts": []
    }
  }
}`,
  },
];
