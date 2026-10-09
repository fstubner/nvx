import type { FaqItem, SectionCopy } from './types';

export const faqCopy: SectionCopy = {
  heading: 'Questions',
  leadHtml:
    'Common questions about nvx. What it does not cover is listed in <a href="/docs/limitations/">Limitations</a>.',
};

// `a` is plain text and goes into the FAQ structured data and /llms.txt;
// `aHtml` is the rendered version and may add links and <code>. Keep the two
// saying the same thing. A search result quoting the plain answer and a page
// showing a different one is the failure this pairing exists to avoid.
export const faq: FaqItem[] = [
  {
    group: 'Basics',
    q: 'What is nvx?',
    a: 'nvx runs npm install and npx inside an OS sandbox on Windows, macOS and Linux, so a package cannot read your credentials or reach a host you did not allow. You and your agent type the same commands. It also manages Node.js and Bun versions.',
    aHtml:
      '<p>nvx runs <code>npm install</code> and <code>npx</code> inside an OS sandbox on Windows, macOS and Linux, so a package cannot read your credentials or reach a host you did not allow. You and your agent type the same commands. It also manages Node.js and Bun versions.</p>',
  },
  {
    group: 'Basics',
    q: "Is nvx related to Microsoft's NVX?",
    a: 'No. Microsoft has a separate micro-VM sandbox project also called NVX, at github.com/microsoft/nvx. This project is at github.com/fstubner/nvx and nvx.run, and its npm package is @fstubner/nvx.',
    aHtml:
      '<p>No. Microsoft has a separate micro-VM sandbox project also called NVX, at github.com/microsoft/nvx. This project is at <a href="https://github.com/fstubner/nvx">github.com/fstubner/nvx</a> and nvx.run, and its npm package is <code>@fstubner/nvx</code>.</p>',
  },
  {
    group: 'Basics',
    // From the release assets (release.yml builds these five), the Landlock
    // error in sandbox_landlock_linux.go and the `ip` lookup in
    // sandbox_network_linux.go.
    q: 'What does it need to run?',
    a: 'Windows on x64, macOS on Apple silicon or Intel, or Linux on x86_64 or arm64. On Linux the sandbox needs kernel 5.13 or later with Landlock, unprivileged user namespaces and the ip command. nvx doctor checks that the sandbox starts.',
    aHtml:
      '<p>Windows on x64, macOS on Apple silicon or Intel, or Linux on x86_64 or arm64. On Linux the sandbox needs kernel 5.13 or later with Landlock, unprivileged user namespaces and the <code>ip</code> command. <code>nvx doctor</code> checks that the sandbox starts.</p>',
  },
  {
    group: 'Basics',
    q: 'Do I have to change how I run npm?',
    a: 'No. nvx puts shims on PATH, so npm install is still npm install, whether you, your agent or your editor runs it.',
    aHtml:
      '<p>No. nvx puts shims on <code>PATH</code>, so <code>npm install</code> is still <code>npm install</code>, whether you, your agent or your editor runs it.</p>',
  },
  {
    group: 'Basics',
    // nvm and NVM for Windows are unrelated projects. NVM for Windows' v2
    // (2026-09-02) added per-directory auto-switching, so the comparison here
    // is about containment rather than switching.
    q: 'Does it replace nvm, fnm or volta?',
    a: 'Yes. nvx installs, switches and pins Node.js per project and switches on cd. It also contains installs, which none of them do. nvx import brings over the versions you already have.',
    aHtml:
      '<p>Yes. nvx installs, switches and pins Node.js per project and switches on <code>cd</code>. It also contains installs, which none of them do. <code>nvx import</code> brings over the versions you already have. See <a href="/docs/versions/">Node.js and Bun versions</a>.</p>',
  },
  {
    group: 'Containment',
    q: 'What can a contained install reach?',
    a: "The project and a throwaway home of its own. Your environment variables and the project's .env files are hidden, and .git is read-only. It can connect only to the hosts on the allowlist, which starts with the npm registry and the OSV advisory API. On macOS it can also read files outside your home folder.",
    aHtml:
      "<p>The project and a throwaway home of its own. Your environment variables and the project's <code>.env</code> files are hidden, and <code>.git</code> is read-only. It can connect only to the hosts on the allowlist, which starts with the npm registry and the OSV advisory API. On macOS it can also read files outside your home folder. <a href=\"/docs/containment/\">How it works</a> has the per-platform table.</p>",
  },
  {
    group: 'Containment',
    q: 'Is my own code sandboxed too?',
    a: 'Not by default. Installs and tool runs that fetch a package are contained. npm run, npm test and node run uncontained. Set isolation.level to strict to contain them too.',
    aHtml:
      '<p>Not by default. Installs and tool runs that fetch a package are contained. <code>npm run</code>, <code>npm test</code> and <code>node</code> run uncontained. Set <code>isolation.level</code> to <code>strict</code> to contain them too.</p>',
  },
  {
    // npm v12 shipped on 2026-07-08 with lifecycle scripts blocked by default.
    group: 'Containment',
    q: 'npm 12 blocks install scripts by default. Is this still needed?',
    a: 'It closes the biggest hole, and nvx covers what is left. A package you approve keeps that approval if it is compromised later. Your bundler, test runner and dev server still evaluate package code. Older npm and yarn classic have no per-package approval at all. nvx runs the install, scripts included, where it cannot reach your credentials.',
    aHtml:
      '<p>It closes the biggest hole, and nvx covers what is left. A package you approve keeps that approval if it is compromised later. Your bundler, test runner and dev server still evaluate package code. Older npm and yarn classic have no per-package approval at all. nvx runs the install, scripts included, where it cannot reach your credentials.</p>',
  },
  {
    group: 'Using it',
    q: 'Does it work with AI coding agents?',
    a: 'Yes. The packages an agent installs are checked and contained like yours. nvx does not contain the agent itself. Set NVX_AGENT_MODE=1 in its environment so nvx refuses instead of asking. The agents page has a snippet for AGENTS.md and the steps for MCP servers.',
    aHtml:
      '<p>Yes. The packages an agent installs are checked and contained like yours. nvx does not contain the agent itself. Set <code>NVX_AGENT_MODE=1</code> in its environment so nvx refuses instead of asking. <a href="/docs/agents/">AI agents and MCP</a> has a snippet for AGENTS.md and the steps for MCP servers.</p>',
  },
  {
    group: 'Using it',
    // Figures from the CHANGELOG for 0.8.0 and the containment figure measured
    // on Windows 11 on 2026-10-07. Nothing here is estimated.
    q: 'How much slower is a contained install?',
    a: 'Measured on Windows 11, a trivial contained command took 312 ms against 53 ms for node alone. An install of one package through the contained shim took a median of 8.8 s on Windows and 2.39 s on Linux. Nothing has been measured on macOS.',
    aHtml:
      '<p>Measured on Windows 11, a trivial contained command took 312 ms against 53 ms for node alone. An install of one package through the contained shim took a median of 8.8 s on Windows and 2.39 s on Linux. Nothing has been measured on macOS. The <a href="https://github.com/fstubner/nvx/blob/main/docs/enforcement-matrix.md#measured-costs-and-platform-floors">enforcement matrix</a> has the conditions.</p>',
  },
];
