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

// Text only, since 2026-10-09. Every card used to carry a terminal panel, and
// with the hero's terminal that made the page a run of "sentence, then
// terminal" with the same install shown twice. The docs carry the output now.
export const surfaces: SurfaceCard[] = [
  {
    title: 'Versions, per project',
    body: 'Install Node.js or Bun, pin a version per project, and switch on <code>cd</code> from a <code>.nvmrc</code>, <code>.node-version</code> or <code>package.json</code>. Each terminal keeps its own version. <a href="/docs/versions/">Versions</a>',
  },
  {
    title: 'Checked before it runs',
    body: 'nvx checks each package for known advisories, lookalike names and a very recent release, and asks before it goes ahead. A package OSV lists as malicious is refused outright. <a href="/docs/blocked/">What a block looks like</a>',
  },
  {
    title: 'Installs, contained',
    body: 'Installs run in the platform sandbox (AppContainer, Landlock or Seatbelt) with a throwaway home and a scrubbed environment. They can write to the project, and reach only the hosts you allow. <a href="/docs/containment/">How it works</a>',
  },
  {
    title: 'Governed by a file in your repo',
    body: 'The rules live in <code>.nvx-policy.json</code>, reviewed in a pull request like any other change. A file that loosens them has to be trusted first, and an organisation baseline cannot be loosened at all. <a href="/docs/policy/">Configuration</a>',
  },
];
