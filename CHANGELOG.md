# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Security

* **"Builds coexist" was not true, and the prune that made it untrue could race a
  launch.** Staged supervisor copies were pruned on every launch, keeping only the
  copy the running process wanted -- so alternating a release and a dev build
  re-copied ten megabytes each time, and a second nvx could delete the first's
  copy in the window between staging it and executing it. That is a narrower
  version of the race per-build naming exists to remove.

  Launches no longer prune other builds; they only clear the legacy fixed-name
  copy and leftover temporary files. Reclaiming disk moved to `nvx cleanup`, where
  nothing is mid-launch. Verified by alternating two builds and watching both
  copies survive, then reclaiming them with `cleanup`.

* **The async-pipe limitation is documented for tool runners, not only installs.**
  `npx`/`bunx` tools that stream a child's output hang exactly as an install does,
  which README and SECURITY.md now say. The two-minute hint still covers installs
  only, deliberately: an install running that long is anomalous, an `npx`-launched
  dev server running for hours is working, and a timer cannot tell them apart. A
  hint that fired on the second would be noise, and a hint people learn to ignore
  is worse than none.

* **A typo in `isolation.network.mode` silently gave you more network than you
  asked for.** An unrecognised value fell through to proxy in every reader, so a
  policy saying `"offlin"` got a live egress proxy while its author believed they
  had asked for no network at all. Nothing warned. The neighbouring
  `isolation.level` already reported an unrecognised value, and this is the field
  where being wrong grants more access rather than less.

  Unrecognised modes now warn once, name the valid values, and are rewritten to
  proxy so no downstream reader falls into its own default arm -- which is also
  where the platforms diverged: the same typo produced proxy on Windows and Linux
  and no network rule at all on macOS. Warned rather than refused, matching
  `isolation.level`, and because proxy is the restrictive default: you asked for
  stricter than the default and get the default, loudly.

* **A corrupted staged supervisor bricked every contained launch, permanently.**
  The staged copy's name encodes the source binary's size and timestamp and the
  reuse check compares size, so a corruption preserving size -- an antivirus
  quarantine stub, a cloud-sync placeholder, a bad sector -- was invisible to it.
  Every later launch failed identically, and neither `cleanup` nor `doctor` could
  clear it. Found by an acceptance pass zeroing 512 bytes in place; it is the
  failure the previous commit set out to eliminate, reached by another route.

  An image error from `CreateProcess` now discards the staged copy and retries
  once. The trigger is narrow -- five image-specific Windows codes, matched by
  code rather than message text so it works on a localised Windows -- because a
  launch refused for permissions must not spin. The launch error is wrapped with
  `%w` for the same reason; it was `%v`, which stringified the code away.

* **The stale-grant check reported live grants as leftovers, and `--fix` deleted
  them.** The scan matched any AppContainer package SID on a directory, ignoring
  what the entry actually granted. But the current design writes `(X,RA)` --
  traverse and read-attributes, non-inheritable -- on the directories above a
  sandbox so it can walk through them without listing them. Run nvx from a
  subdirectory and the project root carries one, so on a healthy machine `doctor`
  announced it as a pre-0.5.0 leftover letting "any nvx sandbox read and write
  this project", exited non-zero, and offered to remove it. False in every clause
  for a traverse-only entry, and the removal took out a grant nvx had just
  written. The regex also matched entries belonging to unrelated AppContainer
  applications.

  The scan now matches on what an entry grants, not on the SID being present.
  Anything beyond the traverse pair is stale, a strict subset of it is not, and
  unreadable rights stay quiet -- this drives both a security claim shown to the
  user and a removal, so asserting either from an entry that could not be parsed
  is what caused the false positive. Legacy `(OI)(CI)(M)` grants still match.
  Introduced in the previous commit and caught by the next acceptance pass.

* **`nvx doctor` now finds the pre-0.5.0 grants that leave a project writable by
  every sandbox on the machine.** Up to 0.5.0 every sandbox ran as one shared
  package identity and the grants it wrote were never revoked, so a contained
  install in one project can read and write another. The cleanup only ever ran on
  the working directory of the session currently running, so a project you do not
  revisit stays exposed indefinitely.

  README has disclosed this since 0.5.0, and the containment behaviour is
  unchanged -- what was missing was any way to observe it. The comparable
  leftover, the loopback exemption, warns on every contained launch and fails
  `doctor`; this one was silent. An acceptance pass found 19 such grants live on
  the nvx repository itself while `doctor` reported a healthy install and exited
  0, and wrote into that repository from a sandbox scoped to a different project.

  `doctor` now reports leftover grants on the project it runs in, counts them
  against health, and removes them under `--fix`. nvx keeps no record of where it
  has run, so it cannot sweep the machine; the check answers for the directory you
  are standing in. It is not a launch-path warning because the launch path already
  removes them from the working directory before running anything.

  `docs/enforcement-matrix.md` said an unqualified "Yes" for Windows write
  containment, contradicting README's own Known limitations. Corrected.

### Fixed

* **A hung contained process could block every later contained launch.** The
  sandbox supervisor was staged under one fixed name, and Windows refuses to
  replace a running executable -- so a supervisor still alive from an earlier
  build held that file and the next launch died at staging with a bare "Access is
  denied", naming a path inside `~/.nvx` that nobody would connect to their stuck
  process. The only cure was finding and killing it by hand. Asynchronous piped
  stdio still hangs, so leaving a supervisor alive is not a rare accident.

  Staged copies are now named per build, so nothing is ever replaced: builds
  coexist and a leftover copy is inert rather than blocking. Copies from other
  builds are pruned when they are not in use, failures ignored, because a copy
  that will not delete is one another sandbox is executing. The legacy fixed-name
  copy is cleared on the next launch.

  The name is derived from the binary's size and timestamp rather than a content
  hash: this is a launch path measured in milliseconds and hashing ten megabytes
  to learn what a stat already tells us would be a poor trade.

* **The macOS network mode was the only reader of that field not trimming
  whitespace.** A policy carrying `"mode": "proxy "` was `proxy` on Windows and
  Linux and matched no case in the Seatbelt switch, so macOS emitted no network
  rule at all. It failed closed, but it was a silent, platform-divergent
  behaviour change from one trailing space in a config file.

### Security

* **On a platform with no sandbox, nvx ran the command unprotected and said it
  had contained it.** `sandbox_native_other.go` -- the path for any Unix that is
  not Linux or macOS -- set a process group, logged "using environment isolation
  only" at info level, and then executed the command. nvx printed "Running in
  native sandbox" around it. A contained install on FreeBSD therefore had full
  access to the user's home, their SSH keys and the network.

  This is exactly what SECURITY.md's design stance rules out: if a containment
  primitive is unavailable, refuse to run rather than run unprotected. Every other
  platform honoured it. It now refuses, naming `--no-sandbox` as the deliberate
  opt-out. A scrubbed environment and a redirected HOME are worth having but are
  conventions the child can ignore, so they do not amount to a sandbox and are no
  longer presented as one.

  No release binaries are published for these platforms, so this was reachable
  only by building from source -- which is exactly the person who would trust the
  word "sandbox" without checking. Closes F28, and with it F33, the last open
  contradiction of the fail-closed claim. Pinned by
  `TestUnsupportedPlatformRefusesInsteadOfRunningUnprotected`, which asserts the
  command left nothing behind rather than trusting its exit code.

* **On macOS, a contained process could reach every service on your loopback
  address.** Every restricted mode emitted `(allow network-outbound (remote tcp
  "localhost:*"))`, so an install could reach a local database, a daemon's TCP
  port or another project's dev server with no `allow_hosts` entry. Worse than it
  first sounds: any reachable service that forwards traffic -- a debugging proxy,
  `ssh -D`, a dev server's proxy route -- turns that into unrestricted egress, so
  the allowlist stopped meaning anything. `network.mode: offline` was covered by
  the same rule, so offline was not offline. Present since the sandbox was first
  implemented.

  Loopback is now granted per mode. `proxy` reaches the proxy's own ports and
  nothing else; `offline` emits no network rule at all; `loopback` keeps the
  wildcard, that being the entire meaning of the mode. A `proxy` launch with no
  known proxy port now grants nothing instead of falling back to the wildcard.
  The per-port rules that already existed were dead code -- the wildcard above
  them had subsumed them since the beginning.

  Pinned by `TestSeatbeltGrantsLoopbackOnlyWhereTheModeMeansIt`, confirmed to fail
  against the previous behaviour. This fixes the generated Seatbelt profile; per
  the enforcement matrix, no macOS hardware has yet confirmed the kernel enforces
  any part of that profile.

### Changed

* **The docs stop claiming macOS enforcement nobody has tested.** Three tables
  printed the same "Yes" for Windows, Linux and macOS on guarantees backed by very
  different evidence -- measured, CI-tested, and never checked respectively. macOS
  cells now read "profile only" and say what that means. The one macOS check in CI
  asserts that a sandboxed process can write its own working directory and nothing
  about what is blocked, so it would pass against a build whose sandbox blocked
  nothing.

* **Two documents contradicted each other on a security question.** SECURITY.md
  said macOS egress control was cooperative and a raw socket could bypass the
  allowlist; README said macOS blocks raw TCP/UDP at the OS. README was right --
  the profile is `(deny default)` -- so SECURITY.md understated the design while
  the matrix overstated the evidence for it. Both corrected.

## [0.5.1] - 2026-08-19

### Fixed

* **`npm install esbuild` hung forever inside the sandbox, and now does not.**
  A contained process may not create a named pipe, and Windows implements piped
  child stdio with named pipes, so a postinstall that captures its own
  subprocess blocked inside libuv before the child existed. 0.5.0 shipped this
  as an unfixable limitation. That was half right.

  The restriction really is unliftable, and that is now measured rather than
  assumed: `CreateNamedPipeW` inside a real AppContainer returns
  `ERROR_ACCESS_DENIED` for every name shape tried, so it is the NPFS device
  refusing and no name routes around it. Granting it would mean loosening
  `\Device\NamedPipe` for every AppContainer on the machine, UWP apps included.

  But file descriptors were never restricted, and synchronous capture does not
  need a stream -- its whole contract is "run it, give me the output at the
  end". A preload in every contained node process now routes `spawnSync`,
  `execSync` and `execFileSync` through temp files in the guest home. esbuild
  installs in seconds and the resulting binary bundles correctly. The preload
  falls back to the original function on any error, because it loads into every
  contained node process and must never be the reason one fails.

  Async `spawn(..., {stdio:"pipe"})` is a real stream a file cannot stand in
  for and still hangs; it stays under Known limitations with the two-minute
  hint. Containment is untouched -- this changes how a contained process talks
  to its own children, not what it can reach.

* **An AppContainer's temp directory never existed.** Windows redirects a
  sandboxed process's temp to `<LOCALAPPDATA>\Packages\<pkg>\AC\Temp`, ignoring
  the `TEMP` nvx sets. Nothing created it, so `os.tmpdir()` inside the sandbox
  pointed at a missing directory and every scratch file failed with ENOENT --
  which is most tools. Found while diagnosing the above.

* **The lifecycle smoke fixture could not fail on the bug it was written for.**
  Its postinstall only wrote a file; it never captured a subprocess, so it
  passed while `npm install esbuild` hung. It now captures a child and asserts
  the captured text. Verified by disabling the preload and watching the smoke
  hang.


## [0.5.0] - 2026-08-18

Everything below shipped in the `v0.5.0` tag. Entries that sat under
[Unreleased] while the tag was cut have been folded in: for a security tool
whose changelog is the disclosure channel, a reader of the 0.5.0 section must
see what the build they downloaded actually contains.


### Security

* **One sandbox could borrow another's egress allowlist.** Every nvx sandbox on
  a machine shares one AppContainer package identity, and Windows scopes its
  loopback restriction to the package -- so two projects running at once sat in
  the same loopback namespace. An acceptance pass port-scanned loopback from
  project B, found project A's relay, and tunnelled to a host only A's policy
  allowed, in a run where B's own proxy refused that host. The host-side proxy
  listeners had the same exposure to any local process.

  Each session now mints a random credential and its proxy requires it, over
  HTTP and SOCKS alike. It travels as ordinary proxy credentials in HTTP_PROXY,
  so npm, node and curl send it without knowing anything about nvx, while a
  sibling that found the port by scanning gets 407. Authentication is checked
  before the allowlist, so the 403-vs-200 difference cannot be used to probe
  what another session may reach. Replaying the original attack now yields 407
  from both the relay and the host proxy.
* **A loopback exemption left by an older `nvx setup` opened every service on
  127.0.0.1 to contained code, and nothing said so.** The whole Windows egress
  design rests on Windows refusing an AppContainer's loopback connections. Before
  0.5.0 the proxy ran on the host's loopback, so an elevated `nvx setup`
  registered an exemption to reach it -- which also opened local databases,
  daemon ports and other dev servers. 0.5.0 never registers one and removes it
  during `nvx setup`, but that command needs an Administrator terminal and this
  release tells users it is no longer required, so on an upgraded machine the
  exemption simply stayed: the allowlist looked enforced, other hosts really were
  blocked, and loopback was wide open.

  nvx cannot remove it unelevated, so it now detects it and warns on every
  affected contained launch -- every launch, not once, because it is a live
  weakening of what the command was asked to do. `nvx doctor` reports it and
  exits non-zero. Both print the exact removal command. A clear result is cached
  for a day so a healthy machine pays nothing per launch; the exempt result is
  never cached, so the warning stops the moment the exemption is gone.

  Worth recording why this survived: `TestLoopbackIsNotAutomaticallyAllowed`
  exercises the proxy's allow decision, which cannot see an OS-level exemption,
  so no test failed while the guarantee did. The new check is pinned against the
  machine's real exemption list instead.

* **A contained install could plant a command that ran later, uncontained, as
  you.** `nvx use` puts the project-bin shim directory near the front of PATH,
  ahead of System32. That directory used to live inside the project, so a
  postinstall could write a file called `git` into it and wait; the next `git`
  the developer typed ran it with their full user token -- every credential
  store, every project, unrestricted network. No sandbox bug was involved. The
  containment held and was routed around by a directory nvx itself put on PATH.
  Reproduced end to end in PowerShell and Git Bash.

  Two changes, and it needs both. The directory moved under `~/.nvx`, which the
  sandbox cannot write. And generation now refuses to shim a name that already
  resolves elsewhere on PATH, because `node_modules/.bin` is itself writable by
  an install -- so relocating alone would just move the plant one directory
  back. Stale wrappers are pruned on every regeneration.

  The cost, stated plainly: if you have a global tool of the same name, the
  project-local one no longer wins through nvx. `npx <tool>` still runs the
  local one, contained.

* **`--agent-mode` let a repository switch containment off.** The flag exists
  for AI agents, and it sets the same blanket yes as `-y`/`NVX_YES` -- which
  covered the two prompts that decide the security model itself: trusting a
  project's own `.nvx-policy.json` when it loosens settings, and adding a host
  to the egress allowlist. So an agent cloning a repository it had not read
  would auto-approve that repository's request to disable the sandbox, and the
  approval persisted. Measured: a policy carrying
  `{"isolation":{"enabled":false}}` was refused without the flag and silently
  trusted with it; arbitrary egress hosts, including an IP on a C2-style port,
  were approved and written to the grants store.

  Those two prompts no longer accept a blanket yes. They need an interactive
  answer, or `NVX_TRUST_YES` -- deliberately not `NVX_YES`, because nothing
  sets it by habit. Ordinary prompts (vulnerability warnings, install-script
  confirmations) still honour `-y`, so non-interactive installs do not stall.

* **Permissions left by pre-0.5.0 nvx were only partly cleaned.** The cleanup
  removed one identity, on the project you happened to be standing in --
  measured at 19 stale entries on a single directory, each still granting
  modify access to every sandbox. It now removes every AppContainer package
  identity from the directories it grants. Projects nvx never revisits still
  keep theirs, which README.md and SECURITY.md now say plainly along with the
  manual command.

* **Git Bash got no protection at all on Windows, and `nvx doctor` said it was
  fine.** The shim directory held only `npm.cmd`/`npm.ps1`, which bash never
  selects: it looks for a file named exactly `npm` and does not consult PATHEXT.
  A bare `npm install` in Git Bash therefore ran the real npm -- unaudited,
  unsandboxed -- while `doctor` reported interception as healthy, because it was
  answering the PATHEXT question rather than the one bash asks. Agent harnesses
  on Windows commonly run Git Bash, which is the case nvx exists for.

  Extensionless shims are now written alongside the others, and `doctor` reports
  their absence and no longer calls that state healthy.

* **Security prompts hung instead of failing closed when stdin was not a
  terminal.** `PromptYesNo` opened the console directly and treated "a console
  exists" as "a human is present". On Windows `NUL` is itself a character device,
  so `< /dev/null` -- what CI steps and agent harnesses do -- looked interactive,
  and an install stopped at a prompt nobody could answer. README and SECURITY.md
  both promise the operation is denied in that case. Interactivity is now decided
  by `GetConsoleMode` on Windows and a terminal ioctl on Unix. Measured: an
  install that previously hung past 90s now denies and exits in 2s.

### Fixed

* **`npm install esbuild` hung forever inside the sandbox, and the docs said it
  could not.** SECURITY.md and README claimed npm installs were unaffected by
  the named-pipe restriction because lifecycle scripts inherit stdio. That fixes
  npm's own piping and nothing else: a postinstall that captures its OWN
  subprocess still blocks, because the restriction is on the contained process
  creating the pipe. esbuild's postinstall does exactly that. Measured at no
  completion after 13 minutes contained, against 8 seconds uncontained.

  This is an OS restriction nvx cannot lift, so it is now stated as a known
  limitation instead of denied, with the workaround (`--no-sandbox` for that
  package). A contained install still running after two minutes prints a hint
  naming this cause, because the failure mode was silence -- and silence reads
  as "nvx is broken" rather than "this package needs a flag". The smoke test
  that was supposed to catch this could not: its fixture's postinstall only
  writes a file, never capturing a subprocess. That is now recorded next to the
  claim it failed to check.

* **`nvx policy init` still scaffolded a fourth dead key.**
  `isolation.filesystem.mode` was missed when the other three inert keys were
  dropped, so every new policy shipped a security-looking key whose value read
  "strict" and which no decision consulted. It is gone, along with the empty
  `"prompts": {}` that pointed readers back at the removed keys, and the dead
  defaulting and merging code behind them.

* **Upgraded machines let the sandbox list `~/.nvx`.** Homes created before
  0.5.0 carry a read+execute grant where fresh ones get traverse-only, and the
  "is it already granted" check answered yes to either, so it was never
  narrowed. nvx now narrows it once per home.

* **Three wording corrections where the text overstated the guarantee.** The
  loopback-exemption warning said egress to other hosts was unaffected -- an
  acceptance pass disproved it by completing a TLS exchange with an external
  host through a CONNECT proxy on loopback, so any forwarding service on
  127.0.0.1 makes egress arbitrary. `--help` still advertised `nvx setup` as
  adding allowlisted egress, which 0.5.0 removed. And the install-script prompt
  asked whether to run scripts "on your host" when they run contained.

* **The removal command was hidden by `-q` and `--agent-mode`,** leaving the
  loopback warning visible and its fix invisible. It is a warning now, not an
  info line.
* **`nvx use` silently did nothing in Git Bash on Windows, and reported
  success.** Shell detection always answered PowerShell there, so nvx emitted
  assignments bash cannot evaluate: nothing applied, `node -v` was unchanged,
  and it still printed "Now using Node.js". Auto-switch on `cd` never fired
  either. Git Bash and MSYS2 are now detected.

* **`--agent-mode` is documented as `-y -q` and only ever did the `-y` half.**
  It now sets quiet too. That gates success and info lines only; warnings and
  errors still print.

* **`nvx setup --undo` could not remove the profile-root grant** that README
  and SECURITY.md said it removed. It now sweeps that path as well.

* **Four documented policy keys did nothing.** `prompts.interactive`,
  `prompts.non_interactive`, `prompts.network_unknown` and
  `isolation.filesystem.mode` were parsed, merged, scaffolded by
  `nvx policy init` -- and read nowhere, so tightening one was silently
  ineffective. They are no longer written into new policies and README says
  they are unimplemented. Existing policies still parse.

* **A stray `ACC_WRITE_PROBE.tmp` shipped inside the v0.5.0 tag** -- a
  reviewer's escape probe swept in by `git add -A`. Removed, and `*.tmp` is
  now ignored.

* **A contained command took seconds to start.** The sandbox grants traverse
  rights on the directories above your project. On some chains -- `AppData` and
  below, where a filter driver stalls the ACL write -- that grant never
  completes, and nvx retried it on every single launch, burning its whole 3s
  budget each time for something that could not succeed.

  Measured before changing anything: the grants are not needed. With the
  ancestor walk skipped entirely a contained process still launches, stats and
  writes its working directory, and most of those directories are reachable
  anyway from permissions Windows already sets. Failed grants are now remembered
  and not retried for a week; one that succeeds clears the record. Same command,
  same machine: **5.3s to ~1.05s**.

  Measure it on your own machine before relying on that figure. An independent
  review measured **~2.2s** steady state on the same fix, against 0.4s
  uncontained, and the first contained run after a new runtime is staged costs
  **45s to 3 minutes** while the runtime is copied. The 1.05s above was one
  measurement on a warm home with a managed runtime already in place, published
  as though it were the number; it is the best case, not the typical one.

* **`nvx node --strict app.js` ran uncontained.** `--strict` was stripped from
  the wrapped command's arguments and then discarded, so you got neither the
  containment you asked for nor an error saying you had not got it -- and
  `nvx help` shows the flag without saying it must come first. It is now
  honoured wherever it appears. `--standard` deliberately still is not: it
  reduces containment, so a dependency's own arguments must not be able to
  weaken the sandbox around it.

* **A contained process could list the names inside directories nvx granted for
  traverse.** The ancestor grant used `(RX)`, which includes list-folder. It is
  now traverse and read-attributes only, which is what it was always described
  as being. Your home directory itself remains listable from any sandbox -- that
  is an ACE Windows ships, not one nvx adds -- and is now stated in
  `docs/enforcement-matrix.md` rather than implied away.

* **Installing any package with a lifecycle script hung forever on Windows.**
  A process inside an AppContainer is not permitted to create a named pipe, and
  Windows builds piped child stdio out of named pipes -- so npm's default of
  piping lifecycle-script output blocked inside libuv before the child process
  existed. The target's own timeout never fired, because its event loop never
  got another turn. This affected exactly the class of package the sandbox
  exists to contain, and `npm install` of anything with a `postinstall` never
  returned.

  Lifecycle scripts now inherit stdio instead, which npm supports directly.
  Their output goes to the terminal as it happens rather than being buffered,
  which for a tool whose job is to show you what a package does during install
  is not a regression.

  Found by an independent acceptance pass, not by the test suite: every existing
  test launched contained children *from the parent*, and none had a contained
  process spawn one. `scripts/sandbox-smoke.ps1` now installs a dependency
  carrying a postinstall and fails if it does not finish, and that check has been
  confirmed to fail against the previous behaviour.

  The underlying restriction remains: anything else inside the sandbox that
  captures a child's output still hangs. That is recorded in README.md under
  Known limitations and pinned by a probe, so the workaround can be removed if
  the restriction ever lifts.


### Security (continued)

* **A sandboxed command could reach every project nvx had previously run in, and
  read credentials a trusted tool had persisted.** The AppContainer profile is
  stable by design, so every session ran as the same identity, and the filesystem
  permissions nvx grants were never revoked. A permission added while installing
  in project A was therefore still valid when nvx later ran in project B.
  Measured: a contained install read *and wrote* a second project's files, read a
  concurrent session's guest home, and read a `tool_home` profile's credential.
  This contradicted README.md, which claimed other projects were unreachable.

  Writable roots are now granted to a capability derived from the project rather
  than to the shared identity, so a session elsewhere does not hold it. Stale
  permissions from earlier versions are removed the first time nvx runs in an
  affected project. Sessions in the same project still share one identity, which
  is deliberate.

* **The new egress relay had opened a route to services on your own machine.**
  The proxy permitted every loopback destination unconditionally, which was
  harmless while nothing contained could reach the proxy at all. Relaying to a
  proxy that runs outside the sandbox changed that: a contained process could
  reach any local service -- a database, a dev server, another agent -- with an
  empty allowlist. Loopback is now allowlisted like any other destination.
  `network.mode: loopback` still permits it; `offline` no longer does.

### Fixed (continued)

* **`nvx doctor` rewrote your persistent PATH without being asked.** It repaired
  a shadowed PATH the moment it found one, and because the repair targets
  whichever `NVX_HOME` is set, pointing that at a throwaway directory silently
  fronted the real user PATH with it. Diagnosis is now read-only; the repair
  moved behind `nvx doctor --fix`, which the report points at. A flag rather
  than a prompt deliberately: `NVX_YES` is set as a matter of course by agents
  and CI, so a prompt would auto-approve a persistent system change for exactly
  the callers least able to notice it.

* **An interrupted install blocked that version from ever installing again.**
  The lock file recorded a pid that nothing read back, so Ctrl-C during a
  download left a lock no command cleared -- `nvx cleanup` does not touch
  install locks -- while the error said "already in progress", sending you after
  a process that no longer exists. A lock whose owner is provably gone is now
  cleared and reported. One whose owner is alive, or whose contents cannot be
  parsed, is still respected.

* **`nvx cleanup` deleted guest homes belonging to sandboxes that were still
  running**, so running it during a concurrent install destroyed that install's
  `HOME` mid-run. Sessions now record their owning process and are skipped while
  it is alive.


**0.4.0 was tagged but never published.** The tag was cut, then held back
because the README it shipped with made three claims about the sandbox that
were subsequently disproved by execution — including that a malicious package
could not read your `.env`, which it can. Those claims were corrected before
this release. So 0.5.0 is the first published build carrying the 0.4.0 fixes
as well as its own, and the newest downloadable build before it remains
`v0.2.0-beta`.

### Security

* **Windows egress is now actually restricted to the allowlist.** It was not
  before, on any released build. The sandbox held the `internetClient`
  capability, so `HTTP_PROXY` was a request a package could simply decline —
  measured against 0.4.0, a `postinstall` script opened connections to
  `1.1.1.1:443` and `registry.npmjs.org:443` with no restriction at all.

  The AppContainer is now granted no network capability, so Windows itself
  refuses direct connections and DNS does not resolve. The parent's egress proxy
  is exposed on an AF_UNIX socket — a filesystem object, which the AppContainer
  network restriction does not cover — and a new in-container supervisor,
  `nvx __appcontainer-exec`, re-exposes it as loopback TCP for tools that only
  understand `host:port`. That is the same parent-proxy-plus-relay shape Linux
  already used, and it needs no elevation.

  The same `postinstall` script now gets `EACCES` and `ENOTFOUND` for both hosts
  while `npm install` completes normally against the real registry.

* **`nvx setup` no longer registers a loopback exemption, and removes an existing
  one.** The exemption was how a sandbox reached the proxy before the relay; it
  also let the sandbox reach every other loopback listener on the machine. With
  the relay it grants access for no remaining reason. Setup is now only about
  drive-root stat access, and is no longer needed for allowlisted egress.

### Fixed

* **Every sandboxed command failed on Windows without an nvx-managed runtime.**
  A runtime nvx does not manage is copied somewhere the sandbox can reach, and
  that copy walked the source with `filepath.Walk` — which inspects each path
  with `Lstat`, so a directory *link* arrived looking like a file and the copy
  tried to open its own destination folder for writing. nvm for Windows makes
  `C:\Program Files\nodejs` exactly such a link, and it is how most Windows
  developers install node, so the whole sandbox died with
  `open <nvxHome>\sandbox-exec\<hash>: is a directory` — a message naming
  neither node nor the link.

  Staging now recurses with `os.ReadDir`/`os.Stat`, which follow both kinds of
  Windows directory link. Resolving the path up front would have fixed only
  half: a symbolic link sets `ModeSymlink` and `filepath.EvalSymlinks` resolves
  it, but a junction reports `ModeIrregular` and `EvalSymlinks` returns it
  unchanged with no error. Links below the root are followed too; previously
  they would have been copied as empty folders, leaving a runtime missing files
  and failing later somewhere unrelated.

* **The same path then launched node against a directory it could not read.**
  The staged copy was used for the interpreter while `npm-cli.js` still pointed
  at the original directory, so node failed with `Cannot find module
  C:\Program Files\nodejs\node_modules\npm\bin\npm-cli.js`. The command is now
  made reachable before it is rewritten, so both come from the copy.

### Changed

* `network.mode: open` is now the only mode that grants the Windows sandbox a
  network capability. An unrecognised mode relays rather than connecting direct,
  so a typo cannot silently disable the allowlist.
* Proxied Windows runs fail closed if the egress socket cannot be created, rather
  than falling back to a direct connection.

## [0.4.0] - 2026-08-18

**0.3.0 was never published.** It has a dated entry below and `version.go` claims
it, but no `v0.3.0` tag exists, so the newest downloadable build is `v0.2.0-beta`
— which predates every fix listed here. Anyone tracking releases has none of this.

### Security

Each of these was reproduced by execution, not inferred from reading.

* **Linux containment did not work at all.** Every sandboxed launch failed on
  `/dev/null`: the read-only rules requested a directory-only Landlock right on a
  character device, which the kernel rejects, and the failure was fatal. It failed
  closed, so nothing was exposed — the feature was simply dead on every Linux system.
* **Linux proxy mode could not reach allowed hosts.** The egress proxy was started
  *inside* the loopback-only network namespace, leaving allowlisted traffic no route
  out. The proxy now runs outside the namespace and the contained process reaches it
  over a UNIX socket, which a namespace does not contain.
* **The Linux seccomp filter was inverted**, allowing the UDP it claimed to block
  and denying the AF_UNIX the sandbox needs. Unreachable while containment was dead;
  fixed alongside it.
* **`linux/arm64` used the wrong syscall numbers** — every entry one too high, so
  `landlock_restrict_self` invoked `memfd_secret` and the error misleadingly blamed
  the kernel version. This is a published release target.
* **The Linux sandbox could read all of `~/.nvx`**, including other tools' persisted
  credentials in `tool_home`, the grants pin store, and `policy.json`. Narrowed to
  the runtime trees it actually needs.
* **macOS granted the sandbox write access to `~/.nvx` and the runtime directory**
  on the default path, so a contained process could rewrite `policy.json`,
  self-approve grants, poison `npm_global`, or replace the `node` binary every later
  run executes. A persistent sandbox defeat.
* **Windows never delivered piped stdio to the sandboxed child**, so every
  stdio-protocol daemon — that is, every MCP server — failed deterministically, while
  interactive use looked healthy.
* **Orphaned sandbox processes were not cleaned up** (Linux reaping, Windows job
  object), so abandoned launches accumulated until the tool had to be removed.
* **A cached binary path is now validated before execution** instead of being
  trusted to still be on `PATH`.
* **Fixed a data race in the egress proxy** that could abort a run outright: the
  session map was read without the lock guarding its writes, and ordinary parallel
  package-manager traffic could trigger it.

### Changed

* **macOS: `npm install -g` inside the sandbox is now denied**, matching Windows and
  Linux. Global installs write under `~/.nvx`, which is no longer writable from
  inside. This is the documented design, but it is a behaviour change for anyone who
  relied on the gap.
* **Windows: `nvx setup` is optional.** The sandbox runs unelevated; setup adds the
  loopback exemption that enables allowlisted egress.
* **Windows sandbox startup is roughly 13x faster** — a measured launch went from
  90.7s to 6.6s. The cost was an ACL walk re-granting directory ancestors on every
  launch, hanging on one of them until it timed out.
* **Uncontained runs are announced** rather than proceeding silently.

### Fixed

* **Documentation corrected where it overclaimed.** `README.md`, `SECURITY.md` and
  `docs/enforcement-matrix.md` stated that Windows restricts egress to the policy
  allowlist. It does not, unless an elevated `nvx setup` has run: by default the
  sandbox is granted `internetClient` and the proxy variables are stripped, so the
  allowlist is never consulted — not even cooperatively.
* **CI verifies containment instead of reporting green.** Both Windows smoke tests
  exited 0 unconditionally on CI, and the egress test asserted only that blocked
  traffic fails — which a sandbox denying everything passes perfectly. The privileged
  Linux tests now run, and both egress tests assert the allow path too.

### Added

* `nvx doctor` — diagnoses and repairs shim interception, including a shadowed
  persistent `PATH` on Windows.
* `nvx grants list` and `nvx grants reset [--all]`.
* `nvx import`, quiet/agent-mode flags, and wildcard trusted-package patterns.
* Containment v2: subcommand-aware classification, `isolation.level`
  (`standard`/`strict`), and `--strict`/`--standard` flags.
* Persistent per-tool guest profiles under `~/.nvx/tool_home`, so a trusted tool
  keeps its own state without being handed the real home directory.

### Known limitations

* **Windows egress is not allowlisted without an elevated `nvx setup`.** A
  no-elevation design has been shown feasible — an AppContainer can reach an AF_UNIX
  socket held by the parent, and intra-container loopback works — but it needs an
  in-container supervisor that does not exist yet.
* **The macOS fixes are verified at profile-generation level only.** Whether
  `sandbox-exec` enforces the generated profile as written has not been re-tested on
  macOS hardware.
* **The sandbox re-entrancy marker is a plain environment variable** and can be
  forged by a process able to set its own environment.

## [0.3.0] - 2026-07-05

### Added
* **Bun runtime**: `nvx install bun@1.2` (and `bun`/`latest`), managed the same way as Node.js with mandatory checksum verification. `bun`/`bunx` shims route to the Bun provider.
* **`runtime@version` CLI**: install/use/default/uninstall accept a runtime prefix; a bare version stays Node.js for nvm compatibility. Node and Bun can be active in one shell without evicting each other from `PATH`.
* **FilesystemProvider registry**: `native` and `docker` are first-class; `wsl`/`wslc`/`systemd-nspawn` are gated behind `NVX_EXPERIMENTAL=1`. An unavailable backend (e.g. Docker not running) fails closed before launch.
* **Docker hardening**: image selected per runtime; `offline`/`loopback` enforced with `--network none`; `--cap-drop=ALL`, `no-new-privileges`, `--pids-limit`, `tmpfs /tmp`.
* **Audit log**: `~/.nvx/audit.log` records egress allow/deny and policy-trust events.
* **Docs**: `SECURITY.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `docs/runtime-providers.md`, `docs/enforcement-matrix.md`, and a tag-triggered release workflow.

### Changed
* **JS runtime focus**: shipped runtimes are Node.js and Bun only. Deno, Go, Python, and uv/pyx work remains on the `feature/polyglot-runtimes` branch.
* **Project policy trust**: approved egress hosts persist under `~/.nvx/grants` (outside the project tree) instead of `.nvx-policy.json`. A project policy file that would weaken settings is ignored unless its exact contents are trusted for that project (prompted once; fail-closed when non-interactive).
* **Fail-closed policy parsing**: the Linux sandbox child aborts on a policy parse error instead of falling back to defaults.
* **Faster shims**: resolved runtime binary paths are cached (keyed by `PATH`) so the shim skips the expensive Windows `PATH` scan on repeat calls — dispatch overhead drops from ~100 ms to ~38 ms on Windows, and measures ~3 ms on Linux and ~4 ms on macOS (GitHub-hosted runners). See `scripts/bench.py`.

## [0.2.0-beta] - 2026-07-02

### Added
* **Isolation v1 policy schema**: `isolation.filesystem.provider` and `isolation.network.mode` replace the flat `isolation.provider`; top-level `runtime` and `prompts` blocks.
* **Shim-only sandbox path**: `npm`, `node`, `npx`, `yarn`, `pnpm`, and `bunx` run sandboxed by default when `isolation.enabled` is true; use `--no-sandbox` to bypass per invocation.
* **Embedded egress proxy**: `network.mode: proxy` starts an in-process HTTP CONNECT + SOCKS5 proxy on loopback with policy allowlist and interactive approval for unknown hosts (persisted to `.nvx-policy.json` on approve).
* **RuntimeProvider execution hooks**: binary resolution and default network allowlists go through `RuntimeProvider` so sandbox code is not Node-specific.
* **Cross-platform smoke tests**: filesystem, egress block, and macOS runtime smokes in CI.
* **`nvx policy init`**: scaffold global and project policy files.
* **Project bin shims**: sandbox `node_modules/.bin` tools via `.nvx/project-bin/`.

### Changed
* **Default isolation**: `isolation.enabled` defaults to `true`; `network.mode` defaults to `proxy`.
* **Removed legacy CLI**: `nvx sandbox`, `nvx s`, `nvx exec`, and the `nvxs` shim target are removed; shims are the sole sandbox entry point.
* **Fail-closed Windows native path**: AppContainer setup failure no longer falls back to Low IL alone.
* **Linux network isolation**: loopback-only network namespace with in-child egress proxy; seccomp blocks UDP and offline TCP.

### Removed
* **`--provider` flag**: use `--filesystem-provider=` on shim invocations instead.

## [0.1.0] - 2026-06-30

### Added
* **Multi-Platform Swapping**: Zero-dependency swapping of Node.js versions in under a millisecond by modifying only session-level shell environment variables (`PATH`, `NPM_CONFIG_PREFIX`), supporting PowerShell, Zsh, and Bash.
* **Auto-Configuration Swapping**: Instantly switches Node.js version when navigating into directories containing configuration files (`.nvmrc`, `.node-version`, `package.json`, or Volta configurations).
* **Dynamic PATH Shim Architecture**: Uses dynamic shims in `~/.nvx/bin` to intercept execution reliably in subshells, IDEs, and scripts, resolving early shell alias vulnerabilities.
* **Registry Checksum Integrity**: Enforces cryptographic integrity for Node.js downloads using SHA-256 hashes from nodejs.org, mitigating MITM or server compromise attacks.
* **Interactive Security Interceptor**: Intercepts `npm`, `yarn`, and `pnpm` install commands to perform:
  * Vulnerability scans against the OSV database.
  * Typosquatting audits based on Levenshtein distance and registry download comparison.
  * Release-age warning for packages published less than 24 hours ago.
  * Install script blocking/warning to prevent arbitrary code execution during dependencies installation.
* **Flexible Process Sandboxing**: Runs executions inside isolated environments across platforms with selectable filesystem providers: OS-native isolation (`native`), Docker containers (`docker`), Microsoft WSL Containers via `wslc.exe` (`wslc` — Hyper-V utility VM, separate from WSL distros), default-WSL-distro fallback (`wsl`), macOS Seatbelt sandboxing (`sandbox-exec`), and Linux volatile containers (`systemd-nspawn`, requires root). Providers are selected via `isolation.filesystem.provider` or the `--filesystem-provider` shim flag, and unknown providers fail closed.
* **Project-Scoped Tool Isolation**: Optional `environment.isolated_tools` policy setting scopes globally installed npm packages to `<project>/.nvx/npm_global`, so different projects can pin different versions of global CLI tools.
* **Fail-Closed Prompts**: Security prompts deny by default in non-interactive environments (including CI); approval requires an explicit `-y` / `--yes` or `NVX_YES=true`.
* **CI Integration**: Added remote GitHub Actions CI pipeline testing across Windows, macOS, and Linux matrix with `gosec` and `govulncheck` static analysis scanners.
