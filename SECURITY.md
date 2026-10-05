# Security Policy

## Supported versions

nvx is pre-1.0 software. Security fixes are applied to the latest tagged
release and the `main` branch. Older tags do not receive backports.

| Version        | Supported          |
| -------------- | ------------------ |
| latest release | :white_check_mark: |
| `main`         | :white_check_mark: |
| older tags     | :x:                |

## Reporting a vulnerability

**Please do not open a public issue for security vulnerabilities.**

Report privately via GitHub Security Advisories:
[**Report a vulnerability**](https://github.com/fstubner/nvx/security/advisories/new).

Please include:

- the nvx version (`nvx version`) and your OS/arch,
- a description of the issue and its impact,
- reproduction steps or a proof of concept,
- any relevant policy files (`.nvx-policy.json`) or `~/.nvx/audit.log` excerpts.

`audit.log` records the working directory of each entry, and the hostnames of
egress decisions. Of a command it records the name, plus its subcommand when
that word is one nvx recognises — `install`, `run`, `publish` and similar. It
records no other argument: not package names, not script paths, not your
project's own script names, not flag values. Anything nvx does not recognise is
dropped rather than guessed at.

Read an excerpt before sending it and redact what you would rather not share: a
working directory or a hostname can name a client or an unannounced product.

We aim to acknowledge reports within 5 business days and to provide a
remediation timeline after triage. Coordinated disclosure is appreciated;
we will credit reporters who wish to be named once a fix ships.

## Threat model

nvx is designed to make the *default* developer workflow safer against
**supply-chain attacks in the JavaScript/runtime ecosystem** — malicious or
compromised packages executed via `npm`/`npx`/`yarn`/`pnpm`/`bunx` install
and run scripts. Its defenses are layered:

1. **Runtime integrity** — runtime downloads (e.g. Node.js) are verified
   against the publisher's `SHASUMS256.txt` over HTTPS before use. Archive
   extraction is protected against zip-bombs and path/symlink traversal.
2. **Supply-chain checks** — typosquatting detection, OSV vulnerability
   lookups, package release-age warnings, install-script prompts and the
   `blocked_packages` list run before untrusted code executes.

   What they run on depends on the command. For `npm install`, `npm update`
   and `npm dedupe`, nvx first runs npm's own resolver with
   `--package-lock-only --ignore-scripts`, contained like the install, on a
   scratch copy of `package.json` and the lockfile. Every package in the tree
   it writes is checked, dependencies included, apart from those already
   installed at the same version, which npm leaves alone. For `npm ci`, `npm rebuild` and
   an `npm install` whose lockfile matches `package.json`, every lockfile entry
   for this platform is checked. A lockfile entry's `resolved` URL and `integrity` hash
   must match the registry's record for its name and version, and an entry that
   does not is refused.

   The typosquat check is the one exception to "every package". A typosquat is
   a name someone typed wrongly, so it runs only on the names you chose: the
   packages named on the command line, or with none named, the dependencies in
   your `package.json` (all four dependency fields). A dependency that came in
   with one of those was named by its author, and skips the typosquat check
   and the download lookup behind it. The blocklist, release-age, install-script
   and advisory checks still cover the whole tree.

   Everywhere else the checks run on the packages the command names, or on the
   project's `package-lock.json`, or on the versions `package.json` declares.
   That covers `npx`, `npm exec`, `npm create`, `npm init <initializer>`, every
   pnpm, yarn and bun command, and npm projects that use workspaces or depend
   on a local folder. The dependencies those packages bring in are not checked
   there, and pnpm, yarn and bun lockfiles are not read. A package from git, a
   URL or a local path is checked against `blocked_packages` by the name it
   installs under, and skips the other checks.

   nvx makes the lookups itself, outside the sandbox. These are the hosts it
   contacts for them.

   - The registry npm will fetch each package from, read from `.npmrc` and
     `npm_config_*` as npm reads them. For a contained install that is the
     project's `.npmrc` only, because the contained npm reads nothing else.
   - `api.npmjs.org` for weekly download counts, when a public-registry
     package you chose has a name close to a popular one. Each name is looked
     up once per run.
   - `api.osv.dev` for advisories on public-registry packages.
   - `cdn.jsdelivr.net` for the popular-package list, once the cached copy is
     7 days old.

   A package from any registry other than `registry.npmjs.org` is
   never sent to `api.npmjs.org` or `api.osv.dev`, and skips the typosquat and
   advisory checks. The run says so in one line and the audit log records
   `check_skipped`. When `.npmrc` holds an `_authToken` for a package's
   registry, nvx sends it with that metadata request and nowhere else. It is
   not put in the sandbox's environment or written to any log.
3. **Process isolation** — commands that fetch or execute package-authored code
   run inside an OS-native sandbox (Windows AppContainer, Linux Landlock +
   network namespace + seccomp, macOS Seatbelt) with a scrubbed environment and
   filesystem writes confined to the working directory and an ephemeral guest
   home (on macOS also `/dev` and the system temp directories).

   That is installs (`install`, `ci`, `add`, `update`, `rebuild`, `dedupe`,
   `audit fix`) and ad-hoc tool runners (`npx`, `bunx`, `npm exec`, `pnpm dlx`,
   `bun x`, `npm create`, `npm init <initializer>`). It is **not** your own code:
   `npm run build`, `npm test` and a bare `node app.js` run uncontained at the
   default `standard` level, by design — `isolation.level: strict` extends
   containment to those too. This entry said "shimmed commands" without the
   distinction until 0.5.6, which was less careful than README on the same point.
4. **Egress control** — outbound network access is mediated by a loopback
   allowlist proxy; unknown hosts are denied or prompted (fail-closed when
   non-interactive). When nvx's own environment sets `HTTPS_PROXY` or
   `HTTP_PROXY`, an allowed connection is forwarded through that proxy, with
   `NO_PROXY` and loopback destinations dialled directly. The allowlist decides
   before anything is forwarded. The upstream proxy resolves the name itself,
   so nvx's link-local check covers only what nvx's own resolver returned.

**Design stance:** security-relevant failures **fail closed**. If a sandbox
primitive is unavailable or a policy cannot be parsed, nvx refuses to run the
command rather than running it unprotected.

## Known limitations

These are deliberate, documented trade-offs — not undisclosed weaknesses:

- **Same-origin checksums.** Runtime archives and their `SHASUMS256.txt` are
  fetched from the same publisher over HTTPS. This detects corruption and
  tampering in transit but is not an independent second-channel signature
  (e.g. GPG). Independent signature verification is on the roadmap. With
  `NVX_NODE_MIRROR` (or `NVM_NODEJS_ORG_MIRROR`, `FNM_NODE_DIST_MIRROR`) set,
  the archives and checksums both come from that mirror, so the mirror is
  trusted exactly as nodejs.org is.
- **Network enforcement is weakest on macOS.** On Linux a loopback-only network
  namespace plus seccomp genuinely block raw sockets and non-proxied DNS; on
  Windows the AppContainer holds no network capability, so the OS refuses direct
  connections and DNS does not resolve. On both, the egress proxy runs outside the
  containment and is reached over a UNIX socket.
- **On macOS, enforcement is real but narrower than on Windows and Linux.** The
  profile is `(deny default)` and permits outbound traffic to localhost only, so a
  raw socket to an external host is refused by the kernel rather than merely
  discouraged. A hosted macOS runner confirms this on every CI build
  (`scripts/sandbox-enforcement-macos.sh`): a contained process is denied a write
  outside its project, is denied egress with an empty allowlist, and is still
  permitted to write its own project — the last of those being what distinguishes
  enforcement from a sandbox that has simply failed to start.

  What macOS does not do is contain reads outside the credential stores. See the
  entry below. Since 2026-08-24 a
  macOS runner also confirms that an allowlisted host completes through the proxy,
  that UDP is refused, and that nvx fails closed without `sandbox-exec`. One cell
  stays unclaimed: which layer refuses the outbound connection the probe observes
  being refused, DNS or connect.

  This entry has been wrong in both directions. Until 2026-08-20 it said macOS
  egress was cooperative and a raw socket could bypass the allowlist, which
  understated the design and contradicted README's matrix — two shipped documents
  disagreeing on a security question is its own defect. Until 2026-08-23 it then
  said none of it had been verified on macOS hardware, which was true when written
  and outlived the probe that made it false.
- **On macOS, loopback access is now scoped to the mode** (fixed 2026-08-20).
  `proxy` reaches nvx's egress proxy and nothing else on 127.0.0.1; `offline`
  reaches nothing; `loopback` reaches all of it, which is what that mode is for.

  Previously every restricted mode granted `localhost:*`, so contained code could
  reach a local database, daemon port or another project's dev server with no
  allowlist entry, and `offline` was not offline. Any reachable service that
  forwards traffic would have made the allowlist meaningless. This was present
  from the sandbox's first implementation. The fix is to the generated profile.
  What a macOS runner confirms is that egress is denied with an empty allowlist,
  which does not by itself prove the per-mode loopback scoping — nothing stands up
  a loopback listener on macOS and checks which modes reach it.
- **On Windows, two sandboxes in the SAME project can reach each other's
  loopback listeners.** Windows permits loopback within an AppContainer package,
  and nvx gives each project one package. Two concurrent runs of the same project
  therefore share a package and can connect to each other; runs in different
  projects cannot, and neither can anything on the host. That is the boundary
  this draws: same project means same dependencies and same policy, so it is one
  trust domain rather than two.

  Until 2026-08-29 there was one package for the whole machine, which made this
  far wider: **any** contained process could reach a listener inside **any**
  other nvx sandbox, across unrelated projects. That defeated the egress
  allowlist — a sandbox with a permissive allowlist relays for one without —
  and joined two projects the per-project filesystem identity keeps apart.
  Measured with both controls: the host could not reach a sandbox's listener and
  a sandbox could not reach the host's, while sandbox-to-sandbox succeeded.
  `scripts/sandbox-enforcement-windows.ps1` now asserts the cross-project
  refusal, with the same-project connection as its positive control.

- **A local service is reachable from a sandbox when the policy allowlists it.**
  `"allow_hosts": ["localhost:5432"]` means what it says on every platform where
  the proxy mediates egress: the proxy runs outside the containment and dials on
  the contained process's behalf, so a permitted destination is permitted whether
  or not it is loopback. This is intended — a project that talks to a local
  database or registry needs it. What it costs is that anything reachable on
  loopback which *forwards* traffic (a debugging proxy, `ssh -D`, a dev-server
  proxy route) makes egress arbitrary, so allowlist a local port with the same
  care as a remote one.

  nvx will not grant loopback through the unknown-host prompt, only through the
  policy file or `--connect`. The prompt is raised by whatever the sandbox is
  running — the untrusted code — and localhost is where the services that take no
  credentials listen, so a postinstall must not be able to ask for the developer's
  database. Approving any other host at that prompt lasts for the current run and
  is no longer recorded.

- **On Windows, a loopback exemption left by a pre-0.5.0 `nvx setup` opens every
  service on 127.0.0.1** to contained code, whatever the allowlist says — local
  databases, daemon ports, other dev servers. 0.5.0 never registers one and
  removes it during `nvx setup`, but that command needs an Administrator terminal
  and is otherwise no longer required, so on an upgraded machine the exemption
  simply persists. nvx cannot remove it without elevation; it warns on every
  affected launch and `nvx doctor` reports it, both printing the removal command.
  While it is registered, treat the egress allowlist as unenforced: only *direct*
  connections to other hosts stay blocked, and any reachable loopback service that
  forwards traffic (a debugging proxy, `ssh -D`, a dev-server proxy route) makes
  egress arbitrary.
- **Windows egress was not restricted at all before 0.5.0.** Earlier versions
  granted the sandbox the `internetClient` capability *and removed the proxy
  environment variables*, so a contained process connected directly and the
  allowlist was never consulted — not even cooperatively. Measured on 2026-08-18
  against 0.4.0: a postinstall script reached `1.1.1.1:443` and
  `registry.npmjs.org:443` directly. If you are running 0.4.0 or earlier on
  Windows, treat egress as unrestricted; filesystem containment, environment
  scrubbing and the pre-install checks were unaffected. See
  `docs/enforcement-matrix.md`.
- **A `.env` inside the project is readable by a contained install.** The project
  directory has to be readable for an install to work, and `.env` lives in it.
  Environment *variables* are scrubbed; a file is a file. Secrets outside the
  project — `~/.ssh`, `~/.aws`, `~/.npmrc` — stay unreachable on Windows and
  Linux. On macOS the Seatbelt profile allows filesystem reads and denies the
  credential stores by path (see `docs/enforcement-matrix.md` note 2). Those
  three, the other registry and cloud credential files listed there, and the
  keychains cannot be read. **Other files outside the project can**, other
  projects included.
- **Your home directory's names are visible on Windows, contents are not.**
  A contained process can list your profile directory — enough to learn which
  credential stores exist — because it carries an ACE Windows ships for all
  AppContainers, which nvx cannot revoke.

  `C:\` and `C:\Users` are **not** listable by default; they carry no such ACE.
  They become listable where an elevated `nvx setup` has run, which grants them
  so that tools walking up to a drive root work. `nvx setup --undo` removes what
  nvx added. Measured 2026-08-30 in a real container, with an uncontained control
  of the same script:

  ```
                         contained        uncontained
  LIST[C:\]              DENIED:EPERM     OK, 40 entries
  LIST[C:\Users]         DENIED:EPERM     OK, 14 entries
  LIST[C:\Users\you]   OK, 203 entries  OK, 203 entries
  ```

  This entry used to name all three as always-visible, crediting the shipped ACE
  for all of them. README and `docs/enforcement-matrix.md` were corrected and this
  file was missed — a partial sweep, which is how the same wrong sentence survives
  in one place after being fixed in two.
- **On Windows, a profile folder that lost its inheritance protection is open
  to other accounts.** Windows ships `C:\Users` and each profile folder
  protected from the drive root's "Authenticated Users: Modify". Older nvx
  builds switched that protection off on every folder they granted, and nvx now
  keeps it as it found it. `nvx doctor` reports a profile, or the folder above
  it, that has lost its protection, with the `icacls ... /inheritance:r`
  command that restores it.
- **A contained command started in your home directory cannot write it.** On
  Linux and macOS, a command started in your home directory or above it starts
  in the sandbox's home instead, so it cannot write `~/.nvx` or your shell
  profile. On Windows the same applies to a directory above your profile or
  inside `~/.nvx`. nvx says so when it happens.
- **`audit.log` is a record, not evidence against a local attacker.** Anything
  running as you can append to it, and that includes code nvx deliberately does
  not contain: at the default `standard` level your own code — `npm run build`,
  `node script.js` — runs uncontained, so it can write a fabricated
  `"mode":"sandboxed"` entry that `nvx audit` then displays as a genuine
  contained run. Measured; a contained process is refused (`EPERM`), an
  uncontained one is not.

  This is inherent rather than an oversight: the file has to be writable by nvx
  running as you, so it is writable by anything else running as you. Read it as
  what nvx recorded about its own runs, not as proof of what did or did not
  happen on a machine where untrusted code has already executed outside the
  sandbox.
- **Projects granted by nvx before 0.5.0 keep a dead permission.** Every sandbox
  shared one identity until 0.5.0 and those permissions were never revoked. No
  current sandbox holds that identity, so the permission grants nothing. nvx
  removes it the next time it runs in that project, and `nvx doctor` reports it.
  The manual command is under "Limitations in detail" below.
- **Capturing a child's output on Windows needs help from nvx, and gets it.** An
  AppContainer may not create a named pipe, which is how Windows implements piped
  child stdio, so a contained program that captures a subprocess's output would
  hang. Both kinds of capture are handled, and so is writing to a child's stdin.
  What a contained process still cannot do is give a child an IPC channel
  (`child_process.fork`).

  This bullet used to be headed "cannot capture a child's output", and four lines
  later said "**Synchronous capture is handled; streaming capture is not**" —
  while the paragraph after that explained, correctly, that streaming is handled.
  A reader skimming the bold text got the false half of a document that
  contradicted itself twice on one page.

  **Synchronous capture goes through files.** The restriction is
  on creating a pipe, not on file descriptors, so a preload loaded into every
  contained node process redirects `spawnSync`/`execSync`/`execFileSync` through
  temp files in the guest home. Their contract is "run it, give me the output at
  the end", which a file satisfies exactly — the caller never sees a stream either
  way. `npm install esbuild` works as a result; it previously hung forever.

  **Streaming capture goes through pipes nvx creates.** Asynchronous
  `spawn(..., { stdio: "pipe" })` is a real stream that a file cannot
  stand in for, and it is handled differently: nvx creates the pipes outside the
  container and the preload only opens them. Opening an existing pipe is a
  different access check from creating one, and it is permitted when the pipe's
  DACL names both the user nvx runs as and that container's package identity.
  Both endpoints are inside the same sandbox; nvx moves bytes between two of its
  own children, which it already parents. No capability is granted to make this
  work.

  **Another process running as the same user can open these pipes.** That is
  unavoidable rather than an oversight: a contained process's token carries the
  user's identity, so the ACE that admits the sandbox necessarily admits the
  user. Anything already running as you can read the project and the audit log
  regardless, so the pipes sit inside that existing boundary rather than outside
  it — but a second local *account* cannot reach them. This entry claimed the
  stronger "openable by one sandbox and no other" until an acceptance review
  opened one from an ordinary process; the code had in fact granted Everyone,
  which is now the user's SID.

  A contained child's stdin is carried the same way, so `child.stdin` is a real
  stream. The pool streams 8 piped children at once, counted across every node
  process in the session; beyond that, output is buffered to a file in the guest
  home and delivered when the stream ends rather than as it is produced —
  available from `stdout` events or `close`, not from an `exit` handler.

  **A child given an IPC channel, as `child_process.fork` does, is refused.**
  That is a second named pipe, created by libuv inside the contained process,
  and node builds the parent half of it itself, so nvx cannot hand it over
  ready-made. It throws at once and names `--no-sandbox`. Vitest's default
  worker pool forks, so `nvx --no-sandbox npx vitest run` is the way to run it.

  nvx's diagnostic hint covers installs only, on purpose: an install still running
  after two minutes is anomalous, while an `npx`-launched dev server running for
  hours is working correctly, so a timer cannot tell the second case from a hang.
  Nothing here affects containment: it changes how a contained process talks to
  its own children, not what it may reach.
- **The Docker provider has no egress allowlist.** Under the `docker` isolation
  provider, `offline` and `loopback` both run with `--network none`. An
  allowlist there would be cooperative only, so nvx refuses to run a command in
  `proxy` mode under that provider.
- **Docker passes allowed environment values on the command line.** `docker run`
  takes them as `-e KEY=VALUE`, so anything `isolation.environment.allow` lets
  through is visible in the process list to other processes running as you for as
  long as the container is starting. nvx keeps those values out of its own output
  and out of `nvx report`, and that is all it can do: the argument list belongs to
  `docker`. The native providers on Windows, Linux and macOS pass the environment
  directly to the child and are unaffected. If a token matters more than the
  container does, use the native provider for it.
- **nvx is not a malware scanner.** The supply-chain checks reduce risk from
  common attack patterns; they do not guarantee detection of a determined,
  novel attacker. Treat nvx as defense-in-depth, not a guarantee.

## Limitations in detail

Moved here from README, where this depth sat on the front page. Each entry
states what an attacker gains and what still holds; the probe output and the
timing behind these claims is in `docs/enforcement-matrix.md`.

- **A stray `package.json` in a parent directory puts every project beneath it in
  one sandbox scope.** nvx decides which project a sandbox belongs to by walking
  up from the working directory to the nearest `package.json`. Projects that
  resolve to the same root share one identity, so a contained install in either
  can read and write the other. Normally every project has its own manifest and
  they stay separate — what breaks it is a manifest somewhere above them.

  A home directory is the easy way to acquire one, from an `npm install` run in
  the wrong folder. Measured 2026-09-01: an `npm install` in `C:\Users\you`
  left a `package.json` there, and every project beneath it — including nvx's own
  test fixtures under `%TEMP%` — collapsed into a single scope. The containment
  probes caught it as a cross-project read, which is how it was found; deleting
  the file restored per-project isolation immediately.

  `nvx doctor` reports it when the manifest sits in your home directory or at a
  volume root — the cases that collapse many unrelated projects at once. A
  manifest in an ordinary ancestor is a monorepo and is left alone, so if
  contained commands start behaving as though two projects are one and doctor is
  quiet, look for a `package.json` above them.

- **A contained process can see directory NAMES outside the project, though not
  their contents.** On Windows it can list **your home directory** — enough to
  learn that `.ssh`, `.aws` or `.1password` exist. File contents in those places
  stay unreadable.

  That one is Windows, not nvx: your profile directory carries an ACE for ALL
  APPLICATION PACKAGES that Windows ships and nvx cannot revoke (deny rules were
  measured not to override it).

  `C:\` and `C:\Users` are a separate matter, and this entry used to lump them in
  with the home directory as though the same ACE covered them. It does not — they
  carry no ALL APPLICATION PACKAGES entry. They are listable only where an
  elevated `nvx setup` has granted them, which it does so that tools walking up to
  a drive root can work. Measured 2026-08-30 in a real container:

  ```
  LIST[C:\]            DENIED:EPERM     (OK where setup's grant applies)
  LIST[C:\Users]       DENIED:EPERM     (OK where setup's grant applies)
  LIST[C:\Users\you] OK, 203 entries  (always — the shipped ACE)
  ```

  `nvx setup --undo` removes the grants nvx added; the shipped ACE on your profile
  stays either way.

- **A contained command run outside any project may start in the sandbox home.**
  A directory with no `package.json` above it becomes the command's writable
  root, and granting the sandbox access to a large one — `%TEMP%`, a home
  folder, the parent of all your projects — is an ACL write over everything
  beneath it: minutes, on every launch, before anything ran. nvx now gives that
  grant 1.5 seconds; if it does not finish, the command starts in the sandbox
  home instead and prints one line saying so, and the directory is not retried
  for a month. Files the command writes to its working directory then land in
  the sandbox home and are removed with it. `npx -y <tool>` never cares; a
  command that must write the directory it was started from should be run from
  a project, where the grant is always waited for.

- **A loopback exemption left by a pre-0.5.0 `nvx setup` lets contained code reach
  every service on 127.0.0.1.** Local databases, daemon ports, another project's
  dev server — none of them need an `allow_hosts` entry while it is registered.
  Windows normally refuses an AppContainer's loopback connections, which is what
  the 0.5.0 egress design depends on; the older setup registered an exemption
  because the proxy then ran on the host's loopback. 0.5.0 never adds one and
  removes it during `nvx setup`, but that needs an Administrator terminal and is
  otherwise no longer required, so on an upgraded machine it persists.

  **Treat the egress allowlist as unenforced while it is registered.** Only
  *direct* connections to other hosts stay blocked. Any reachable loopback
  service that forwards traffic — a debugging proxy like mitmproxy or Charles, an
  `ssh -D` dynamic forward, a dev server's proxy route — turns this into
  arbitrary egress: measured on 2026-08-19 by completing a TLS exchange with an
  external host from inside a sandbox, through a CONNECT proxy on 127.0.0.1.
  nvx warns on every affected launch and `nvx doctor` reports it; removing it is
  one elevated command, which both of them print.

- **Projects granted by nvx before 0.5.0 keep a dead permission until nvx runs in
  them again.** Up to 0.5.0 every sandbox shared one identity and the permissions
  nvx granted were never revoked, so any project you had used nvx in was readable
  and writable from any sandbox.

  **That is no longer exploitable.** Sandboxes now run under a per-project
  AppContainer package, so nothing holds the shared identity those old permissions
  name. Measured 2026-08-31 by recreating it exactly — the old identity granted
  modify access on a directory, then a contained process from an unrelated project
  run against it: `EPERM` on both write and list.

  What remains is litter. nvx removes it the first time it runs in that project,
  but it keeps no list of where it has been, so a project you do not revisit keeps
  the entry. To clean one by hand:
  `icacls <project> /remove:g *S-1-15-2-...` for each such entry `icacls <project>`
  lists.

- **A contained process can list the names in your home directory, though not
  read anything in it.** Measured 2026-09-05: a contained process enumerated 208
  entries in `%USERPROFILE%`, while `~/.npmrc`, `~/.ssh` and `~/.aws/credentials`
  were all refused with EPERM.

  So credentials stay unreadable — that part of the claim above holds — but which
  tools you use is visible: the presence of `.ssh`, `.aws`, `.1password` and the
  rest. That is reconnaissance value, not access.

  nvx grants ancestors traverse-only precisely to avoid this, and that is not
  enough: Windows puts `ALL APPLICATION PACKAGES:(RX)` on the profile directory by
  default, and every AppContainer inherits it regardless of what nvx does. Fixing
  it would mean an explicit deny ACE on a directory nvx does not own, which is not
  obviously the right trade and has not been made.

- **A contained command sees almost none of your environment.** Containment keeps
  11 environment variables on Windows (7 elsewhere) and drops the rest, so that a
  package's install script cannot read the secrets sitting in the shell that
  launched it. Measured on Windows: 107 variables outside a contained run, 48
  inside.

  Most of what goes is operating-system furniture nothing reads. Some of it is
  not: a tool that checks `CI` to suppress interactive prompts starts prompting,
  and a build reading `NODE_ENV=production` quietly emits a development bundle.
  Nothing errors, which is what makes it confusing. nvx now says so when a
  variable of that kind is removed. The set it names is deliberately short, so
  most contained runs stay silent; `NVX_DEBUG=1` records the complete list.

  Name the ones a project genuinely needs:

  ```json
  { "isolation": { "environment": { "allow": ["CI", "NODE_ENV"] } } }
  ```

  Exact names, matched without regard to case; no patterns. A name that looks
  like a credential (a sensitive prefix such as `AWS_` or `GITHUB_`, or a word
  such as TOKEN, SECRET or PASSWORD, as in `GH_TOKEN`) is refused and
  reported rather than honoured — a policy file lives in the repository, and a
  single line in one must not be able to hand a cloud credential to whatever an
  install script runs. Adding an entry widens what contained code can see, so a
  project file that does it needs the same approval as an egress allowlist entry.

- **A contained tool cannot reach a program kept outside the project.** The
  sandbox grants your project, a throwaway home, and nvx's own runtimes — nothing
  else. A tool that keeps its executables somewhere else cannot run them.

  Playwright is the case that surfaced it: its browsers live in
  `%LOCALAPPDATA%\ms-playwright` (`~/.cache/ms-playwright` elsewhere), and a
  contained process could not even list that directory. Name it and it works:

  ```json
  { "isolation": { "filesystem": {
      "allow_read_exec": ["%LOCALAPPDATA%/ms-playwright"] } } }
  ```

  Read and execute only — never write, whatever else the policy says. Paths take
  `~`, `$VAR` and `%VAR%` so one policy file works across machines, and a path
  that does not exist here is skipped with a warning rather than failing the run.
  Adding one widens what contained code may execute, so a project file that does
  it needs the same approval as an egress allowlist entry. On Windows the grant is
  scoped to that project's sandbox identity, not shared with every sandbox on the
  machine.

  **On Windows the grant is a real filesystem permission, and nvx takes it back
  when the policy stops asking.** It has to persist between runs — re-applying it
  every launch would put a permissions call on the startup path for every root —
  so it is recorded, and reconciled against the policy each time you run something
  contained in that project. Remove the `allow_read_exec` entry, or the policy
  file, and the next contained run withdraws the permission. `nvx grants list`
  shows what is currently granted and `nvx grants reset` withdraws it immediately.

  Three cases are not automatic. `nvx grants reset --all` — which sweeps every
  project — clears the first; the other two it can only report, because in both
  it no longer knows which permission it would be removing.

  The identity is derived from the project root, so *moving* that root leaves the
  permission granted under the old identity unreconciled. The root is the nearest
  ancestor holding a `package.json`, so this needs a `package.json` to appear or
  disappear closer to your working directory than the current one; adding one
  further up changes nothing. The stale permission cannot be *used* while stale —
  a run at the old root reconciles it before the contained process starts — but it
  stays on disk until such a run happens or you reset.

  The second is a grant record nvx cannot read. It keeps the file, renamed to
  `.unreadable`, and says so, but it can no longer tell what that record listed, so
  those permissions are removed with `icacls` by hand. Records are written
  atomically, so this should take deliberate corruption to reach.

  The third is a granted directory that is **renamed**. The permission is attached
  to the directory, so it travels with it, while nvx's record still names the old
  path. nvx cannot follow it and does not pretend to: it reports that the
  directory is gone and that the permission moved with it if it was renamed rather
  than deleted, and leaves you to remove it at the new location with `icacls`.
  Moving a granted directory is worth avoiding for that reason.

  A fourth case needs nothing cleaned up but is worth knowing about: if a
  directory nvx granted read/execute is later used as a working directory by the
  same project, nvx's own writable-root grant replaces that permission with a
  wider one. Dropping the policy entry then leaves the wider permission in place —
  nvx says so rather than removing it, because taking it away would remove access
  granted for a different reason. The sandbox keeps that access until the project
  stops using the directory.

  Nothing else needs cleaning up by hand. Earlier builds of this feature left the
  permission behind entirely, with no way back but working out the capability SID
  and running `icacls` yourself.

  On Linux this grants reading, listing and executing. An earlier note here said
  listing was refused and called it a Landlock limitation; it was a wrong constant
  in nvx, since fixed.

- **On Windows, nvx stops a command once the program that started it has exited,
  and reports exit 129.** It checks every 15 seconds and needs two consecutive observations,
  so this lands 15–30 seconds after the parent goes away. It only applies when
  nvx's input is a pipe — the shape a long-lived stdio server is launched with.

  This exists because sandboxed MCP servers outlived their clients and
  accumulated until a machine froze: 18 nvx processes, 43 Node processes and
  3.9 GB, measured 2026-08-27. An ordinary shell pipeline is unaffected, because
  there the shell that built it is still running.

  **What this can catch by surprise:** a command deliberately detached with a
  pipe still attached to its input — for example Node's
  `spawn(cmd, {detached: true, stdio: 'pipe'})` where the launcher then exits.
  Detaching via `start /b` is unaffected, because that leaves the input a console
  and the check never arms. If you need a long-running job to outlive its
  launcher, give it a console or a file for stdin rather than a pipe.

- **`nvx audit` shows what nvx recorded, which anything running as you can add to.**
  A contained process cannot write `~/.nvx/audit.log`, but uncontained code can —
  and at the default `standard` level your own code is uncontained, so
  `npm run build` could append a fabricated "sandboxed" entry that `nvx audit`
  then displays as real. The file has to be writable by nvx running as you, so it
  is writable by anything else running as you. Useful for reviewing your own
  usage; not proof against someone who already runs code as you.

- **On Windows, a server started inside the sandbox needs `--expose` to be
  reachable from the host.** Windows refuses connections into an AppContainer, so
  a contained `npx vite` binds its port, prints that it is listening, and serves
  nobody. `--expose` publishes it:

  ```
  nvx --expose 5173:8080 npx vite
  ```

  The two numbers cannot be the same, and that is not a style choice: an
  AppContainer shares the host's network stack rather than getting its own, so one
  port number cannot hold both the contained server and the host listener —
  measured, with the contained server losing the race and dying on `EADDRINUSE`.
  Give the port your server uses inside, then the port you want to visit. With the
  second omitted (`--expose 5173`) nvx picks a free one and prints the URL.

  Nothing is relaxed to make this work: the contained side dials *outward* over a
  UNIX socket and the parent splices inbound requests onto it, so no network
  capability is granted and egress stays exactly as restricted. That is asserted
  on every run of the probe, not merely intended.

  Your own `npm run dev` is uncontained at the default `standard` level and needs
  none of this. Until 0.5.6 there was no way to reach a contained server at all;
  the FAQ had claimed loopback worked for dev servers, which was measured false on
  2026-08-20.

- **A freshly published package stops an MCP server starting, for up to 24 hours.**
  nvx holds a cooling-off window on new npm releases: a version published in the
  last day is flagged before it installs, because supply-chain compromises are
  usually caught inside that window. The flag is a prompt — and a server your
  editor spawns has no one to answer it, so it is denied and the launch aborts.

  Your client reports `-32000: Connection closed`, which is what it reports for
  any server that dies before answering. nvx now replies to the client's first
  request with the real reason instead of leaving the pipe silent, so the message
  you see names the cooling-off window and the package rather than nothing.

  This affects any MCP server installed from npm on a floating version
  (`npx -y <pkg>` fetches the latest), and it is self-inflicted if you publish the
  package yourself: publish in the morning and the server is unavailable until the
  next day. Three ways out, narrowest first:

  ```jsonc
  // 1. exempt just this package, in ~/.nvx/policy.json
  { "release_age": { "trusted_packages": ["your-pkg", "@your-scope/*"] } }
  ```

  ```jsonc
  // 2. pin to a version you have already used
  { "command": "npx", "args": ["-y", "your-pkg@1.2.3"] }
  ```

  ```jsonc
  // 3. approve every nvx check for this server, not only this one
  { "command": "npx", "args": ["-y", "your-pkg"], "env": { "NVX_YES": "true" } }
  ```

  The first keeps the cooling-off window for everything else, which
  `release_age.min_age_hours` does not — that widens the window for every package
  you install. The third is the broadest: `NVX_YES` also approves the typosquat,
  install-script and known-advisory checks for that server, so it is the last
  resort. Each check it approves is printed on stderr and written to
  `~/.nvx/audit.log` as a `check_approved` record.

  Until 0.6.0 the only exemption list was `typosquatting.trusted_packages`, which
  waived typosquat detection at the same time. It no longer waives the
  release-age window; a file still using it that way is told to move the entry.

- **On Windows and macOS, a contained tool needs `--connect` to reach a service
  running on your machine.** The other direction, and the same reason: the
  sandbox has no route to your loopback, and the egress proxy refuses a host
  loopback destination unless `allow_hosts` names it. A contained tool that has
  to talk to something you are already running — a browser with remote
  debugging on, a local database, a device emulator — gets there one named
  port at a time:

  ```
  nvx --connect 9222:19222 npx @playwright/mcp --cdp-endpoint http://127.0.0.1:19222
  ```

  The in-sandbox port cannot also be 9222, for the network-stack reason above, so
  pick a second number — as above — whenever the command line has to name it.
  Your shell expands the command line before nvx runs, so a variable is no use
  there.

  With the second number omitted (`--connect 9222`) nvx chooses a free port,
  prints it, and sets `NVX_CONNECT_9222` in the sandbox. That form is for tools
  that read their endpoint from the environment or a config file at startup, not
  from argv.

  Nothing general is opened. nvx runs the listener inside, dials `127.0.0.1:9222`
  itself from outside, and closes both when the command exits — the sandbox never
  gets to choose a destination. This is deliberately not the machine-wide loopback
  exemption that 0.5.0 removed, which opened every local service to every sandbox
  on the machine, permanently, and could not be revoked without elevation.

  **The boundary is the project.** Windows permits loopback *within* an
  AppContainer package, and every run of one project shares that project's
  package. Until 2026-08-29 every nvx sandbox on the machine shared one package,
  and on 2026-08-28 a sandbox in an unrelated project, with no grant of its own,
  read the service. Packages are per project now, so another project's sandbox
  cannot reach the in-sandbox listener. nvx also identifies the process behind
  each tunnel connection and refuses any it cannot place inside this run, and
  logs the refusal. Treat concurrent runs of one project as one trust domain all
  the same, and do not rely on `--connect` to keep them apart.

  **macOS reaches the same place by a different route.** What stops a contained
  tool there is the Seatbelt profile, which in the default `proxy` mode permits
  outbound connections to the egress proxy's own ports and nothing else. So the
  profile could simply name your service's port and be done. nvx runs the relay
  anyway, and the profile opens only the relay's port: the command, the two-number
  rule and `NVX_CONNECT_<port>` then mean the same thing on both platforms, and
  the sandbox reaches a pipe whose far end nvx chose rather than an address it
  could have guessed.

  The peer check above is Windows-only, and is not missing on macOS. There, a
  process outside any sandbox can already open your service directly, so the relay
  hands it nothing; another sandbox cannot reach the relay's port, because its own
  profile permits only its own proxy ports.

  **Linux tunnels it across the namespace.** Its sandbox has a network namespace
  of its own, so 127.0.0.1 in there is a different 127.0.0.1 from yours. The
  supervisor listens on the in-sandbox port and forwards over a UNIX socket in the
  guest home, which crosses because it is a filesystem object; nvx dials your
  service from outside, as everywhere else.

  `offline` cannot carry it on Linux. It denies the contained process every IP
  socket, including the one it would use to reach the tunnel. nvx says so and
  names the modes that work, so the flag is never accepted in silence. The
  default, `proxy`, carries it, and so does `loopback`.

  In a policy file it is `isolation.network.connect_ports`, and adding one counts
  as loosening, so a project cannot grant itself a host port without approval.

- **`network.mode: loopback` reaches services on 127.0.0.1 — by three different
  routes, and Windows does not cover the same traffic as the other two.** On macOS
  the Seatbelt profile grants loopback directly. On Linux every loopback TCP
  connection is redirected to a relay inside the namespace, which asks the kernel
  what the connection was for and carries it out, so a raw connection to a local
  database arrives at the same address it named. On Windows the reach comes from
  nvx's proxy permitting loopback destinations, which covers what a proxy-aware
  client sends — HTTP and HTTPS — and leaves a raw socket with nowhere to go.

  A Linux host whose kernel will not take the redirect rules falls back to the
  Windows behaviour and says so, rather than failing the run: what is lost is
  reach, not containment.

  The default `proxy` mode reaches loopback only where `allow_hosts` names it, and
  `offline` reaches nothing. Until 2026-09-08 the mode did nothing at all on
  Windows and Linux: each treated it as `offline`, so a mode whose name says
  "reach these services" reached none of them. Docker still does. It runs the
  mode with `--network none`, the same as `offline`.

  A server the **sandbox itself** runs stays reachable from inside it. The relay
  tries the sandbox's own namespace before the host, so a contained `npm run dev`
  on 127.0.0.1:3000 and a contained test client still find each other.

  Until 2026-08-20 that was not true: every restricted mode granted all of
  loopback, so a contained install could reach your database or another project's
  dev server with no `allow_hosts` entry, and `offline` was not offline. If any
  reachable loopback service forwards traffic, the allowlist is bypassable
  entirely, which is what made it serious. Fixed, and pinned by a test. The fix is
  to the *generated profile*; what a macOS runner now confirms is that egress is
  denied with an empty allowlist, which does not by itself prove the per-mode
  loopback scoping (see the entry above).

## Scope

In scope: sandbox escapes, policy-bypass or policy-tampering vectors,
egress-allowlist bypasses, checksum-verification bypasses, and privilege
escalation caused by nvx.

Out of scope: vulnerabilities in Node.js or other managed runtimes
themselves, in npm packages, or in the operating system's sandbox primitives.
