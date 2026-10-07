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

Report privately via GitHub Security Advisories, with the
[**Report a vulnerability**](https://github.com/fstubner/nvx/security/advisories/new)
form.

Please include:

- the nvx version (`nvx version`) and your OS/arch,
- a description of the issue and its impact,
- reproduction steps or a proof of concept,
- any relevant policy files (`.nvx-policy.json`) or `~/.nvx/audit.log` excerpts.

`audit.log` records the working directory of each entry, and the hostnames of
egress decisions. Of a command it records the name. It also records the
subcommand when that word is one nvx recognises, such as `install`, `run` or
`publish`. It records no other argument. Package names, script paths, your
project's own script names and flag values are all left out. nvx drops anything
it does not recognise and never guesses at it.

Read an excerpt before sending it and redact what you would rather not share. A
working directory or a hostname can name a client or an unannounced product.

We aim to acknowledge reports within 5 business days and to provide a
remediation timeline after triage. Coordinated disclosure is appreciated. We
will credit reporters who wish to be named once a fix ships.

## Threat model

nvx is designed to make the *default* developer workflow safer against
**supply-chain attacks in the JavaScript/runtime ecosystem**. These are
malicious or compromised packages executed via `npm`/`npx`/`yarn`/`pnpm`/`bunx`
install and run scripts. Its defenses are layered:

1. **Runtime integrity.** Runtime downloads (e.g. Node.js) are verified
   against the publisher's `SHASUMS256.txt` over HTTPS before use. Archive
   extraction is protected against zip-bombs and path/symlink traversal.
2. **Supply-chain checks.** Typosquatting detection, OSV vulnerability
   lookups, package release-age warnings, install-script prompts and the
   `blocked_packages` list run before untrusted code executes.

   A package OSV lists as malicious, with an advisory ID starting `MAL-`, is
   refused without a prompt. `-y`, `--agent-mode`, `NVX_YES` and
   `NVX_TRUST_YES` do not let it through, and neither does
   `vulnerabilities.min_severity`. Only a `vulnerabilities.allowed_advisories`
   entry naming that advisory does. A version whose publish time the registry
   does not give is asked about like one inside the release-age window.

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

   A `pnpm install`, `yarn`, `yarn install` or `bun install` that names no
   package, and their `rebuild` and `dedupe` commands, check every entry for this
   platform in the package manager's own lockfile: `pnpm-lock.yaml`
   (lockfileVersion 5.x, 6.x and 9.x, written by pnpm 7 to 12), `yarn.lock`
   (Yarn 1, and Yarn 2 and later) or `bun.lock`. pnpm and Bun record each
   entry's `integrity` hash and Yarn 1 its `resolved` URL and hash, and those
   must match the registry's record, as a `package-lock.json` entry's must.
   Yarn 2 and later record a checksum of Yarn's own archive, which nvx cannot
   compare with the registry's hash. Yarn fetches those entries from the
   registry by name and version, and an entry that names its own
   `__archiveUrl` must name the registry's tarball. Yarn 1 records no `os` or
   `cpu`, so its entries are checked on every platform. A dependency that
   `package.json` or a lockfile entry declares and the lockfile has no entry
   for is resolved afresh by the package manager: nvx checks it as declared,
   and the run says so. The packages it brings in are not checked. Yarn 1
   leaves another platform's optional dependencies out of its lockfile, so a
   missing optional dependency is not checked for Yarn 1. A lockfile that is
   there and cannot be read is asked about the way an unreadable
   `package-lock.json` is: refused when nobody can answer, and with approval
   the checks run on what `package.json` declares.

   The typosquat check is the one exception to "every package". A typosquat is
   a name someone typed wrongly, so it runs only on the names you chose. Those
   are the packages named on the command line or, with none named, the
   dependencies in your `package.json` (all four dependency fields) and in
   those of its workspace members. A dependency that came in
   with one of those was named by its author, and skips the typosquat check
   and the download lookup behind it. The blocklist, release-age, install-script
   and advisory checks still cover the whole tree.

   Everywhere else the checks run on the packages the command names, or on the
   project's `package-lock.json`, or on the versions `package.json` declares.
   That covers `npx`, `npm exec`, `npm create`, `npm init <initializer>`,
   pnpm, yarn and bun commands that name packages (`pnpm add left-pad`) or
   update them, pnpm, yarn and bun projects with no lockfile or only Bun's
   binary `bun.lockb`, an install run from inside a workspace member's folder,
   and npm projects that use workspaces or depend on a local folder. The
   dependencies those packages bring in are not checked there. A package from
   git, a URL or a local path is checked against `blocked_packages` by the
   name it installs under, and skips the other checks.

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
3. **Process isolation.** Commands that fetch or execute package-authored code
   run inside an OS-native sandbox. The sandbox is Windows AppContainer, Linux
   Landlock + network namespace + seccomp, or macOS Seatbelt. It has a scrubbed
   environment. It can write only to the working directory and a guest home
   under `~/.nvx`, which holds its temp directory. The guest home is thrown away
   after each run, except for pnpm and for tools approved as trusted, which keep
   one per project. On macOS it may also write a
   few named device files such as `/dev/null` and `/dev/tty`.

   That is installs (`install`, `ci`, `add`, `update`, `rebuild`, `dedupe`,
   `audit fix`) and ad-hoc tool runners (`npx`, `bunx`, `npm exec`, `pnpm dlx`,
   `bun x`, `npm create`, `npm init <initializer>`). It is **not** your own code.
   nvx reads the command as the package manager does. npm accepts any
   unambiguous prefix of a command, and camelCase, so `npm exe` is `npm exec`
   and `npm installTest` is `npm install-test`. nvx applies npm's rule as the
   releases bundled with Node.js 18 to 26, and npm 12, apply it. An npm command
   nvx does not recognise is contained. pnpm's
   `uni`, `dislink` and `recursive <command>`, yarn's
   `workspace <name> <command>` and bun's `r` and `ci` are read as well.
   `npm run build`, `npm test` and a bare `node app.js` run uncontained at the
   default `standard` level, by design. `isolation.level: strict` extends
   containment to those too. This entry said "shimmed commands" without the
   distinction until 0.5.6, which was less careful than README on the same point.
4. **Egress control.** A loopback allowlist proxy mediates outbound network
   access. Unknown hosts are denied or prompted (fail-closed when
   non-interactive). When nvx's own environment sets `HTTPS_PROXY` or
   `HTTP_PROXY`, an allowed connection is forwarded through that proxy. The
   egress proxy dials `NO_PROXY` and loopback destinations directly. The allowlist decides
   before anything is forwarded. The proxy may be `http://`, `https://`,
   `socks5://` or `socks5h://`. nvx verifies an `https://` proxy's certificate
   against the system roots before it sends any credential. Any other scheme is
   ignored with a warning, and connections are then made directly. An
   `http://`, `https://` or `socks5h://` proxy resolves the name itself, so
   nvx's link-local check covers only what nvx's own resolver returned. A
   `socks5://` proxy is sent the addresses nvx resolved and checked.

**Design stance.** Security-relevant failures **fail closed**. If a sandbox
primitive is unavailable or a policy cannot be parsed, nvx refuses to run the
command. It never runs it unprotected.

## Known limitations

These are deliberate trade-offs, and this section documents each one:

- **Same-origin checksums.** Runtime archives and their `SHASUMS256.txt` are
  fetched from the same publisher over HTTPS. This detects corruption and
  tampering in transit but is not an independent second-channel signature
  (e.g. GPG). Independent signature verification is on the roadmap. With
  `NVX_NODE_MIRROR` (or `NVM_NODEJS_ORG_MIRROR`, `FNM_NODE_DIST_MIRROR`) set,
  the archives and checksums both come from that mirror, so the mirror is
  trusted exactly as nodejs.org is.
- **Network enforcement is weakest on macOS.** On Linux a loopback-only network
  namespace and seccomp genuinely block raw sockets and non-proxied DNS. On
  Windows the AppContainer holds no network capability, so the OS refuses direct
  connections and DNS does not resolve. On both, the egress proxy runs outside the
  containment and is reached over a UNIX socket.
- **On macOS, enforcement is real but narrower than on Windows and Linux.** The
  profile is `(deny default)` and permits outbound traffic to localhost only. The
  kernel refuses a raw socket to an external host outright. A hosted macOS runner
  confirms this on every CI build (`scripts/sandbox-enforcement-macos.sh`). A
  contained process is denied a write outside its project and is denied egress
  with an empty allowlist. It is still permitted to write its own project. That
  last check is what distinguishes enforcement from a sandbox that has simply
  failed to start.

  On macOS reads are denied under the home directory and nvx's home, apart from
  what a run needs, and allowed elsewhere on the disk. See the entry below. A
  macOS runner also confirms that an allowlisted host completes
  through the proxy, that UDP is refused, and that nvx fails closed without
  `sandbox-exec`. It shows a contained lookup refused at the system resolver,
  and a TCP connect to an address refused by the kernel, so each layer refuses
  on its own.

  On macOS a contained process cannot reach the system resolver in any network
  mode but `open`. getaddrinfo's socket was already refused. Until 2026-10-06
  Network.framework's way in, the Mach service `com.apple.dnssd.service`, was
  not, and a contained program could send data out encoded in the names it
  looked up. Until the same date nvx's egress proxy looked up the name a
  contained client asked for before the allowlist refused it, on every
  platform. It now looks a name up only after the allowlist, an earlier grant
  or a yes at the prompt has allowed it, so a refused name never reaches the
  host's resolver.

  This entry has been wrong in both directions. Until 2026-08-20 it said macOS
  egress was cooperative and a raw socket could bypass the allowlist. That
  understated the design and contradicted README's matrix. Two shipped documents
  disagreeing on a security question is its own defect. Until 2026-08-23 it then
  said none of it had been verified on macOS hardware. That was true when written
  and outlived the probe that made it false.
- **Since 0.5.2, loopback access on macOS is scoped to the mode.** On
  127.0.0.1, `proxy` reaches nvx's egress proxy and nothing else. `offline`
  reaches nothing. `loopback` reaches all of it, which is what that mode is for.

  Previously every restricted mode granted `localhost:*`. Contained code could
  reach a local database, daemon port or another project's dev server with no
  allowlist entry, and `offline` was not offline. Any reachable service that
  forwards traffic would have made the allowlist meaningless. This was present
  from the sandbox's first implementation. The fix is to the generated profile.

  A macOS runner confirms that egress is denied with an empty allowlist. That
  does not by itself prove the per-mode loopback scoping. Nothing stands up a
  loopback listener on macOS and checks which modes reach it.
- **On Windows, two sandboxes in the SAME project can reach each other's
  loopback listeners.** Windows permits loopback within an AppContainer package,
  and nvx gives each project one package. Two concurrent runs of the same project
  therefore share a package and can connect to each other. Runs in different
  projects cannot, and neither can anything on the host. That is the boundary
  this draws. The same project means the same dependencies and the same policy,
  so its runs form one trust domain.

  Before 0.6.0 there was one package for the whole machine, which made this far
  wider. **Any** contained process could reach a listener inside **any** other
  nvx sandbox, across unrelated projects. That defeated the egress allowlist,
  because a sandbox with a permissive allowlist relays for one without. It also
  joined two projects the per-project filesystem identity keeps apart.

  The measurement used both controls. The host could not reach a sandbox's
  listener and a sandbox could not reach the host's, while sandbox-to-sandbox
  succeeded.
  `scripts/sandbox-enforcement-windows.ps1` now asserts the cross-project
  refusal, with the same-project connection as its positive control.

- **A local service is reachable from a sandbox when the policy allowlists it.**
  `"allow_hosts": ["localhost:5432"]` means what it says on every platform where
  the proxy mediates egress. The proxy runs outside the containment and dials on
  the contained process's behalf. A permitted destination is permitted whether
  or not it is loopback. This is intended, because a project that talks to a
  local database or registry needs it. The cost is that anything reachable on
  loopback which *forwards* traffic (a debugging proxy, `ssh -D`, a dev-server
  proxy route) makes egress arbitrary. Allowlist a local port with the same care
  as a remote one.

  nvx will not grant loopback through the unknown-host prompt, only through the
  policy file or `--connect`. The prompt is raised by whatever the sandbox is
  running, which is the untrusted code. Localhost is where the services that take
  no credentials listen. So a postinstall must not be able to ask for the
  developer's database. Approving any other host at that prompt lasts for the current run and
  is no longer recorded.

- **On Windows, a loopback exemption left by a pre-0.5.0 `nvx setup` opens every
  service on 127.0.0.1** to contained code, whatever the allowlist says. Treat
  the egress allowlist as unenforced while it is registered. nvx warns on every
  affected launch, and `nvx doctor` prints the elevated command that removes it.
  "Limitations in detail" below has the rest.
- **Windows egress was not restricted at all before 0.5.0.** Earlier versions
  granted the sandbox the `internetClient` capability *and removed the proxy
  environment variables*. A contained process connected directly and the
  allowlist was never consulted, not even cooperatively. Measured against 0.4.0,
  a postinstall script reached `1.1.1.1:443` and `registry.npmjs.org:443`
  directly. If you are running 0.4.0 or earlier on Windows, treat egress as
  unrestricted. Filesystem containment, environment scrubbing and the
  pre-install checks were unaffected. See
  `docs/enforcement-matrix.md`.
- **On Windows, nvx changes the permissions of the project's `.env` files.**
  The project directory has to be readable for an install to work, and `.env`
  lives in it. A deny entry on the file and a medium integrity label on it were
  both measured, and a contained process read it either way. So at each
  contained launch nvx gives each `.env` and `.env.*` file a permission list
  that does not inherit from the project folder and has no entry for a
  sandboxed process. Every other entry is kept, so you read and edit the file
  as before. `nvx grants reset` puts the earlier permissions back. A file an
  editor or `git checkout` replaces is readable to a contained process that is
  already running, until the next launch changes it again. A file nvx may not
  change stays readable, with a warning. On every platform a contained process
  cannot read the project's `.env` or `.env.*` files, except the templates
  `.env.example`, `.env.sample`, `.env.template` and `.env.dist`. On Linux and
  Windows that covers the files present when the run starts.
  `docs/enforcement-matrix.md` note 15 has the details. Secrets outside the
  project, such as `~/.ssh`, `~/.aws` and `~/.npmrc`, stay unreachable on Windows
  and Linux. On macOS the Seatbelt profile denies reads under the home
  directory and nvx's home, apart from the project, the guest home, nvx's
  runtimes and `allow_read_exec` roots. It denies the credential stores by path
  on top of that (see `docs/enforcement-matrix.md` note 2). Other projects in the
  home cannot be read. **Files outside the home can**, such as other apps' temp
  files under `/private/var/folders`.
- **Your home directory's names are visible on Windows, contents are not.**
  A contained process can list your profile directory, which shows which
  credential stores exist. The entry that allows it ships with Windows, and nvx
  cannot revoke it. `C:\` and `C:\Users` are not listable from a contained
  process. An older `nvx setup` granted them, but launches no longer carry the
  identity it granted to, and `nvx setup` removes the entries. "Limitations in
  detail" below has the measurements.
- **On Windows, a profile folder that lost its inheritance protection is open
  to other accounts.** Windows ships `C:\Users` and each profile folder
  protected from the drive root's `Authenticated Users: Modify`. Older nvx
  builds switched that protection off on every folder they granted, and nvx now
  keeps it as it found it. `nvx doctor` reports a profile, or the folder above
  it, that has lost its protection. `nvx setup`, from an Administrator
  terminal, restores it. It does so only where the folder's own entries keep
  SYSTEM and Administrators (and, for your profile, you) in, so nobody is
  locked out. Otherwise it says why and leaves the folder alone.
- **A contained command started in your home directory cannot write it.** On
  Linux and macOS, a command started in your home directory or above it starts
  in the sandbox's home instead. It cannot write `~/.nvx` or your shell profile. On Windows the same applies to a directory above your profile or
  inside `~/.nvx`. nvx says so when it happens.
- **`audit.log` is a record, not evidence against a local attacker.** Anything
  running as you can append to it, and that includes code nvx deliberately does
  not contain. At the default `standard` level your own code (`npm run build`,
  `node script.js`) runs uncontained. So it can write a fabricated
  `"mode":"sandboxed"` entry that `nvx audit` then displays as a genuine
  contained run. A test confirmed this. A contained process is refused
  (`EPERM`), and an uncontained one is not.

  This is inherent. The file has to be writable by nvx running as you, so it is
  writable by anything else running as you. Read it as what nvx recorded about
  its own runs. It is no proof of what did or did not happen on a machine where
  untrusted code has already executed outside the sandbox.
- **Projects granted by nvx before 0.5.0 keep a dead permission.** Every sandbox
  shared one identity until 0.5.0 and those permissions were never revoked. No
  current sandbox holds that identity, so the permission grants nothing. nvx
  removes it the next time it runs in that project, and `nvx doctor` reports it.
  The manual command is under "Limitations in detail" below.
- **Capturing a child's output on Windows needs help from nvx, and gets it.** An
  AppContainer may not create a named pipe, which is how Windows implements piped
  child stdio. A contained program that captures a subprocess's output would
  therefore hang. Both kinds of capture are handled, and so is writing to a child's stdin.
  What a contained process still cannot do is give a child an IPC channel
  (`child_process.fork`).

  This bullet used to be headed "cannot capture a child's output". Four lines
  later it said "**Synchronous capture is handled; streaming capture is not**",
  while the paragraph after that explained, correctly, that streaming is handled.
  A reader skimming the bold text got the false half of a document that
  contradicted itself twice on one page.

  **Synchronous capture goes through files.** The restriction is
  on creating a pipe, not on file descriptors. So a preload loaded into every
  contained node process redirects `spawnSync`/`execSync`/`execFileSync` through
  temp files in the guest home.

  Their contract is "run it, give me the output at the end", which a file
  satisfies exactly. The caller never sees a stream either
  way. `npm install esbuild` works as a result. It previously hung forever.

  **Streaming capture goes through pipes nvx creates.** Asynchronous
  `spawn(..., { stdio: "pipe" })` is a real stream that a file cannot
  stand in for, so it is handled differently. nvx creates the pipes outside the
  container and the preload only opens them. Opening an existing pipe is a
  different access check from creating one. It is permitted when the pipe's
  DACL names both the user nvx runs as and that container's package identity.

  Both endpoints are inside the same sandbox. nvx moves bytes between two of its
  own children, which it already parents. No capability is granted to make this
  work.

  **Another process running as the same user can open these pipes.** That is
  unavoidable. A contained process's token carries the user's identity, so the
  ACE that admits the sandbox necessarily admits the user. Anything already
  running as you can read the project and the audit log regardless, so the pipes
  sit inside that existing boundary. A second local *account* cannot reach them.

  This entry claimed the stronger "openable by one sandbox and no other" until an
  acceptance review opened one from an ordinary process. The code had in fact
  granted Everyone, which is now the user's SID.

  A contained child's stdin is carried the same way, so `child.stdin` is a real
  stream. The pool streams 8 piped children at once, counted across every node
  process in the session. Beyond that, output is buffered to a file in the guest
  home and delivered when the stream ends, not as it is produced. It is available
  from `stdout` events or `close`, not from an `exit` handler.

  **A child given an IPC channel, as `child_process.fork` does, is refused.**
  That is a second named pipe, created by libuv inside the contained process.
  Node builds the parent half of it itself, so nvx cannot hand it over
  ready-made. It throws at once and names `--no-sandbox`. Vitest's default
  worker pool forks, so `nvx --no-sandbox npx vitest run` is the way to run it.

  nvx's diagnostic hint covers installs only, on purpose. An install still
  running after two minutes is anomalous. An `npx`-launched dev server running
  for hours is working correctly, so a timer cannot tell the second case from a
  hang. Nothing here affects containment. It changes how a contained process
  talks to its own children, not what it may reach.
- **The Docker provider has no egress allowlist.** Under the `docker` isolation
  provider, `offline` and `loopback` both run with `--network none`. An
  allowlist there would be cooperative only, so nvx refuses to run a command in
  `proxy` mode under that provider.
- **Docker passes allowed environment values on the command line.** `docker run`
  takes them as `-e KEY=VALUE`. So anything `isolation.environment.allow` lets
  through is visible in the process list to other processes running as you while
  the container is starting. nvx keeps those values out of its own output and out
  of `nvx report`, and that is all it can do. The argument list belongs to
  `docker`. The native providers on Windows, Linux and macOS pass the environment
  directly to the child and are unaffected. If a token matters more than the
  container does, use the native provider for it.
- **nvx is not a malware scanner.** The supply-chain checks reduce risk from
  common attack patterns. They do not guarantee detection of a determined,
  novel attacker. Treat nvx as defense-in-depth, not a guarantee.

## Limitations in detail

Each entry states what an attacker gains and what still holds. The probe output
and the timing behind these claims are in `docs/enforcement-matrix.md`.

- **A stray `package.json` in a parent directory puts every project beneath it in
  one sandbox scope.** nvx decides which project a sandbox belongs to by walking
  up from the working directory to the nearest `package.json`. Projects that
  resolve to the same root share one identity, so a contained install in either
  can read and write the other. Normally every project has its own manifest and
  they stay separate. A manifest somewhere above them breaks that.

  A home directory is the easy way to acquire one, from an `npm install` run in
  the wrong folder. In one measured case, an `npm install` in `C:\Users\you`
  left a `package.json` there. Every project beneath it, including nvx's own
  test fixtures under `%TEMP%`, collapsed into a single scope. The containment
  probes caught it as a cross-project read, which is how it was found. Deleting
  the file restored per-project isolation immediately.

  `nvx doctor` reports it when the manifest sits in your home directory or at a
  volume root. Those are the cases that collapse many unrelated projects at once.
  A manifest in an ordinary ancestor is a monorepo and is left alone. If
  contained commands start behaving as though two projects are one and doctor is
  quiet, look for a `package.json` above them.

- **A contained process can list the names in your home directory, though not
  read anything in it.** On Windows that is enough to learn that `.ssh`, `.aws`
  or `.1password` exist. Measured on 2026-09-05, a contained process enumerated
  208 entries in `%USERPROFILE%`. `~/.npmrc`, `~/.ssh` and `~/.aws/credentials`
  were all refused with EPERM. That is reconnaissance value, not access.

  The listing comes from Windows, not nvx. Windows puts
  `ALL APPLICATION PACKAGES:(RX)` on the profile directory by default, and every
  AppContainer inherits it. nvx grants the folders above a project traverse
  only, so it adds nothing here. Deny ACEs are no fix nvx can rely on. On
  2026-08-18 a deny ACE on a file in the project, `.env`, did not keep a
  contained process out, for the container's SID or for ALL APPLICATION
  PACKAGES. A deny on the profile itself has not been tried. It would mean
  changing a folder nvx does not own.

  `C:\` and `C:\Users` are a separate matter. They carry no ALL APPLICATION
  PACKAGES entry, and they are listable only where an older elevated `nvx setup`
  granted them. Current versions grant nothing there, because the walk-up
  preload answers the stats those grants were for. Measured 2026-08-30 in a
  real container, with an uncontained control of the same script:

  ```
                         contained        uncontained
  LIST[C:\]              DENIED:EPERM     OK, 40 entries
  LIST[C:\Users]         DENIED:EPERM     OK, 14 entries
  LIST[C:\Users\you]   OK, 203 entries  OK, 203 entries
  ```

  The first two read OK while an older setup's grant applied. Launches no longer
  carry the identity it granted to, so a grant left on a machine admits nothing.
  `nvx setup` removes the grants nvx added, from an Administrator terminal. The shipped entry
  on your profile stays either way.

  This entry used to name all three as always visible, crediting the shipped
  ACE for all of them. README and `docs/enforcement-matrix.md` were corrected
  and this file was missed. That was a partial sweep, which is how the same
  wrong sentence survives in one place after being fixed in two.

- **A contained command run outside any project may start in the sandbox home.**
  A directory with no `package.json` above it becomes the command's writable
  root. A large one, such as `%TEMP%`, a home folder or the parent of all your
  projects, needs an ACL write over everything beneath it. That
  takes minutes, on every launch, before anything runs. nvx gives that grant 1.5
  seconds. If it does not finish, the command starts in the sandbox home instead
  and prints one line saying so. The directory is then not retried for a month.
  Files the command writes to its working directory then land in the sandbox
  home and are removed with it. `npx -y <tool>` never cares. Run a command that
  must write the directory it was started from in a project, where nvx always
  waits for the grant.

- **A loopback exemption left by a pre-0.5.0 `nvx setup` opens every service on
  127.0.0.1 to contained code.** Local databases, daemon ports and another
  project's dev server need no `allow_hosts` entry while it is registered.
  Windows normally refuses an AppContainer's loopback connections, which is what
  the 0.5.0 egress design depends on. The older setup registered an exemption
  because the proxy then ran on the host's loopback. 0.5.0 never adds one and
  removes it during `nvx setup`. That needs an Administrator terminal and is
  otherwise no longer required, so on an upgraded machine it persists.

  **Treat the egress allowlist as unenforced while it is registered.** Only
  *direct* connections to other hosts stay blocked. Any reachable loopback
  service that forwards traffic turns this into arbitrary egress. That includes a
  debugging proxy like mitmproxy or Charles, an `ssh -D` dynamic forward and a dev
  server's proxy route.

  A test confirmed this through a CONNECT proxy on 127.0.0.1. It completed a TLS
  exchange with an external host from inside a sandbox.
  nvx warns on every affected launch and `nvx doctor` reports it. Removing it is
  one elevated command, which both of them print.

- **Projects granted by nvx before 0.5.0 keep a dead permission until nvx runs in
  them again.** Up to 0.5.0 every sandbox shared one identity and the permissions
  nvx granted were never revoked. Any project you had used nvx in was readable
  and writable from any sandbox.

  **That is no longer exploitable.** Sandboxes now run under a per-project
  AppContainer package, so nothing holds the shared identity those old permissions
  name. A test recreated it exactly. It granted the old identity modify access on
  a directory, then ran a contained process from an unrelated project against it.
  Both write and list returned `EPERM`.

  What remains is litter. nvx removes it the first time it runs in that project.
  It keeps no list of where it has been, so a project you do not revisit keeps
  the entry. To clean one by hand, run
  `icacls <project> /remove:g *S-1-15-2-...` for each such entry `icacls <project>`
  lists.

- **A contained command sees almost none of your environment.** Containment keeps
  11 environment variables on Windows (7 elsewhere) and drops the rest. A
  package's install script then cannot read the secrets sitting in the shell
  that launched it. Measured on Windows, there were 107 variables outside a
  contained run and 48 inside.

  Most of what goes is operating-system furniture nothing reads. Some of it is
  not. A tool that checks `CI` to suppress interactive prompts starts prompting,
  and a build reading `NODE_ENV=production` quietly emits a development bundle.
  Nothing errors, which is what makes it confusing.

  nvx now says so when a variable of that kind is removed. The set it names is deliberately short, so
  most contained runs stay silent. `NVX_DEBUG=1` records the complete list.

  Name the ones a project genuinely needs:

  ```json
  { "isolation": { "environment": { "allow": ["CI", "NODE_ENV"] } } }
  ```

  Entries are exact names, matched without regard to case, with no patterns. A
  name that looks like a credential is refused and reported, never honoured. That
  covers a sensitive prefix such as `AWS_` or `GITHUB_`, or a word such as TOKEN,
  SECRET or PASSWORD, as in `GH_TOKEN`. A policy file lives in the repository. A
  single line in one must not be able to hand a cloud credential to whatever an
  install script runs. Adding an entry widens what contained code can see, so a
  project file that does it needs the same approval as an egress allowlist entry.

- **A contained tool cannot reach a program kept outside the project.** The
  sandbox grants your project, a throwaway home and nvx's own runtimes, and
  nothing else. A tool that keeps its executables somewhere else cannot run them.

  Playwright is the case that surfaced it. Its browsers live in
  `%LOCALAPPDATA%\ms-playwright` (`~/.cache/ms-playwright` elsewhere), and a
  contained process could not even list that directory. Name it and it works:

  ```json
  { "isolation": { "filesystem": {
      "allow_read_exec": ["%LOCALAPPDATA%/ms-playwright"] } } }
  ```

  The grant is read and execute only, never write, whatever else the policy says.
  Paths take `~`, `$VAR` and `%VAR%` so one policy file works across machines. A
  path that does not exist here is skipped with a warning, and the run goes on.
  Adding one widens what contained code may execute, so a project file that does
  it needs the same approval as an egress allowlist entry. On Windows the grant is
  scoped to that project's sandbox identity, not shared with every sandbox on the
  machine.

  **On Windows the grant is a real filesystem permission, and nvx takes it back
  when the policy stops asking.** It has to persist between runs, because
  re-applying it every launch would put a permissions call on the startup path
  for every root. So it is recorded, and reconciled against the policy each time
  you run something contained in that project. Remove the `allow_read_exec` entry, or the policy
  file, and the next contained run withdraws the permission. `nvx grants list`
  shows what is currently granted and `nvx grants reset` withdraws it immediately.

  Three cases are not automatic. `nvx grants reset --all`, which sweeps every
  project, clears the first. It can only report the other two, because in both
  it no longer knows which permission it would be removing.

  The identity is derived from the project root, so *moving* that root leaves the
  permission granted under the old identity unreconciled. The root is the nearest
  ancestor holding a `package.json`. So this needs a `package.json` to appear or
  disappear closer to your working directory than the current one. Adding one
  further up changes nothing. The stale permission cannot be *used* while stale,
  because a run at the old root reconciles it before the contained process
  starts. It stays on disk until such a run happens or you reset.

  The second is a grant record nvx cannot read. It keeps the file, renamed to
  `.unreadable`, and says so. It can no longer tell what that record listed, so
  those permissions are removed with `icacls` by hand. Records are written
  atomically, so this should take deliberate corruption to reach.

  The third is a granted directory that is **renamed**. The permission is attached
  to the directory, so it travels with it. nvx's record still names the old path.

  nvx cannot follow it and does not pretend to. It reports that the directory is
  gone, and that the permission moved with it if it was renamed and not deleted.
  It leaves you to remove it at the new location with `icacls`.
  Moving a granted directory is worth avoiding for that reason.

  A fourth case needs nothing cleaned up but is worth knowing about. A directory
  nvx granted read/execute may later be used as a working directory by the same
  project. nvx's own writable-root grant then replaces that permission with a
  wider one. Dropping the policy entry leaves the wider permission in place. nvx
  says so and leaves it, because taking it away would remove access granted for a
  different reason. The sandbox keeps that access until the project
  stops using the directory.

  Nothing else needs cleaning up by hand. Earlier builds of this feature left the
  permission behind entirely, with no way back but working out the capability SID
  and running `icacls` yourself.

  On Linux this grants reading, listing and executing. An earlier note here said
  listing was refused and called it a Landlock limitation. It was a wrong
  constant in nvx, since fixed.

- **On Windows, nvx stops a command once the program that started it has exited,
  and reports exit 129.** It checks every 15 seconds and needs two consecutive observations,
  so this lands 15 to 30 seconds after the parent goes away. It only applies when
  nvx's input is a pipe, which is the shape a long-lived stdio server is launched
  with.

  This exists because sandboxed MCP servers outlived their clients and
  accumulated until a machine froze. That freeze, measured 2026-08-27, held 18
  nvx processes, 43 Node processes and 3.9 GB. An ordinary shell pipeline is unaffected, because
  there the shell that built it is still running.

  **This can catch a command by surprise** when it is deliberately detached with
  a pipe still attached to its input. An example is Node's
  `spawn(cmd, {detached: true, stdio: 'pipe'})` where the launcher then exits.
  Detaching via `start /b` is unaffected, because that leaves the input a console
  and the check never arms. If you need a long-running job to outlive its
  launcher, give it a console or a file for stdin, and not a pipe.

- **`nvx audit` shows what nvx recorded, which anything running as you can add to.**
  A contained process cannot write `~/.nvx/audit.log`, but uncontained code can.
  At the default `standard` level your own code is uncontained. So
  `npm run build` could append a fabricated "sandboxed" entry that `nvx audit`
  then displays as real. The file has to be writable by nvx running as you, so it
  is writable by anything else running as you. The log is useful for reviewing
  your own usage. It is no proof against someone who already runs code as you.

- **On Windows, a server started inside the sandbox needs `--expose` to be
  reachable from the host.** Windows refuses connections into an AppContainer, so
  a contained `npx vite` binds its port, prints that it is listening, and serves
  nobody. `--expose` publishes it:

  ```
  nvx --expose 5173:8080 npx vite
  ```

  The two numbers cannot be the same. An AppContainer shares the host's network
  stack and does not get its own. So one port number cannot hold both the
  contained server and the host listener. In a test, the contained server lost
  the race and died on `EADDRINUSE`.
  Give the port your server uses inside, then the port you want to visit. With the
  second omitted (`--expose 5173`) nvx picks a free one and prints the URL.

  Nothing is relaxed to make this work. The contained side dials *outward* over a
  UNIX socket and the parent splices inbound requests onto it. No network
  capability is granted and egress stays exactly as restricted. That is asserted
  on every run of the probe, not merely intended.

  Your own `npm run dev` is uncontained at the default `standard` level and needs
  none of this. Until 0.5.6 there was no way to reach a contained server at all.
  The FAQ had claimed loopback worked for dev servers, which was measured false on
  2026-08-20.

- **A freshly published package stops an MCP server starting, for up to 24 hours.**
  nvx holds a cooling-off window on new npm releases. A version published in the
  last day is flagged before it installs, because supply-chain compromises are
  usually caught inside that window. The flag is a prompt, and a server your
  editor spawns has no one to answer it. So nvx denies the prompt and the launch
  aborts.

  Your client reports `-32000: Connection closed`, which is what it reports for
  any server that dies before answering. nvx now replies to the client's first
  request with the real reason, so the pipe is not silent. The message you see
  names the cooling-off window and the package.

  This affects any MCP server installed from npm on a floating version
  (`npx -y <pkg>` fetches the latest). It is self-inflicted if you publish the
  package yourself. Publish in the morning and the server is unavailable until
  the next day. Three ways out, narrowest first:

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
  `release_age.min_age_hours` does not. That setting widens the window for every
  package you install. The third is the broadest. `NVX_YES` also approves the
  typosquat, install-script and known-advisory checks for that server, so it is
  the last resort. It never approves a package OSV lists as malicious. Each check it approves is printed on stderr and written to
  `~/.nvx/audit.log` as a `check_approved` record.

  Until 0.6.0 the only exemption list was `typosquatting.trusted_packages`, which
  waived typosquat detection at the same time. It no longer waives the
  release-age window. A file still using it that way is told to move the entry.

- **On Windows and macOS, a contained tool needs `--connect` to reach a service
  running on your machine.** This is the other direction, for the same reason.
  The sandbox has no route to your loopback, and the egress proxy refuses a host
  loopback destination unless `allow_hosts` names it. A contained tool may need
  a service you already run, such as a browser with remote debugging on, a local
  database or a device emulator. It gets there one named
  port at a time:

  ```
  nvx --connect 9222:19222 npx @playwright/mcp --cdp-endpoint http://127.0.0.1:19222
  ```

  The in-sandbox port cannot also be 9222, for the network-stack reason above.
  Pick a second number, as above, whenever the command line has to name it.
  Your shell expands the command line before nvx runs, so a variable is no use
  there.

  With the second number omitted (`--connect 9222`) nvx chooses a free port,
  prints it, and sets `NVX_CONNECT_9222` in the sandbox. That form is for tools
  that read their endpoint from the environment or a config file at startup, not
  from argv.

  Nothing general is opened. nvx runs the listener inside, dials `127.0.0.1:9222`
  itself from outside, and closes both when the command exits. The sandbox never
  gets to choose a destination. This is deliberately not the machine-wide
  loopback exemption that 0.5.0 removed. That exemption opened every local
  service to every sandbox on the machine, permanently, and could not be revoked
  without elevation.

  **The boundary is the project.** Windows permits loopback *within* an
  AppContainer package, and every run of one project shares that project's
  package. Before 0.6.0 every nvx sandbox on the machine shared one package. A
  sandbox in an unrelated project, with no grant of its own, was then measured
  reading the service.

  Packages are per project now, so another project's sandbox
  cannot reach the in-sandbox listener. nvx also identifies the process behind
  each tunnel connection and refuses any it cannot place inside this run, and
  logs the refusal. Treat concurrent runs of one project as one trust domain all
  the same, and do not rely on `--connect` to keep them apart.

  **macOS reaches the same place by a different route.** What stops a contained
  tool there is the Seatbelt profile. In the default `proxy` mode it permits
  outbound connections to the egress proxy's own ports and nothing else. So the
  profile could simply name your service's port and be done.

  nvx runs the relay anyway, and the profile opens only the relay's port. The command, the
  two-number rule and `NVX_CONNECT_<port>` then mean the same thing on both
  platforms. The sandbox reaches a pipe whose far end nvx chose, and never an
  address it could have guessed.

  The peer check above is Windows-only, and is not missing on macOS. There, a
  process outside any sandbox can already open your service directly, so the relay
  hands it nothing. Another sandbox cannot reach the relay's port, because its own
  profile permits only its own proxy ports.

  **Linux tunnels it across the namespace.** Its sandbox has a network namespace
  of its own, so 127.0.0.1 in there is a different 127.0.0.1 from yours. The
  supervisor listens on the in-sandbox port and forwards over a UNIX socket in the
  guest home, which crosses because it is a filesystem object. nvx dials your
  service from outside, as everywhere else.

  `offline` cannot carry it on Linux. It denies the contained process every IP
  socket, including the one it would use to reach the tunnel. nvx says so and
  names the modes that work, so the flag is never accepted in silence. The
  default, `proxy`, carries it, and so does `loopback`.

  In a policy file it is `isolation.network.connect_ports`, and adding one counts
  as loosening, so a project cannot grant itself a host port without approval.

- **`network.mode: loopback` reaches services on 127.0.0.1 by three different
  routes, and Windows does not cover the same traffic as the other two.** On macOS
  the Seatbelt profile grants loopback directly. On Linux every loopback TCP
  connection is redirected to a relay inside the namespace. The relay asks the
  kernel what the connection was for and carries it out. So a raw connection to a
  local database arrives at the same address it named. On Windows the reach comes
  from nvx's proxy permitting loopback destinations. That covers what a
  proxy-aware client sends, HTTP and HTTPS, and leaves a raw socket with nowhere
  to go.

  A Linux host whose kernel will not take the redirect rules falls back to the
  Windows behaviour and says so. The run does not fail. What is lost is reach, not
  containment.

  The default `proxy` mode reaches loopback only where `allow_hosts` names it, and
  `offline` reaches nothing. Before 0.6.0 the mode did nothing at all on Windows
  and Linux. Each treated it as `offline`, so a mode whose name says "reach these
  services" reached none of them. Docker still does. It runs the
  mode with `--network none`, the same as `offline`.

  A server the **sandbox itself** runs stays reachable from inside it. The relay
  tries the sandbox's own namespace before the host. So a contained `npm run dev`
  on 127.0.0.1:3000 and a contained test client still find each other.

  Before 0.5.2 that was not true. Every restricted mode granted all of loopback,
  so a contained install could reach your database or another project's dev
  server with no `allow_hosts` entry. `offline` was not offline.

  If any reachable loopback service forwards traffic, the allowlist is bypassable entirely, which
  is what made it serious. Fixed, and pinned by a test. The fix is to the
  *generated profile*. A macOS runner now confirms that egress is denied with an
  empty allowlist. That does not by itself prove the per-mode loopback scoping
  (see the entry above).

## Scope

In scope are sandbox escapes, policy-bypass or policy-tampering vectors,
egress-allowlist bypasses, checksum-verification bypasses, and privilege
escalation caused by nvx.

Out of scope are vulnerabilities in Node.js or other managed runtimes
themselves, in npm packages, or in the operating system's sandbox primitives.
