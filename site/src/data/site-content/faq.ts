import type { FaqItem, SectionCopy } from './types';

export const faqCopy: SectionCopy = {
  heading: 'Questions',
  leadHtml:
    'Common questions about nvx. What it does not cover is listed in full in <a href="/docs/limitations/">Known limitations</a>.',
};

// `a` is plain text and goes into the FAQ structured data and /llms.txt;
// `aHtml` is the rendered version and may add links and <code>. Keep the two
// saying the same thing. A search result quoting the plain answer and a page
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
    // From the release assets (release.yml builds these five), the Landlock
    // error in sandbox_landlock_linux.go, the `ip` lookup in
    // sandbox_network_linux.go and seatbeltExecPath. No Windows or macOS
    // version floor is stated because none has been established.
    q: 'What does it need to run?',
    a: 'Windows on x64, macOS on Apple silicon or Intel, or Linux on x86_64 or arm64. nvx is one static binary and needs nothing installed alongside it. On Linux the sandbox needs kernel 5.13 or later with Landlock enabled and unprivileged user namespaces. The network allowlist also needs the ip command from iproute2. When one is missing, contained commands refuse to run, so nothing runs uncontained. nvx doctor checks whether a contained process can start. On macOS the sandbox uses sandbox-exec, which ships with macOS.',
    aHtml:
      '<p>Windows on x64, macOS on Apple silicon or Intel, or Linux on x86_64 or arm64. nvx is one static binary and needs nothing installed alongside it.</p><p>On Linux the sandbox needs kernel 5.13 or later with Landlock enabled and unprivileged user namespaces. The network allowlist also needs the <code>ip</code> command from iproute2. When one is missing, contained commands refuse to run, so nothing runs uncontained. <code>nvx doctor</code> checks whether a contained process can start. On macOS the sandbox uses <code>sandbox-exec</code>, which ships with macOS.</p>',
  },
  {
    group: 'Basics',
    q: 'Does it replace nvm, fnm or volta?',
    // nvm and NVM for Windows are unrelated projects. NVM for Windows'
    // v2 (2026-09-02) added the same per-directory auto-switching and
    // auto-install this answer used to claim as a Windows gap, so the
    // comparison here is now about containment, not about switching.
    a: 'Yes. nvx installs, switches, pins and auto-switches on cd just as they do. On Windows, that includes NVM for Windows, a separate project despite the name. What nvx adds beyond any of them is containment. Installs run inside an OS sandbox with a scrubbed environment and an egress allowlist, on Windows, macOS and Linux. The platforms differ in the details. macOS contains reads of credential stores only, and on Windows bun installs contained only on the drive Windows is installed on. Known limitations lists the rest.',
    aHtml:
      '<p>Yes. nvx installs, switches, pins and auto-switches on <code>cd</code> just as they do. On Windows, that includes NVM for Windows, a separate project despite the name. What nvx adds beyond any of them is containment. Installs run inside an OS sandbox with a scrubbed environment and an egress allowlist, on Windows, macOS and Linux.</p><p>The platforms differ in the details. macOS contains reads of credential stores only, and on Windows <code>bun</code> installs contained only on the drive Windows is installed on. <a href="/docs/limitations/">Known limitations</a> lists the rest.</p>',
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
    a: "Your project directory, including its lockfile and node_modules, and a throwaway home directory of its own. Environment variables are scrubbed. It can write to the project and that home. It can read the project's .git and cannot write it. Outbound connections are limited to an allowlist that by default names the npm registry and the OSV vulnerability API, and GitHub's download hosts for Bun.",
    aHtml:
      "<p>Your project directory, including its lockfile and <code>node_modules</code>, and a throwaway home directory of its own. Environment variables are scrubbed. It can write to the project and that home. It can read the project's <code>.git</code> and cannot write it. Outbound connections are limited to an allowlist that by default names the npm registry and the OSV vulnerability API, and GitHub's download hosts for Bun.</p>",
  },
  {
    group: 'Containment',
    q: 'Is macOS protected the same way as Windows and Linux?',
    a: 'No. On Windows and Linux a contained install cannot read your home directory. On macOS the Seatbelt profile allows filesystem reads and denies the known credential stores by path. SSH keys, cloud credentials and your npm token are out of reach on all three. On macOS other files in your home, other projects included, can still be read by absolute path. Writes and outbound connections are contained on macOS too.',
    aHtml:
      '<p><strong>No.</strong> On Windows and Linux a contained install cannot read your home directory. On macOS the Seatbelt profile allows filesystem reads and denies the known credential stores by path. SSH keys, cloud credentials and your npm token are out of reach on all three. On macOS other files in your home, other projects included, <em>can</em> still be read by absolute path. Writes and outbound connections are contained on macOS too.</p>',
  },
  {
    // npm v12 shipped on 2026-07-08 with lifecycle scripts blocked by default.
    // Anyone who knows that will check this claim, so the site answers it
    // rather than waiting to be corrected. The honest answer is also the
    // stronger one, and every limb of it is checkable.
    group: 'Containment',
    q: 'npm 12 blocks install scripts by default. Is this still needed?',
    a: 'It closes the biggest hole, and nvx is about what is left. Packages that genuinely need a build step get approved, and that approval is permanent, so a later compromise of an approved package inherits it. Your bundler, test runner and dev server evaluate package code, which no script setting touches. A git dependency can override the git binary through .npmrc and run despite ignore-scripts. Older npm and yarn classic have no per-package mechanism at all. Containment is also a different thing from detection. The packages in the August 2026 ChainDrop compromise carried valid GitHub Actions provenance.',
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
    a: 'At an interactive terminal nvx asks whether to allow that host, and a yes lasts for that run only. With nobody to answer, it refuses the connection and names the host. To allow a host for good, add it to isolation.network.allow_hosts in a policy file. nvx will not honour a project file that widens the allowlist until you have approved that file. Passing -y or --agent-mode, or setting NVX_YES, approves neither the host nor the file, because an agent will answer yes to anything.',
    aHtml:
      '<p>At an interactive terminal nvx asks whether to allow that host, and a yes lasts for that run only. With nobody to answer, it refuses the connection and names the host.</p><p>To allow a host for good, add it to <code>isolation.network.allow_hosts</code> in a policy file. nvx will not honour a project file that widens the allowlist until you have approved that file. Passing <code>-y</code> or <code>--agent-mode</code>, or setting <code>NVX_YES</code>, approves neither the host nor the file, because an agent will answer yes to anything.</p>',
  },
  {
    group: 'Using it',
    q: 'What does the sandbox cost in speed?',
    // Figures as docs/enforcement-matrix.md cites them, under "Measured costs
    // and platform floors". The 75 ms is scripts/bench.py, which runs with
    // isolation off, so it is the shim's dispatch and nothing more.
    a: "On Windows a contained command costs a few hundred milliseconds to about a second, and a project's first contained run takes a few seconds. Measured on Windows 11, a project's first contained run took about 2.4 s and each one after took about 390 ms. A second Windows 11 machine, measured on 2026-08-29, gave 2.9 s first and 785 ms steady, the median of 8 runs. A command that is not contained pays only the shim's dispatch, about 75 ms on Windows from three runs on one machine. No figure has been established on Linux or macOS.",
    aHtml:
      "<p>On Windows a contained command costs <strong>a few hundred milliseconds to about a second</strong>, and a project's first contained run takes a few seconds. Measured on Windows 11, a project's first contained run took about 2.4 s and each one after took about 390 ms. A second Windows 11 machine, measured on 2026-08-29, gave 2.9 s first and 785 ms steady, the median of 8 runs.</p><p>A command that is not contained pays only the shim's dispatch, about 75 ms on Windows from three runs on one machine. No figure has been established on Linux or macOS. The <a href=\"https://github.com/fstubner/nvx/blob/main/docs/enforcement-matrix.md#measured-costs-and-platform-floors\">enforcement matrix</a> has the measurements.</p>",
  },
  {
    group: 'Using it',
    q: 'Can I install it from winget, Homebrew, Scoop or npm?',
    a: 'npm, yes. npm install -g @fstubner/nvx installs the binary for your platform and runs no install script. winget, Homebrew and Scoop not yet. The install script and the prebuilt release binaries work on all three platforms.',
    aHtml:
      '<p>npm, yes. <code>npm install -g @fstubner/nvx</code> installs the binary for your platform and runs no install script. winget, Homebrew and Scoop not yet. The install script and the prebuilt release binaries work on all three platforms.</p>',
  },
  {
    group: 'Using it',
    q: 'Can I use it in CI?',
    a: "Yes. Once nvx install has added the project's version, the shims run it with no shell setup. A CI step only needs ~/.nvx/bin on PATH. With no terminal attached every prompt is refused, so a check that would ask fails the step instead of waiting. NVX_YES=true approves the install-time checks, and each approval is printed and recorded. It does not approve a new egress host or a project policy that widens the sandbox. nvx policy check gives CI a distinct exit code for each kind of failure.",
    aHtml:
      "<p>Yes. Once <code>nvx install</code> has added the project's version, the shims run it with no shell setup. A CI step only needs <code>~/.nvx/bin</code> on <code>PATH</code>. With no terminal attached every prompt is refused, so a check that would ask fails the step instead of waiting.</p><p><code>NVX_YES=true</code> approves the install-time checks, and each approval is printed and recorded. It does not approve a new egress host or a project policy that widens the sandbox. <code>nvx policy check</code> gives CI a distinct exit code for each kind of failure.</p>",
  },
  {
    group: 'Using it',
    q: 'How do I uninstall it?',
    a: 'Take back what nvx granted, then delete it. On Windows, if you ever ran nvx setup, run nvx setup --undo from an Administrator terminal. Run nvx grants reset --all. Then delete ~/.nvx, remove the nvx lines from your shell profile, and take ~/.nvx/bin off your PATH. Installation has the steps in order.',
    aHtml:
      '<p>Take back what nvx granted, then delete it. On Windows, if you ever ran <code>nvx setup</code>, run <code>nvx setup --undo</code> from an Administrator terminal. Run <code>nvx grants reset --all</code>. Then delete <code>~/.nvx</code>, remove the nvx lines from your shell profile, and take <code>~/.nvx/bin</code> off your <code>PATH</code>. <a href="/docs/install/#uninstall">Installation</a> has the steps in order.</p>',
  },
];
