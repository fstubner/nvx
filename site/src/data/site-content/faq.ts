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
    a: 'nvx runs npm install and npx inside an OS sandbox on Windows, macOS and Linux, so a package cannot read your credentials or reach a host you did not allow. You and your agent type the same commands. It also installs, switches and pins Node.js and Bun versions. It is one static binary with no dependencies.',
    aHtml:
      '<p>nvx runs <code>npm install</code> and <code>npx</code> inside an OS sandbox on Windows, macOS and Linux, so a package cannot read your credentials or reach a host you did not allow. You and your agent type the same commands. It also installs, switches and pins Node.js and Bun versions. It is one static binary with no dependencies.</p>',
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
    // error in sandbox_landlock_linux.go, the `ip` lookup in
    // sandbox_network_linux.go and seatbeltExecPath. No Windows or macOS
    // version floor is stated because none has been established.
    q: 'What does it need to run?',
    a: 'Windows on x64, macOS on Apple silicon or Intel, or Linux on x86_64 or arm64. nvx is one static binary and needs nothing installed alongside it. On Linux the sandbox needs kernel 5.13 or later with Landlock enabled and unprivileged user namespaces. The network allowlist also needs the ip command from iproute2. When one is missing, contained commands refuse to run, so nothing runs uncontained. nvx downloads the glibc build of Node.js and Bun, so Alpine and other musl systems are refused. On Ubuntu 23.10 and later, AppArmor can refuse the sandbox its namespaces. nvx doctor checks whether a contained process can start. On macOS the sandbox uses sandbox-exec, which ships with macOS.',
    aHtml:
      '<p>Windows on x64, macOS on Apple silicon or Intel, or Linux on x86_64 or arm64. nvx is one static binary and needs nothing installed alongside it.</p><p>On Linux the sandbox needs kernel 5.13 or later with Landlock enabled and unprivileged user namespaces. The network allowlist also needs the <code>ip</code> command from iproute2. When one is missing, contained commands refuse to run, so nothing runs uncontained. nvx downloads the glibc build of Node.js and Bun, so Alpine and other musl systems are refused. On Ubuntu 23.10 and later, AppArmor can refuse the sandbox its namespaces. <code>nvx doctor</code> checks whether a contained process can start. On macOS the sandbox uses <code>sandbox-exec</code>, which ships with macOS.</p>',
  },
  {
    group: 'Basics',
    q: 'Does it replace nvm, fnm or volta?',
    // nvm and NVM for Windows are unrelated projects. NVM for Windows'
    // v2 (2026-09-02) added the same per-directory auto-switching and
    // auto-install this answer used to claim as a Windows gap, so the
    // comparison here is now about containment, not about switching.
    a: 'Yes. nvx installs, switches, pins and auto-switches on cd just as they do. On Windows, that includes NVM for Windows, a separate project despite the name. What nvx adds beyond any of them is containment. Installs run inside an OS sandbox with a scrubbed environment and an egress allowlist, on Windows, macOS and Linux. The platforms differ in the details. Files outside your home directory stay readable on macOS, and on Windows bun installs contained only on the drive Windows is installed on. Known limitations lists the rest.',
    aHtml:
      '<p>Yes. nvx installs, switches, pins and auto-switches on <code>cd</code> just as they do. On Windows, that includes NVM for Windows, a separate project despite the name. What nvx adds beyond any of them is containment. Installs run inside an OS sandbox with a scrubbed environment and an egress allowlist, on Windows, macOS and Linux.</p><p>The platforms differ in the details. Files outside your home directory stay readable on macOS, and on Windows <code>bun</code> installs contained only on the drive Windows is installed on. <a href="/docs/limitations/">Known limitations</a> lists the rest.</p>',
  },
  {
    group: 'Basics',
    q: 'Do I have to change how I run npm?',
    a: 'No. nvx puts shims on PATH, so npm install is still npm install. It is contained when it runs code you did not write, and nothing else about your workflow changes.',
    aHtml:
      '<p>No. nvx puts shims on <code>PATH</code>, so <code>npm install</code> is still <code>npm install</code>. It is contained when it runs code you did not write, and nothing else about your workflow changes.</p>',
  },
  {
    group: 'Basics',
    q: 'Can one project use both Node.js and Bun?',
    a: 'Yes. nvx use node@20 and nvx use bun@1.2 activate independently in the same shell, and neither takes the other off PATH. Toolchains installed outside nvx stay visible and run alongside them, uncontained unless they run through nvx. The Docker provider picks its image from the active runtime, node:<version> or oven/bun:<version>. It has no setting for another image, so a stack that needs both in one container needs your own Dockerfile or docker-compose setup.',
    aHtml:
      '<p>Yes. <code>nvx use node@20</code> and <code>nvx use bun@1.2</code> activate independently in the same shell, and neither takes the other off <code>PATH</code>. Toolchains installed outside nvx stay visible and run alongside them, uncontained unless they run through nvx.</p><p>The Docker provider picks its image from the active runtime, <code>node:&lt;version&gt;</code> or <code>oven/bun:&lt;version&gt;</code>. It has no setting for another image, so a stack that needs both in one container needs your own Dockerfile or <code>docker-compose</code> setup.</p>',
  },
  {
    group: 'Containment',
    q: 'What can a contained install actually reach?',
    a: "Your project directory, including its lockfile and node_modules, and a throwaway home directory of its own. Environment variables are scrubbed, and the project's .env files are hidden. It can write to the project and that home. It can read the project's .git and cannot write it. Outbound connections are limited to an allowlist that by default names the npm registry, Yarn's download host for corepack and the OSV vulnerability API, and GitHub's download hosts for Bun.",
    aHtml:
      "<p>Your project directory, including its lockfile and <code>node_modules</code>, and a throwaway home directory of its own. Environment variables are scrubbed, and the project's <code>.env</code> files are hidden. It can write to the project and that home. It can read the project's <code>.git</code> and cannot write it. Outbound connections are limited to an allowlist that by default names the npm registry, Yarn's download host for corepack and the OSV vulnerability API, and GitHub's download hosts for Bun.</p>",
  },
  {
    group: 'Containment',
    q: 'Is macOS protected the same way as Windows and Linux?',
    a: "No. On Windows and Linux a contained install can read only what it is granted. On macOS the Seatbelt profile denies reads under your home directory and nvx's home, apart from the project, the runtimes and the folders a policy names, and allows reads elsewhere on the disk, such as other apps' temp files. SSH keys, cloud credentials and your npm token are out of reach on all three. Writes and outbound connections are contained on macOS too.",
    aHtml:
      "<p><strong>No.</strong> On Windows and Linux a contained install can read only what it is granted. On macOS the Seatbelt profile denies reads under your home directory and nvx's home, apart from the project, the runtimes and the folders a policy names, and allows reads <em>elsewhere</em> on the disk, such as other apps' temp files. SSH keys, cloud credentials and your npm token are out of reach on all three. Writes and outbound connections are contained on macOS too.</p>",
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
    a: "nvx refuses the connection, names the host and prints the command that allows it, nvx allow-host <host>, for you to run in your own terminal. It does not ask at a prompt, because a coding agent that drives a terminal could answer one. The command adds the host to isolation.network.allow_hosts in the project's .nvx-policy.json, or in ~/.nvx/policy.json with --global, and trusts that file. A project file that widens the allowlist is not honoured until you run nvx trust on it. -y, --agent-mode and NVX_YES approve neither. NVX_TRUST_YES=true allows an unknown host for one run without the command, for everything started from that environment.",
    aHtml:
      "<p>nvx refuses the connection, names the host and prints the command that allows it, <code>nvx allow-host &lt;host&gt;</code>, for you to run in your own terminal. It does not ask at a prompt, because a coding agent that drives a terminal could answer one.</p><p>The command adds the host to <code>isolation.network.allow_hosts</code> in the project's <code>.nvx-policy.json</code>, or in <code>~/.nvx/policy.json</code> with <code>--global</code>, and trusts that file. A project file that widens the allowlist is not honoured until you run <code>nvx trust</code> on it. <code>-y</code>, <code>--agent-mode</code> and <code>NVX_YES</code> approve neither. <code>NVX_TRUST_YES=true</code> allows an unknown host for one run without the command, for everything started from that environment. <a href=\"/docs/blocked/\">When nvx stops something</a> lists every refusal and its fix.</p>",
  },
  {
    group: 'Containment',
    q: 'Can I run a dev server, or reach a local service, from the sandbox?',
    a: 'Yes, with a flag on Windows and Linux. A server started in the sandbox reports itself listening, but your machine cannot reach it. Windows refuses connections into the sandbox, and a Linux sandbox has a network namespace of its own. Publish the port with --expose. nvx --expose 5173:8080 npx vite makes port 5173 inside reachable at 8080, and the two numbers must differ. For a service already running on your machine, use --connect for one run or allow_hosts in a policy. Containment has the details, macOS included.',
    aHtml:
      '<p>Yes, with a flag on Windows and Linux. A server started in the sandbox reports itself listening, but your machine cannot reach it. Windows refuses connections into the sandbox, and a Linux sandbox has a network namespace of its own. Publish the port with <code>--expose</code>. <code>nvx --expose 5173:8080 npx vite</code> makes port 5173 inside reachable at 8080, and the two numbers must differ.</p><p>For a service already running on your machine, use <code>--connect</code> for one run or <code>allow_hosts</code> in a policy. <a href="/docs/containment/#local-servers-and-services">Containment</a> has the details, macOS included.</p>',
  },
  {
    group: 'Containment',
    q: 'Does it work with AI coding agents?',
    a: "The agent types the same npm install and npx commands you would, and the nvx shims on PATH check and contain them the same way, so the packages it installs are contained. nvx contains packages, not agents. The agent's own code runs uncontained at the default level, and an agent with a shell of its own can run the commands a person can. Set --agent-mode in its environment so that nvx refuses instead of asking, and deny nvx trust, nvx allow-host and nvx --no-sandbox in its permissions. The agents page has a snippet for AGENTS.md and the steps for MCP servers.",
    aHtml:
      "<p>The agent types the same <code>npm install</code> and <code>npx</code> commands you would, and the nvx shims on <code>PATH</code> check and contain them the same way, so the packages it installs are contained.</p><p>nvx contains packages, not agents. The agent's own code runs uncontained at the default level, and an agent with a shell of its own can run the commands a person can. Set <code>--agent-mode</code> in its environment so that nvx refuses instead of asking, and deny <code>nvx trust</code>, <code>nvx allow-host</code> and <code>nvx --no-sandbox</code> in its permissions. <a href=\"/docs/agents/\">Agents and CI</a> has a snippet for AGENTS.md and the steps for MCP servers. <a href=\"https://github.com/fstubner/nvx/blob/main/SECURITY.md\">SECURITY.md</a> has the threat model and its limits.</p>",
  },
  {
    group: 'Using it',
    q: 'What does the sandbox cost in speed?',
    // Figures are the CHANGELOG's for 0.8.0 (shim, install) and the
    // containment figure measured on Windows 11 on 2026-10-07. Nothing here
    // is estimated, and each number carries its conditions.
    a: "Measured on Windows 11, a trivial contained command (nvx --strict node -e 0) took 312 ms against 53 ms for node alone. A command that is not contained pays only the shim. node --version through it took a median of 0.101 s against 0.052 s for node alone (21 interleaved runs), and 0.018 s through the shim on Linux. An install is two contained runs and the checks. Through the contained shim on machines busy with other work, a 321-package project with no lockfile took a median of 64.8 s on Windows and 61.4 s in a Linux container, and an install of one package took 8.8 s on Windows and 2.39 s on Linux (median of 5 interleaved runs). Those figures are the whole of what has been measured, and nothing has been measured on macOS.",
    aHtml:
      "<p>Measured on Windows 11, a trivial contained command (<code>nvx --strict node -e 0</code>) took 312 ms against 53 ms for node alone. A command that is not contained pays only the shim. <code>node --version</code> through it took a median of 0.101 s against 0.052 s for node alone (21 interleaved runs), and 0.018 s through the shim on Linux.</p><p>An install is two contained runs and the checks. Through the contained shim on machines busy with other work, a 321-package project with no lockfile took a median of 64.8 s on Windows and 61.4 s in a Linux container, and an install of one package took 8.8 s on Windows and 2.39 s on Linux (median of 5 interleaved runs). Those figures are the whole of what has been measured, and nothing has been measured on macOS. The <a href=\"https://github.com/fstubner/nvx/blob/main/docs/enforcement-matrix.md#measured-costs-and-platform-floors\">enforcement matrix</a> has older measurements.</p>",
  },
  {
    group: 'Using it',
    q: 'Does switching versions affect my other terminals?',
    a: 'No. nvx use and the switch on cd set PATH and NPM_CONFIG_PREFIX in the shell they run in, and change no system-wide path or link. A build running in another terminal is unaffected. Commands has the details.',
    aHtml:
      '<p>No. <code>nvx use</code> and the switch on <code>cd</code> set <code>PATH</code> and <code>NPM_CONFIG_PREFIX</code> in the shell they run in, and change no system-wide path or link. A build running in another terminal is unaffected. <a href="/docs/commands/#shells">Commands</a> has the details.</p>',
  },
  {
    group: 'Using it',
    q: 'Does nvx handle TypeScript and bundler commands?',
    a: 'Yes. Global and project-local tools such as tsc, ts-node, vite and webpack run on the selected Node.js version. Project-local tools are contained only at the strict isolation level, like your own code. Containment has the details.',
    aHtml:
      '<p>Yes. Global and project-local tools such as <code>tsc</code>, <code>ts-node</code>, <code>vite</code> and <code>webpack</code> run on the selected Node.js version. Project-local tools are contained only at the <code>strict</code> isolation level, like your own code. <a href="/docs/containment/#zero-config-sandbox">Containment</a> has the details.</p>',
  },
  {
    group: 'Using it',
    q: 'Can I install it from winget, Homebrew, Scoop or npm?',
    a: 'npm, yes. npm install -g @fstubner/nvx installs the binary for your platform and runs no install script. Type the scoped name. The unscoped nvx on npm is a different project. winget, Homebrew and Scoop not yet. The install script and the prebuilt release binaries work on all three platforms.',
    aHtml:
      '<p>npm, yes. <code>npm install -g @fstubner/nvx</code> installs the binary for your platform and runs no install script. Type the scoped name. The unscoped <code>nvx</code> on npm is a different project. winget, Homebrew and Scoop not yet. The install script and the prebuilt release binaries work on all three platforms.</p>',
  },
  {
    group: 'Using it',
    q: 'Can I use it in CI?',
    a: "Yes. Once nvx install has added the project's version, the shims run it with no shell setup. A CI step only needs ~/.nvx/bin on PATH. With no terminal attached every prompt is refused, so a check that would ask fails the step instead of waiting. NVX_YES=true approves the install-time checks, and each approval is printed and recorded. It does not approve a new egress host or a project policy that widens the sandbox. Per-check policy lines are the narrower choice. nvx policy check gives CI a distinct exit code for each kind of failure.",
    aHtml:
      "<p>Yes. Once <code>nvx install</code> has added the project's version, the shims run it with no shell setup. A CI step only needs <code>~/.nvx/bin</code> on <code>PATH</code>. With no terminal attached every prompt is refused, so a check that would ask fails the step instead of waiting.</p><p><code>NVX_YES=true</code> approves the install-time checks, and each approval is printed and recorded. It does not approve a new egress host or a project policy that widens the sandbox. Per-check policy lines are the narrower choice. <code>nvx policy check</code> gives CI a distinct exit code for each kind of failure. <a href=\"/docs/agents/#in-ci\">Agents and CI</a> has the details.</p>",
  },
  {
    group: 'Using it',
    q: 'How do I uninstall it?',
    a: 'Take back what nvx granted, then delete it. On Windows, if you ever ran nvx setup from a version before 0.8.0, run nvx setup again from an Administrator terminal. Run nvx grants reset --all. Then delete ~/.nvx, remove the nvx lines from your shell profile, and take ~/.nvx/bin off your PATH. Installation has the steps in order.',
    aHtml:
      '<p>Take back what nvx granted, then delete it. On Windows, if you ever ran <code>nvx setup</code> from a version before 0.8.0, run <code>nvx setup</code> again from an Administrator terminal. Run <code>nvx grants reset --all</code>. Then delete <code>~/.nvx</code>, remove the nvx lines from your shell profile, and take <code>~/.nvx/bin</code> off your <code>PATH</code>. <a href="/docs/install/#uninstall">Installation</a> has the steps in order.</p>',
  },
];
