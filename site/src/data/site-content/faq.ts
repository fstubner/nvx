import type { FaqItem, SectionCopy } from './types';

export const faqCopy: SectionCopy = {
  heading: 'Questions',
  leadHtml:
    'Including the ones whose honest answer is a limitation. The full list is in <a href="https://github.com/fstubner/nvx#known-limitations">Known limitations</a>.',
};

// `a` is plain text and goes into the FAQ structured data and /llms.txt;
// `aHtml` is the rendered version and may add links and <code>. Keep the two
// saying the same thing — a search result quoting the plain answer and a page
// showing a different one is the failure this pairing exists to avoid.
export const faq: FaqItem[] = [
  {
    group: 'Basics',
    q: 'What is nvx?',
    a: 'nvx installs, switches and pins Node.js and Bun versions on Windows, macOS and Linux, and runs package installs inside an OS sandbox. It is one static binary with no dependencies.',
    aHtml:
      '<p>nvx installs, switches and pins Node.js and Bun versions on Windows, macOS and Linux, and runs package installs inside an OS sandbox. It is one static binary with no dependencies.</p>',
  },
  {
    group: 'Basics',
    q: 'Does it replace nvm, fnm or volta?',
    // nvm and NVM for Windows are unrelated projects. NVM for Windows'
    // v2 (2026-09-02) added the same per-directory auto-switching and
    // auto-install this answer used to claim as a Windows gap, so the
    // comparison here is now about containment, not about switching.
    a: 'Yes. nvx installs, switches, pins and auto-switches on cd just as they do. On Windows, that includes NVM for Windows, a separate project despite the name. What nvx adds beyond any of them is containment. Every install runs inside an OS sandbox with a scrubbed environment and an egress allowlist, on all three platforms.',
    aHtml:
      '<p>Yes. nvx installs, switches, pins and auto-switches on <code>cd</code> just as they do. On Windows, that includes NVM for Windows, a separate project despite the name. What nvx adds beyond any of them is containment. Every install runs inside an OS sandbox with a scrubbed environment and an egress allowlist, on all three platforms.</p>',
  },
  {
    group: 'Basics',
    q: 'Do I have to change how I run npm?',
    a: 'No. nvx puts shims on PATH, so npm install is still npm install. It is contained when it runs code you did not write, and nothing else about your workflow changes.',
    aHtml:
      '<p>No. nvx puts shims on <code>PATH</code>, so <code>npm install</code> is still <code>npm install</code>. It is contained when it runs code you did not write, and nothing else about your workflow changes.</p>',
  },
  {
    group: 'Containment',
    q: 'What can a contained install actually reach?',
    a: 'Your project directory, its lockfile and node_modules. Environment variables are scrubbed, writes cannot leave the project, and outbound network access is limited to an allowlist that defaults to the npm registry and the OSV vulnerability API.',
    aHtml:
      '<p>Your project directory, its lockfile and <code>node_modules</code>. Environment variables are scrubbed, writes cannot leave the project, and outbound network access is limited to an allowlist that defaults to the npm registry and the OSV vulnerability API.</p>',
  },
  {
    group: 'Containment',
    q: 'Is macOS protected the same way as Windows and Linux?',
    a: 'No. On Windows and Linux a contained install cannot read your home directory, so SSH keys, cloud credentials and your npm token are out of reach. On macOS the Seatbelt profile allows filesystem reads, so those files can be read by absolute path. macOS still enforces write containment and egress control.',
    aHtml:
      '<p><strong>No.</strong> On Windows and Linux a contained install cannot read your home directory, so SSH keys, cloud credentials and your npm token are out of reach. On macOS the Seatbelt profile allows filesystem reads, so those files <em>can</em> be read by absolute path. macOS still enforces write containment and egress control.</p>',
  },
  {
    // npm v12 shipped on 2026-07-08 with lifecycle scripts blocked by default.
    // Anyone who knows that will check this claim, so the site answers it
    // rather than waiting to be corrected. The honest answer is also the
    // stronger one, and every limb of it is checkable.
    group: 'Containment',
    q: 'npm 12 blocks install scripts by default. Is this still needed?',
    a: 'It closes the biggest hole, and nvx is about what is left. Packages that genuinely need a build step get approved, and that approval is permanent, so a later compromise of an approved package inherits it. Your bundler, test runner and dev server evaluate package code, which no script setting touches. A git dependency can override the git binary through .npmrc and run despite ignore-scripts. Older npm and yarn classic have no per-package mechanism at all. Containment is also a different thing from detection: the packages in the August 2026 ChainDrop compromise carried valid GitHub Actions provenance.',
    aHtml:
      '<p>It closes the biggest hole, and nvx is about what is left.</p><p>Packages that genuinely need a build step get approved, and that approval is permanent, so a later compromise of an approved package inherits it. Your bundler, test runner and dev server evaluate package code, which no script setting touches. A git dependency can override the <code>git</code> binary through <code>.npmrc</code> and run despite <code>--ignore-scripts</code>. Older npm and yarn classic have no per-package mechanism at all.</p><p>Containment is also a different thing from detection. The packages in the <a href="https://www.microsoft.com/en-us/security/blog/2026/08/04/chaindrop-supply-chain-compromise-anatomy-self-propagating-worm/">August 2026 ChainDrop compromise</a> carried <em>valid</em> GitHub Actions provenance.</p>',
  },
  {
    group: 'Containment',
    q: 'Is my own code sandboxed too?',
    a: 'Not by default. Containment covers installs and updates, such as npm install and npm update, and ad-hoc tool runners such as npx and bunx. npm run build, npm test and node run uncontained at the standard isolation level. Set isolation.level to strict to extend containment to your own code.',
    aHtml:
      '<p>Not by default. Containment covers installs and updates, such as <code>npm install</code> and <code>npm update</code>, and ad-hoc tool runners such as <code>npx</code> and <code>bunx</code>. <code>npm run build</code>, <code>npm test</code> and <code>node</code> run uncontained at the <code>standard</code> isolation level. Set <code>isolation.level</code> to <code>strict</code> to extend containment to your own code.</p>',
  },
  {
    group: 'Containment',
    q: 'What happens when an install tries to reach a host I have not allowed?',
    a: 'nvx blocks the connection and names the host it blocked. Allowing it means adding it to a project policy file, and nvx will not honour a policy that widens the allowlist until you have approved it. Passing -y or --agent-mode, or setting NVX_YES, deliberately does not count, because an agent will answer yes to anything.',
    aHtml:
      '<p>nvx blocks the connection and names the host it blocked. Allowing it means adding it to a project policy file, and nvx will not honour a policy that widens the allowlist until you have approved it. Passing <code>-y</code> or <code>--agent-mode</code>, or setting <code>NVX_YES</code>, deliberately does not count, because an agent will answer yes to anything.</p>',
  },
  {
    group: 'Using it',
    q: 'What does the sandbox cost in speed?',
    a: 'Measured dispatch overhead is about 75 ms on Windows, from three runs on one machine. It has not been established on Linux or macOS, so no figure is quoted for them. Either way it sits next to commands like npm install that you already wait on.',
    aHtml:
      '<p>Measured dispatch overhead is about <strong>75 ms on Windows</strong>, from three runs on one machine. It has not been established on Linux or macOS, so no figure is quoted for them. Either way it sits next to commands like <code>npm install</code> that you already wait on.</p>',
  },
  {
    group: 'Using it',
    q: 'Can I install it from winget, Homebrew, Scoop or npm?',
    a: 'Not yet. The install script and the prebuilt release binaries are the two routes today, on all three platforms.',
    aHtml:
      '<p>Not yet. The install script and the prebuilt release binaries are the two routes today, on all three platforms.</p>',
  },
];
