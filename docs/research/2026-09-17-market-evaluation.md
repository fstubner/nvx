# nvx market evaluation and competitive map

Date of research: 2026-09-17. Builds on the 2026-09-16 pass (version-manager pain points, incident timeline, npm 12 script blocking, Socket/Snyk/StepSecurity/Chainguard positioning, mise April 2026 sandboxing, aube, agent sandbox capabilities). Those topics are referenced, not re-researched.

Conventions. Every number carries its source and date. "Measured" means fetched from an API or CSV during this pass. "Sourced" means quoted from a page or article. "Inference" is my judgement and is labelled as such. Where a number could not be found, the text says so instead of estimating.

nvx state at time of writing (measured from the GitHub API): 0 stars, created 2026-06-29, latest release v0.6.0 on 2026-09-11, 47 asset downloads summed across all 9 releases, zero Hacker News hits for "nvx node version manager sandbox".

---

## 1. Competitive map

### 1a. Version managers

Stars, push dates and release dates were fetched from the GitHub API on 2026-09-17. Homebrew install counts are from formulae.brew.sh analytics on the same day and only cover macOS and Linux Homebrew users.

| Tool | Stars | Latest release | Last push | Maintained | Windows native | Any security feature | Brew installs, 30d |
|---|---|---|---|---|---|---|---|
| nvm (nvm-sh) | 95,097 | v0.40.7, 2026-08-18 | 2026-09-11 | Yes | No (WSL only) | Verifies Node download checksums. Nothing at install time | 37,014 |
| nvm-windows | 47,698 | v2.0.0, 2026-09-02 (rewrite, then hotfixes) | 2026-09-16 | Yes, moved to Author Software Inc | Yes | Signed builds. Paid "Certified" tier announced for Sept 2026 with version allow/block lists, custom mirrors, AD/Intune, SBOM/VEX add-on, and release notes cite "lockdown of Node.js/V8 permissions, publisher trust, and configurable cooldown periods". No install sandbox. Pricing not published ([docs](https://docs.nvm-windows.com/features/newv2/), [release](https://github.com/nvm-windows/nvm/releases/tag/v2.0.0)) | n/a |
| fnm | 26,872 | v1.39.0, 2026-03-06 | 2026-07-24 | Yes, slow cadence | Yes | None | 4,518 |
| Volta | 13,064 | v2.0.2, 2024-12-05 | 2025-11-15 | No. README says "Volta is unmaintained", pinned issue #2080 (2025-11-14) recommends mise | Yes | None | 200 |
| asdf | 25,592 | v0.20.0, 2026-07-07 | 2026-09-03 | Yes | No | None | 4,612 |
| mise | 33,994 | v2026.9.10, 2026-09-16 | 2026-09-16 | Yes, monthly releases | Yes for version management. Sandbox is macOS and Linux only, "a warning is printed and the command runs unsandboxed" on Windows ([docs](https://mise.jdx.dev/sandboxing.html)) | Opt-in process sandbox (`--deny-all`, `--allow-net`), Seatbelt on macOS, Landlock on Linux. v2026.9.10 added `allow_exotic_deps` for its aube-backed npm installs | 47,104 |
| n | 19,513 | v10.2.0, 2025-05-21 | 2026-08-30 | Yes, slow | No | None | not checked |
| nvs | 2,962 | v1.7.1, 2023-08-16 | 2026-09-13 | Barely (no release in 3 years) | Yes | None | not checked |
| nodenv | 2,415 | v1.6.2, 2025-07-30 | 2026-09-14 | Yes | No | None | not checked |
| proto (moonrepo) | 1,415 | v0.62.2, 2026-09-11 | 2026-09-11 | Yes | Yes | Verifies tool downloads with SHA256, minisign, and since v0.61 GPG. Opt-in lockfile of tool checksums. No install-time containment ([proto v0.61](https://moonrepo.dev/blog/proto-v0.61)) | not checked |
| nvx | 0 | v0.6.0, 2026-09-11 | 2026-09-14 | Yes, one maintainer | Yes | Kernel sandbox around installs on all three OSes, egress allowlist, env scrub, OSV, typosquat, release-age, policy file, CI exit codes | not listed |

Sourced facts worth pulling out.

- No version manager other than nvx contains installs. mise is the only one with any sandbox and it does not run on Windows. proto and nvm-windows verify the runtime download and stop there.
- Volta is dead by its own README. Its brew installs (200 in 30 days) are 1/235th of mise's.
- Homebrew analytics give a rough share of macOS and Linux users who installed a Node-capable version manager in the last 30 days. Of the five counted (nvm, mise, fnm, asdf, volta, total 93,448), mise took 50 percent and nvm 40 percent. That is a proxy, not a survey, and it excludes Windows entirely.
- nvm-windows 2.0 is the first Windows version manager to sell a paid governance tier. It does not sandbox. Its "cooldown periods" language overlaps nvx's release-age window.

### 1b. Package managers, default versus opt-in

This is the table that matters most for nvx's pitch. Column values are as documented on 2026-09-17.

| | npm 12 (12.0.2, 2026-07-27) | pnpm 11 (2026-04-28) | Yarn 4.14+ (2026-04-16) | Bun 1.4.2 (2026-09-05) | aube 2.2.x (jdx, 2026) |
|---|---|---|---|---|---|
| Dependency lifecycle scripts | Blocked by default, `allowScripts` allowlist, implicit node-gyp builds also blocked ([changelog](https://docs.npmjs.com/cli/v12/using-npm/changelog/), [GitHub changelog](https://github.blog/changelog/2026-06-09-upcoming-breaking-changes-for-npm-v12/)) | Blocked by default, `allowBuilds` map, `strictDepBuilds` true ([pnpm 11](https://pnpm.io/blog/releases/11.0)) | `enableScripts: false` default for new projects. Migrated projects get an explicit `enableScripts: true` written in, so upgrades stay opted in ([issue thread](https://github.com/yarnpkg/berry/issues/6258)) | Blocked except a built-in trusted list. `trustedDependencies` replaces the list rather than extends it ([docs](https://bun.com/docs/pm/lifecycle)) | Blocked except built-in trusted list, explicit deny wins ([security](https://aube.sh/security)) |
| Release-age cooldown | `min-release-age` exists since 11.10.0, opt-in | 1440 minutes default, `minimumReleaseAgeStrict` false | `npmMinimalAgeGate` opt-in | `install.minimumReleaseAge` opt-in, default null. Open bugs in 2026 where lockfile-pinned versions and global config bypass it ([#30525](https://github.com/oven-sh/bun/issues/30525), [#30750](https://github.com/oven-sh/bun/issues/30750)) | 1440 minutes default |
| Git / remote tarball deps | `allow-git` and `allow-remote` default `none` | `blockExoticSubdeps` true | `approvedGitRepositories` | Trusted list applies only to registry packages | `blockExoticSubdeps` true |
| Maintainer-trust downgrade check | No | `trustPolicy` opt-in | No | No | `trustPolicy: no-downgrade` default |
| Sandbox around approved scripts | No | No | No | No | `jailBuilds` default false, "planned to flip to true in the next major". macOS Seatbelt, Linux Landlock plus seccomp. On Windows the doc says only "env is scrubbed and HOME is redirected to a temporary directory" |
| Env secret scrubbing | No | No | No | No | Yes inside the jail (NPM_TOKEN, GITHUB_TOKEN, AWS_*, SSH_AUTH_SOCK) |
| Egress allowlist for scripts | No | No | No | No | Doc says the jail restricts network on all platforms. Mechanism on Windows is not described and I could not verify it |
| Contains `npx` / `bunx` executor runs | No | No | No | No | Not documented |

Sourced facts and one inference.

- Every major package manager now blocks dependency scripts by default. That happened between 2025 (pnpm 10, bun) and July 2026 (npm 12). The 2026-09-16 pass covered the npm 12 change. The new point here is that Yarn flipped too (4.14.0) and aube ships the strictest defaults of the five.
- The thing none of the five do is run the scripts they *do* approve inside a kernel boundary, scrub the environment, or gate egress. aube is the only one heading there, and its jail is off by default and has no kernel mechanism on Windows.
- Inference. nvx's install-script story is no longer differentiated on "scripts are blocked". It is differentiated on what happens to the scripts a team has approved (native modules, esbuild, sharp, playwright) and on `npx` runs, which no package manager contains at all.

### 1c. Install-time containment tools

| Tool | Stars / status | Mechanism | Windows native | Positioned for npm install | Notes |
|---|---|---|---|---|---|
| @anthropic-ai/sandbox-runtime | 5,253 stars, v0.0.76 on 2026-09-10 | macOS sandbox-exec, Linux bubblewrap, Windows "alpha" using a dedicated `srt-sandbox` local user, WFP egress fence, per-session ACLs ([repo](https://github.com/anthropics/sandbox-runtime)) | Yes, alpha | No, generic process wrapper | Claude Code itself still says "Native Windows is not supported" for its sandbox ([docs](https://code.claude.com/docs/en/sandboxing)) |
| landstrip | 75 stars, created 2026-06-01, LGPL | Landlock, Seatbelt, "AppContainer or restricted users on Windows" ([repo](https://github.com/landstrip/landstrip)) | Yes | No, agent sandbox with policy files in the sandbox-runtime format | Single maintainer per a third-party evaluation |
| nono (nolabs-ai) | 4,114 stars, created 2026-01-31, Apache 2 | Landlock, Seatbelt, credential proxy | No, "Windows requires WSL2" ([Help Net Security](https://www.helpnetsecurity.com/2026/07/27/nono-open-source-ai-agent-sandboxing/)) | No | Strong Linux/macOS agent sandbox |
| node-safe (berstend) | 217 stars, last push 2023-06-15 | macOS Seatbelt only | No | Yes, wraps node/npm/npx/yarn | Dead |
| Docker Sandboxes (sbx) | Docker product, microVM, free per Docker forum confirmation | Custom VMM, per-sandbox Docker daemon, deny-all egress policy | Yes, Windows 11 x64 with HypervisorPlatform, `winget install Docker.sbx` ([docs](https://docs.docker.com/ai/sandboxes/)) | No, agent-oriented, but any command runs inside | Heaviest and strongest boundary. Not transparent to the shell |
| mise `exec --deny-all` | Part of mise | Seatbelt, Landlock | No, prints a warning and runs unsandboxed | No | Users reported abort traps and unsupported per-host network rules on macOS 26.4 ([discussion](https://github.com/jdx/mise/discussions/8878)) |
| Firejail, bubblewrap wrappers | Linux only | namespaces | No | No | General knowledge, not re-verified this pass |
| Windows Sandbox (.wsb) | Windows feature | Full VM | Pro/Enterprise only, not Home ([Microsoft Learn](https://learn.microsoft.com/en-us/windows/security/application-security/application-isolation/windows-sandbox/)) | No, manual workflow, no persistence | XDA (July 2026) describes devs using it to vet npm deps by hand |
| Microsoft MXC (microsoft/mxc) | 1.3k stars, MIT, "early preview with known overly-permissive policies" | AppContainer / BaseContainer on Windows, standalone `wxc-exec.exe`, JSON policy ([repo](https://github.com/microsoft/mxc), [Windows blog 2026-06-02](https://blogs.windows.com/windowsdeveloper/2026/06/02/windows-platform-security-for-ai-agents/)) | Yes, Windows 11 24H2+ | No, generic | Backs Copilot CLI local sandbox. Blog lists OpenAI, NVIDIA, OpenClaw, Hermes as integrators |
| Codex CLI native Windows sandbox | OpenAI, shipped March 2026 | `elevated` (dedicated sandbox users, ACLs, firewall rules) or `unelevated` (restricted token, ACLs) ([docs](https://learn.chatgpt.com/docs/windows/windows-sandbox)) | Yes | No, agent-bound | OpenAI evaluated AppContainer and rejected it ([OpenAI post](https://openai.com/index/building-codex-windows-sandbox/)). Aug 2026 bug: `codex exec` falls back to restricted token despite `elevated` ([#40158](https://github.com/openai/codex/issues/40158)) |
| Aikido safe-chain | Free, open source | Local proxy, malware intel, 48h age gate by default | Yes, PowerShell installer ([repo](https://github.com/AikidoSec/safe-chain)) | Yes | Detection, not containment |
| Socket Firewall Free | Free | Ephemeral proxy, blocks confirmed malware only, warns on AI-flagged ([docs](https://docs.socket.dev/docs/socket-firewall-free)) | Yes | Yes | Detection, not containment. Cached packages bypass it |
| Veracode Package Firewall (Phylum) | Quote-based | Registry proxy plus policy. Phylum's "Birdcage" sandbox (`phylum npm install`) restricted fs, network, env ([Phylum docs](https://docs.phylum.io/)) | Not verified for Birdcage | Yes | Only prior product that combined detection with an install sandbox. Now folded into Veracode SCA |
| jagreehal/sandbox-node | Small OSS | Docker/Podman | Via Docker Desktop | Yes | Container, not native |

Sourced conclusion. On native Windows, without WSL2, without a VM, the set of tools that put an arbitrary command under a kernel boundary is: MXC (preview), Codex's own sandbox (agent-bound), sandbox-runtime (alpha), landstrip (75 stars), and nvx. None of the other four is positioned for package installs, ships as a version manager, or has a policy file with CI semantics.

### 1d. Detection and policy SaaS

| Vendor | Published price | Free tier | Windows dev-machine component | Containment |
|---|---|---|---|---|
| Socket | Team $25/dev/mo, Business $50/dev/mo, Enterprise custom, up to 20 percent off annual ([pricing](https://socket.dev/pricing)). $60M Series C at $1B valuation, May 2026 | Yes, 1,000 scans/mo. Socket Firewall free | Firewall CLI | No |
| Snyk | Team $25/contributing dev/mo, 5 to 10 seats. Ignite quoted around $1,260/dev/yr by third parties. Credit model since 2026-01-01 | Yes | CLI | No |
| Aikido | Basic $350/mo, Pro $700/mo, Advanced $1,050/mo, each including 10 users (July 2026 figures, up from $300/$600 in May) | Yes, 2 users, 10 repos | Device Protection, npm/PyPI blocking free, extras "an additional subscription per device", price not published ([page](https://www.aikido.dev/protect/device-protection)) | No |
| Endor Labs | Quote only, per contributor per year. Third-party estimate "$20,000+/year" | No | No | No |
| Semgrep Supply Chain | Free to 10 contributors. Teams cited at $30 to $40/contributor/mo, sources conflict on per-module versus bundle | Yes | No | No |
| Phylum / Veracode | Quote. Third parties cite $15k to $100k+/yr for the platform | No | Package Firewall proxy | Historically yes (Birdcage) |
| GitHub | Secret Protection $19 and Code Security $30 per active committer per month, $49 together. Dependabot free | Dependabot free | No | No |
| StepSecurity | Dev Machine Guard $8/device/mo standalone, Secure Registry included ([pricing](https://www.stepsecurity.io/pricing)) | Open-source local mode | Yes, macOS/Windows/Linux, inventories packages and AI agents, checks for binding.gyp | No |
| Chainguard Libraries | Quote, priced by ecosystem and org size. Was free until 2026-06-30, now available to Free and Starter console tiers per docs (Aug 2026) | Yes | No, it is a registry | No |
| GitGuardian Developer Endpoint Protection | Per endpoint per year, quote (announced 2026-06-16) | No | Yes, secrets scanning on laptops | No |
| 1Password | No standalone developer tier. Business $7.99/user/mo annual | Individual plans | SSH agent, secrets injection | No |
| nvm-windows Certified | Not published, "available September 2026" | Community edition free | Yes | No |

### 1e. Scored matrix

Ticks are as documented on 2026-09-17. "Agent-safe approval" means a non-interactive `-y` style flag or the agent itself cannot widen network or trust boundaries.

| Tool | Windows native | Containment (not just detection) | Agent-safe approval model | Policy-as-code | Zero SaaS | Maintained |
|---|---|---|---|---|---|---|
| nvx | Yes | Yes (AppContainer, Landlock+netns+seccomp, Seatbelt) | Yes. README: "-y and --agent-mode deliberately do not" widen egress or trust | Yes (`.nvx-policy.json`, org baseline, `policy check` exit codes) | Yes | Yes, single maintainer |
| mise | Yes for versions, no for sandbox | Yes on macOS/Linux | Partial. Flags are per-invocation, an agent can omit them | Partial (settings file) | Yes | Yes |
| aube | Yes, but jail is env-scrub only on Windows | Partial (jail off by default, macOS/Linux kernel only) | Partial. Approval is per-package config an agent can edit | Yes (project config, `paranoid`) | Yes | Yes |
| npm 12 / pnpm 11 / yarn 4.14 / bun | Yes | No | No. Agent can add to `allowScripts` / `allowBuilds` / `trustedDependencies` | Partial (per-project config) | Yes | Yes |
| Codex CLI sandbox | Yes | Yes | Partial. `--yolo` disables both. `approval_policy=never` does not widen | Config profiles | Yes | Yes |
| Claude Code sandbox | No (WSL2) | Yes on macOS/Linux/WSL2 | Partial. Claude may retry with `dangerouslyDisableSandbox`, prompt in Manual, classifier in auto. `allowUnsandboxedCommands:false` closes it | Settings JSON, managed settings | Yes | Yes |
| Cursor sandbox | No (Linux sandbox in WSL2 per Cursor blog 2026-02-18) | Yes | User approves step-outside requests | `sandbox.json`, enterprise enforcement | Yes | Yes |
| Copilot CLI (MXC) | Yes, preview | Yes | Not documented | JSON policy, Intune | Yes | Yes |
| Docker Sandboxes | Yes | Yes (microVM) | Policy is host-side, agent cannot edit | Sandbox Kits YAML | Yes (governance is paid) | Yes |
| sandbox-runtime | Alpha | Yes | n/a, library | JSON | Yes | Yes |
| landstrip | Yes | Yes | n/a | JSON | Yes | Single maintainer |
| nono | No | Yes | Yes (kernel-enforced, cannot escape) | Profiles | Yes | Yes |
| Socket / Aikido safe-chain / StepSecurity | Yes | No | n/a | Org policy in SaaS | No | Yes |
| nvm / fnm / n / nvs / nodenv / asdf / proto | Mixed | No | n/a | No | Yes | Mixed, Volta dead |

Where nvx is the only tick. Read the rows above literally: nvx is the only tool that ticks all six columns. Per column it is not unique anywhere. Windows-native containment is shared with Codex, MXC/Copilot, Docker sbx, sandbox-runtime (alpha) and landstrip. Agent-safe approval is shared with nono and Docker sbx. Policy-as-code is shared with aube, Cursor and Docker Kits.

Where nvx is beaten. Isolation strength, by Docker Sandboxes (separate kernel). Detection quality, by Socket and Aikido (human-reviewed malware intel, nvx uses OSV plus a heuristic). Package-manager-level defaults, by aube (trust-downgrade check, exotic-dep block, default cooldown). Reach and distribution, by everyone in the table. macOS read containment, by nono and Claude Code (nvx's README concedes reads are not contained on macOS).

---

## 2. Market sizing

All figures sourced. Nothing here is estimated.

Developer population.

- 47.2 million developers worldwide, Q1 2025, of which 36.5 million professional (SlashData, [Developer Population Trends 2025](https://www.slashdata.co/post/global-developer-population-trends-2025-how-many-developers-are-there)).
- JavaScript community "20 to 28 million users", 2025 (SlashData, same report). 59.6 percent of surveyed developers use JS/TS (Developer Nation wave 29, Nov 2024 to Jan 2025).
- npm's homepage claims "more than 17 million developers" (undated marketing claim, [npmjs.com](https://www.npmjs.com/)).
- GitHub Octoverse 2025: 180 million developers on GitHub, TypeScript the most-used language ([GitHub blog](https://github.blog/news-insights/octoverse/octoverse-a-new-developer-joins-github-every-second-as-ai-leads-typescript-to-1/)). Octoverse 2026 not yet published.

Node.js share.

- Stack Overflow 2025 (49,000+ responses, published 2025-12-29): Node.js is the top web framework at 48.7 to 49.1 percent depending on cut ([survey](https://survey.stackoverflow.co/2025/technology)). The 2026 survey opened 2026-06-23 and has not published.
- State of JS 2025: Node.js used by 90 percent of respondents as a backend runtime, Bun 21 percent, Deno 11 percent ([InfoQ](https://www.infoq.com/news/2026/03/state-of-js-survey-2025)).

Windows share.

- Stack Overflow 2025, all developers: Windows 56.7 percent personal, 49.5 percent professional. macOS 32.7 / 32.9. WSL personal use 15.9 percent, down from 17.1.
- State of Devs 2025 (Devographics, 8,717 respondents, JS-heavy audience, June 2025): macOS 57 percent, Windows 28 percent, Linux 15 percent ([InfoWorld](https://www.infoworld.com/article/4021972/javascript-macos-lead-usage-in-worldwide-developer-survey.html)).
- Node.js official download metrics by OS (measured from nodejs.org/metrics/summaries/os.csv). The public CSV stops at 2023-07-12. For the last 30 complete days in the file (2023-06-12 to 2023-07-11): Linux 44.3 percent, Windows 23.0 percent, headers 12.3 percent, macOS 10.4 percent, source 6.9 percent. Windows is 69 percent of the combined Windows plus macOS binary downloads. Linux is inflated by CI and nvm auto-downloads (NodeSource made the same point in 2018). No newer official OS split was found.
- Inference. The Windows share of Node developers sits somewhere between the JS-survey figure (28 percent) and the all-developer figure (50 to 57 percent). The download data points to the higher end for actual Windows binaries. Either way the Windows Node population is tens of millions of installs a year and it is the segment every sandboxing tool except Codex and MXC has skipped.

Version manager usage.

- The only survey figure found: "3 out of every four users manage their Node JS with a version manager" and "52 percent of this population use NVM", attributed to a Node.js Foundation user survey by a secondary source ([codeless.co](https://codeless.co/node-js-statistics/)). The question existed in the 2016 to 2018 Node.js surveys. The 2018 PDF could not be text-extracted here. No 2024 to 2026 figure exists. State of JS 2025 does not ask. A Node.js User Survey 2026 is open on the Linux Foundation's SurveyMonkey.
- Homebrew 30-day installs on 2026-09-17 (measured): node 147,637, mise 47,104, nvm 37,014, asdf 4,612, fnm 4,518, volta 200, aube 100.

Enterprise versus individual.

- SlashData: 36.5 million of 47.2 million developers are professional (77 percent), Q1 2025.
- Node.js 2018 survey: 43 percent report some enterprise work (InfoQ, May 2018).
- No Node-specific 2026 split found.

Install-script disabling and supply-chain concern.

- No survey reporting the fraction of developers who set `ignore-scripts` was found. Searches across Snyk, Sonatype, State of JS, Stack Overflow and JetBrains returned guidance, not measurement. This number is unavailable.
- Closest proxies. Endor Labs, survey of 605 IT professionals, published 2026-04-01: 81 percent say malicious OSS is a top priority, 88 percent know the first days after release are the riskiest window, 21 percent enforce cooldowns, 51 percent found suspected or confirmed malicious packages in 2025 ([Endor Labs](https://www.endorlabs.com/learn/new-research-malware-in-open-source-ecosystems-surges-14x-as-attackers-hijack-trusted-packages)).
- Sonatype 2026 report: 454,600 new malicious packages in 2025, up 75 percent, "over 99 percent of open source malware occurred on npm" ([Sonatype](https://www.sonatype.com/state-of-the-software-supply-chain/2026/open-source-malware)).
- ReversingLabs 2026 report: 10,819 malicious npm packages detected in 2025, nearly 90 percent of detections.
- Snyk's 2023 report (last survey edition found): only 40 percent deploy security tooling into IDEs, "an even smaller percentage using them locally on the command line".
- Inference. Concern is measured at the organisation level, not the developer level. The 21 percent cooldown enforcement figure is the best available signal that defaults, not intent, drive protection. That favours tools that are on by default, which is nvx's model and also npm 12's.

---

## 3. Willingness to pay and adoption signals

What is free (sourced, as of 2026-09-17).

- Default script blocking and cooldowns in npm 12, pnpm 11, Yarn 4.14, Bun, aube.
- Socket Firewall Free, Aikido safe-chain, StepSecurity Dev Machine Guard local mode, Datadog supply-chain firewall, Docker Sandboxes core, MXC (MIT), sandbox-runtime, landstrip, nono, Dependabot.
- Copilot CLI local sandbox is "included in the standard GitHub Copilot seat".

What is paid.

- Detection SaaS is priced per developer per month at $25 (Socket Team, Snyk Team) to $50 (Socket Business), or flat per org (Aikido $350 to $1,050 per month for 10 users).
- Endpoint tier is priced per device: StepSecurity $8/device/mo, Aikido "additional subscription per device" (unpriced), GitGuardian per endpoint per year (unpriced).
- Governance on top of free cores: Docker AI Governance, nvm-windows Certified (unpriced, Sept 2026), Socket Firewall Enterprise.

Evidence that anyone pays for local dev-machine security specifically.

- Yes, but only organisations, through MDM. Aikido, GitGuardian (June 2026) and StepSecurity all describe fleet deployment via Jamf, Intune, Kandji and "request-and-approval workflows". StepSecurity explicitly prices the non-developer device at $8 and bundles developer devices into the per-contributor licence.
- No individual-paid dev-machine security product was found. Every individual-facing tool in this space is free.
- Inference. The category "developer endpoint protection" is forming in 2026 and buyers are CISOs, not developers. Pricing anchors are $8/device and $25/dev. A tool that wants to be paid here needs the MDM story, central policy, and audit export. nvx has the policy file and `audit export`. It has no fleet deployment, no dashboard and no SOC 2, which is the same gap Author Software admits for nvm-windows Certified.

Adoption curves for comparable tools.

- fnm: Show HN 2019-01-24, 87 points, 69 comments. 26,872 stars on 2026-09-17. A third-party guide put it at about 19k in Feb 2026, which I could not reconcile with the measured figure and so treat as unreliable. Homebrew 4,518 installs in 30 days. Last release March 2026.
- mise: Show HN as rtx 2023-02-02. Renamed January 2024. HN front page 2024-04-29. About 27,600 stars in May 2026 per a third-party article, 33,994 measured on 2026-09-17, so roughly 6,400 stars in four months. Homebrew 800,211 installs in 365 days. Monthly releases. Its HN story submissions in 2026 mostly score under 40 points, so growth is not HN-driven.
- aube: created 2026-04-18. HN 2026-04-29, 32 points. 2,001 stars in five months. Homebrew 607 installs in 365 days. mise now installs npm tools through it.
- nvx: 47 downloads across nine releases in eleven weeks, 0 stars, not on winget, Homebrew, Scoop or npm, zero HN hits.
- Inference. The reference curve is aube, not mise: a jdx project with a built-in audience reached 2,000 stars and 600 brew installs in five months. nvx has none of that audience and is at zero. Distribution, not features, is the binding constraint.

Free OSS play, paid enterprise play, or both.

- Sourced pattern: every tool in this market that has a paid tier put a free tool on developer machines first (Socket Firewall, safe-chain, Dev Machine Guard, nvm-windows Community, Docker sbx).
- Inference. nvx is a free OSS play for at least the next two releases. A paid org-baseline tier (signed policy, SIEM export, fleet install) is plausible later and would price against StepSecurity's $8/device. Trying to charge before there are users would compete with free tools from funded vendors.

---

## 4. The "AI agent runs npm install" segment

Sizes (sourced, with caveats stated by the sources).

| Tool | Latest published figure | Date | Caveat |
|---|---|---|---|
| Codex (all surfaces) | 20 million active users (Tibo Sottiaux). 8 million weekly in mid-July 2026. 3 million weekly on 2026-04-08 | 2026-08-21 | No time window on the 20M figure. Not CLI-only |
| Claude Code | Weekly active users "doubled" between 2026-01-01 and 2026-02-12, $2.5B run-rate (Reuters, Feb 2026). Third-party estimates 1.6M to 4.2M WAU | 2026-02 | Anthropic publishes no absolute WAU |
| Cursor | "Over 70 percent of the Fortune 500" (May 2026), 3 million developers in India (July 2026), about $4B ARR (June 2026). Acquired by SpaceX, closed 2026-08-14 | 2026-07 | No total user count published |
| GitHub Copilot | 4.7 million paid subscribers (Microsoft FY26 Q2, 2026-01-28), 20 million all-time users (July 2025) | 2026-01 | Free and trial users excluded from paid figure |
| Windsurf / Devin Desktop | 1 million+ developers, 4,000+ enterprise customers (2025). Cascade end-of-life 2026-07-01 | 2025 | No 2026 figure from Cognition |
| Cross-tool survey | JetBrains AI Pulse, April 2026, 10,000+ developers: Copilot 29 percent work adoption, Cursor 18, Claude Code 18 | 2026-04 | Adoption at work, not sandbox use |

What each does today about sandboxing installs on native Windows (verified 2026-09-17).

| Agent | Native Windows sandbox | Mechanism | Who can widen network | Source |
|---|---|---|---|---|
| Claude Code | No. "Native Windows is not supported. On Windows, run Claude Code inside a WSL2 distribution" | Seatbelt, bubblewrap | User prompt on first new domain in Manual. In auto mode Claude names hosts per command and a classifier reviews. Claude may retry with `dangerouslyDisableSandbox` unless `allowUnsandboxedCommands:false` | [docs](https://code.claude.com/docs/en/sandboxing), feature request [#46740](https://github.com/anthropics/claude-code/issues/46740) (April 2026, still open) |
| Codex CLI | Yes, since March 2026, "experimental" label at that time | `elevated`: dedicated sandbox users, ACLs, firewall rules. `unelevated`: restricted token, ACLs | Network off by default under workspace-write, surfaces as approval. `--yolo` disables everything. WSL2 interop bug lets network_access reach the Windows host ([#43764](https://github.com/openai/codex/issues/43764)) | [docs](https://learn.chatgpt.com/docs/windows/windows-sandbox), [OpenAI post 2026-05-13](https://openai.com/index/building-codex-windows-sandbox/) |
| Cursor | No native. "On Windows, we run our Linux sandbox inside WSL2" | Seatbelt, Landlock v3 (kernel 6.2+) | User approves step-outside. Enterprise admins enforce allowlists | [blog 2026-02-18](https://cursor.com/blog/agent-sandboxing), [docs](https://cursor.com/docs/agent/run-modes) list only macOS and Linux requirements |
| Copilot CLI | Yes, public preview since 2026-06-02 | MXC (AppContainer on Windows). GitHub says it "does not run your commands inside a separate virtual machine or container" | Not documented | [GitHub changelog](https://github.blog/changelog/2026-06-02-cloud-and-local-sandboxes-for-github-copilot-now-in-public-preview/) |
| Windsurf / Devin Desktop | No | Command allow/deny lists only. Dedicated agent terminal is macOS zsh only | n/a | [docs](https://docs.windsurf.com/windsurf/terminal) |
| Docker Sandboxes | Yes | microVM | Host-side policy | [docs](https://docs.docker.com/ai/sandboxes/) |

Is any tool positioned as "the safe way for your agent to run npm install on Windows"?

- Searches run: "npm install AI agent safe Windows sandbox AppContainer", "safe way for agent to run npm install Windows", Hacker News for nvx and for npm-install sandboxes, plus the tool-by-tool checks above.
- Result: none. The only Windows-native results are generic agent sandboxes (Codex, MXC, sandbox-runtime alpha, landstrip) and the microVM route (Docker sbx, Windows Sandbox). Sourced evidence that the gap is felt: Claude Code issue #46740 calls the sandbox "the single most impactful security setting" and notes it is unavailable on native Windows. OpenAI's own post says Windows users previously had to "approve nearly every command" or enable Full Access.
- Sourced fact that narrows the claim. Codex and Copilot CLI do sandbox on native Windows. So the uncontested claim is not "only sandbox on Windows". It is: the only tool that sandboxes `npm install`, `npx`, `pnpm`, `yarn` and `bun` on native Windows regardless of which agent (or human) typed the command, with no WSL2, no VM, and an egress allowlist that `-y` cannot widen. That claim holds for Claude Code users and Cursor users on Windows today, and for any team that does not want to depend on each agent vendor's sandbox.

---

## 5. Threats to the thesis

Each steelman is followed by what nvx still does, or a concession.

1. npm 12 blocks lifecycle scripts by default (2026-07-08).
   Steelman. The primary payload path in Nx, Shai-Hulud, ChainDrop and Miasma was a lifecycle script or an implicit node-gyp build. npm 12 blocks both without any new tool. A developer who never approves scripts has removed most install-time risk for free.
   What nvx still does. Contains the scripts that are approved (esbuild, sharp, playwright, native modules all need them and will be on every team's `allowScripts`). Contains `npx` and `bunx`, which npm 12 does not touch and which run arbitrary code by design. Scrubs env and gates egress, so an approved-but-compromised script cannot exfiltrate tokens. Applies the same policy across npm, pnpm, yarn and bun.
   Concession. For a developer with no approved scripts and no `npx` use, nvx's install-time containment adds little over npm 12. The pitch should say so.

2. pnpm 11 cooldowns (1440 minutes default) and exotic-dep blocking.
   Steelman. pnpm, aube and (opt-in) npm, Yarn and Bun all have release-age gates now. nvx's 24-hour `min_age_hours` is parity, not advantage.
   What nvx still does. Applies the gate across managers that leave it off by default (npm, Yarn, Bun) and keeps it out of reach of `-y`.
   Concession. Release-age is not a differentiator and should not be in the headline.

3. mise sandboxing maturing.
   Steelman. mise has 34k stars, a monthly cadence, and already ships Seatbelt/Landlock sandboxing plus npm installs through aube. If jdx adds a Windows backend, mise covers nvx's whole feature list with a hundred times the audience.
   What nvx still does. Windows today, on by default rather than per-invocation flags, and a policy model an agent cannot edit around. mise's own docs say Windows runs unsandboxed with a warning, and users report abort traps on macOS.
   Concession. This is the largest single-project threat. A native Windows sandbox in mise would remove nvx's clearest claim. Nothing found suggests it is scheduled. Watch discussion #8878.

4. aube.
   Steelman. Default-deny scripts, default cooldown, trust-downgrade detection, exotic-dep block, env scrubbing and a kernel jail on macOS/Linux, planned on by default. It replaces the package manager rather than wrapping it, so it sees every install.
   What nvx still does. Kernel containment on Windows (aube's Windows jail is env scrub plus HOME redirect). Contains `npx`. Works with the package manager the team already uses.
   Concession. On macOS and Linux with `jailBuilds: true`, aube and nvx are close on install scripts, and aube's trust-policy check is something nvx does not have.

5. OS vendors shipping sandboxing natively.
   Steelman. Microsoft's MXC (MIT, AppContainer, standalone CLI, Windows 11 24H2+) is exactly the primitive nvx built by hand, and OpenAI, NVIDIA and GitHub are integrating it. Anthropic's sandbox-runtime has a Windows alpha. Docker ships microVMs on Windows for free.
   What nvx still does. Transparent shim on the commands developers already type, package-aware checks, version management, and the policy file. MXC is "early preview with known overly-permissive policies" per its own README.
   Concession. If Claude Code ships native Windows sandboxing on sandbox-runtime, or Cursor adopts MXC, the "your agent has no sandbox on Windows" argument disappears for those users. nvx should treat MXC as a candidate backend, not only a competitor.

6. Detection is better elsewhere.
   Steelman. Socket, Aikido and StepSecurity have human-reviewed malware feeds and funded research teams. nvx's OSV lookup and typosquat heuristic will miss what they catch.
   What nvx still does. Contains what detection misses. The README already says detection is best-effort.
   Concession. Do not lead with detection. Consider making Socket Firewall or safe-chain a documented layer to run alongside nvx.

---

## 6. Positioning recommendation

Who to sell to first. Teams and individuals running Claude Code or Cursor on native Windows without WSL2. That is the population with an agent that runs `npm install` and has no sandbox at all today (Claude Code docs, Cursor blog). Second, Windows-first shops that standardise on one version manager and want the same containment whether a human or an agent typed the command. Do not start with macOS users (reads are not contained there and nono, Claude Code and mise already serve them) or with enterprises (no fleet install, no SOC 2, and the buyers there are already talking to Socket and StepSecurity).

The single claim to lead with. "npm install, npx and bun run inside a kernel sandbox on native Windows, with an egress allowlist the agent cannot widen." Every word is verifiable against the enforcement matrix. It names the platform where no competitor is positioned, the commands no package manager contains, and the approval property no agent vendor matches.

What to stop claiming. "Supply-chain safety" as a category differentiator, because npm 12, pnpm 11, Yarn 4.14 and aube block scripts and cool down releases by default. Typosquat and OSV checks as headline features, because Socket, Aikido and safe-chain do detection better and free. "Sandboxing" without the Windows qualifier, because mise, nono, Claude Code and Cursor all sandbox on macOS and Linux.

Which competitors to name. mise, because it is the tool the Volta maintainers and jdx's audience are moving to, and its docs state that Windows runs unsandboxed. aube, because it is the strictest package manager and its Windows jail is env-scrub only. Claude Code and Cursor, because their own docs say WSL2 only on Windows. Which to ignore. nvm, fnm, n, nvs, nodenv, asdf, proto (no security story to argue against), Volta (dead), Socket and Snyk (different category, do not pick that fight), Docker Sandboxes (stronger isolation, different workflow, complement not competitor).

Three things that would most change adoption in 90 days.

1. Distribution. 47 downloads and no listing on winget, Scoop, Homebrew or npm is the whole story of section 3. Publish to winget and Scoop first (Windows is the wedge), then Homebrew. Sign releases with Sigstore or an Authenticode cert. nvm-windows 2.0 shipped unsigned and got called out for it within days.
2. A public, reproducible demonstration on Windows. Run a harmless Shai-Hulud-style payload (postinstall that reads `~/.npmrc` and POSTs to an unlisted host) under Claude Code on native Windows with and without nvx, publish the transcript and the enforcement matrix, and post it as a Show HN with the Windows claim in the title. aube reached 2,000 stars on a jdx post. nvx has zero HN presence.
3. Meet the agents where they are. Ship a documented Claude Code setup for native Windows (hooks or PATH shim) and a Codex and Copilot CLI page. Evaluate MXC as an alternative Windows backend so that Microsoft's primitive becomes leverage rather than a threat. Publish a `nvx policy check` GitHub Action so the CI exit codes have a place to run.

---

## Sources

Fetched or searched on 2026-09-17 unless dated otherwise.

- GitHub API repo and release endpoints for nvm-sh/nvm, coreybutler/nvm-windows, Schniz/fnm, volta-cli/volta, asdf-vm/asdf, jdx/mise, tj/n, jasongin/nvs, nodenv/nodenv, moonrepo/proto, fstubner/nvx, anthropics/sandbox-runtime, jdx/aube, landstrip/landstrip, nolabs-ai/nono, berstend/node-safe
- formulae.brew.sh/api/formula/{fnm,mise,nvm,volta,asdf,node,aube}.json
- nodejs.org/metrics/summaries/os.csv (file ends 2023-07-12)
- hn.algolia.com search API for fnm, mise, aube, nvx
- https://docs.npmjs.com/cli/v12/using-npm/changelog/
- https://github.blog/changelog/2026-06-09-upcoming-breaking-changes-for-npm-v12/
- https://pnpm.io/blog/releases/11.0
- https://github.com/yarnpkg/berry/issues/6258 and Yarn 4.14.0 release notes
- https://bun.com/docs/pm/lifecycle, https://github.com/oven-sh/bun/issues/30525, https://github.com/oven-sh/bun/issues/30750
- https://aube.sh/security
- https://mise.jdx.dev/sandboxing.html, https://github.com/jdx/mise/discussions/8878
- https://github.com/anthropics/sandbox-runtime
- https://github.com/microsoft/mxc, https://blogs.windows.com/windowsdeveloper/2026/06/02/windows-platform-security-for-ai-agents/
- https://code.claude.com/docs/en/sandboxing, https://github.com/anthropics/claude-code/issues/46740
- https://learn.chatgpt.com/docs/windows/windows-sandbox, https://openai.com/index/building-codex-windows-sandbox/, https://github.com/openai/codex/issues/40158, https://github.com/openai/codex/issues/43764
- https://cursor.com/blog/agent-sandboxing, https://cursor.com/docs/agent/run-modes, https://cursor.com/changelog/2-5
- https://github.blog/changelog/2026-06-02-cloud-and-local-sandboxes-for-github-copilot-now-in-public-preview/, https://docs.github.com/en/copilot/concepts/about-cloud-and-local-sandboxes
- https://docs.windsurf.com/windsurf/terminal
- https://docs.docker.com/ai/sandboxes/, https://www.docker.com/blog/docker-sandboxes-run-claude-code-and-other-coding-agents-unsupervised-but-safely/, https://forums.docker.com/t/docker-sandbox-pricing-unclear/151790
- https://learn.microsoft.com/en-us/windows/security/application-security/application-isolation/windows-sandbox/
- https://github.com/AikidoSec/safe-chain, https://www.aikido.dev/protect/device-protection, https://www.aikido.dev/pricing
- https://docs.socket.dev/docs/socket-firewall-free, https://socket.dev/pricing
- https://www.stepsecurity.io/pricing
- https://www.veracode.com/products/veracode-package-firewall/, https://docs.phylum.io/
- https://www.endorlabs.com/pricing, https://semgrep.dev/pricing/, https://docs.github.com/en/billing/concepts/product-billing/github-advanced-security
- https://www.chainguard.dev/pricing, https://edu.chainguard.dev/chainguard/libraries/quickstart/
- https://www.gitguardian.com/developer-endpoint-protection
- https://1password.com/developer-security
- https://docs.nvm-windows.com/features/newv2/, https://github.com/nvm-windows/nvm/releases/tag/v2.0.0
- https://moonrepo.dev/blog/proto-v0.61, https://moonrepo.dev/docs/proto/lockfile
- https://github.com/volta-cli/volta/issues/2080
- https://www.slashdata.co/post/global-developer-population-trends-2025-how-many-developers-are-there
- https://survey.stackoverflow.co/2025/, https://stackoverflow.blog/2026/06/23/the-2026-developer-survey-is-now-open-for-human-developers-only/
- https://www.infoq.com/news/2026/03/state-of-js-survey-2025, https://2025.stateofjs.com/en-US/other-tools/
- https://www.infoworld.com/article/4021972/javascript-macos-lead-usage-in-worldwide-developer-survey.html
- https://github.blog/news-insights/octoverse/octoverse-a-new-developer-joins-github-every-second-as-ai-leads-typescript-to-1/
- https://codeless.co/node-js-statistics/ (secondary source for the version-manager survey figure), https://nodejs.org/en/user-survey-report/
- https://www.endorlabs.com/learn/new-research-malware-in-open-source-ecosystems-surges-14x-as-attackers-hijack-trusted-packages
- https://www.sonatype.com/state-of-the-software-supply-chain/2026/open-source-malware
- https://www.reversinglabs.com/blog/sscs-report-2026-takeaways
- https://snyk.io/blog/snyk-state-of-open-source-security-2023/
- User-count sources: https://codex.danielvaughan.com/2026/04/09/codex-3-million-users-growth-usage-limits/, https://www.gradually.ai/en/codex-statistics/, https://www.getpanto.ai/blog/cursor-ai-statistics, https://www.getpanto.ai/blog/github-copilot-statistics, https://en.wikipedia.org/wiki/Cursor_(company), https://shipper.now/windsurf-stats/
